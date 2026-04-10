// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/nomad/api"
	"github.com/ovh/go-ovh/ovh"
)

// ovhTask represents an OVH dedicated server task.
type ovhTask struct {
	Id       int64  `json:"taskId"`
	Function string `json:"function"`
	Comment  string `json:"comment"`
	Status   string `json:"status"`
}

// Cart and ordering types.
type orderCartOpts struct {
	OvhSubsidiary string `json:"ovhSubsidiary"`
}

type orderCart struct {
	CartId string `json:"cartId"`
}

// Cart ordering defaults for OVH Eco dedicated servers.
const (
	orderDuration    = "P1M"     // ISO 8601 monthly duration
	orderPricingMode = "default" // standard pricing
)

// Polling timeouts and intervals for long-running OVH operations.
const (
	orderDeliveryTimeout  = 2 * time.Hour
	orderDeliveryInterval = 30 * time.Second
	taskTimeout           = 60 * time.Minute
	taskPollInterval      = 10 * time.Second
	emailTimeout          = 30 * time.Minute
	emailPollInterval     = 10 * time.Second
)

type orderCartItemOpts struct {
	PlanCode    string `json:"planCode"`
	Duration    string `json:"duration"`
	PricingMode string `json:"pricingMode"`
	Quantity    int    `json:"quantity"`
}

type orderCartItem struct {
	CartId string `json:"cartId"`
	ItemId int64  `json:"itemId"`
}

type orderCartItemConfigOpts struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type orderCheckout struct {
	OrderID int64 `json:"orderId"`
}

type paymentMethodOpts struct {
	PaymentMethod struct {
		Id int64 `json:"id"`
	} `json:"paymentMethod"`
}

// Reinstall types.
type reinstallOpts struct {
	Os             string              `json:"operatingSystem"`
	Customizations *reinstallCustomize `json:"customizations,omitempty"`
}

type reinstallCustomize struct {
	Hostname               *string `json:"hostname,omitempty"`
	SshKey                 *string `json:"sshKey,omitempty"`
	PostInstallationScript *string `json:"postInstallationScript,omitempty"`
	ConfigDriveUserData    *string `json:"configDriveUserData,omitempty"`
	EfiBootloaderPath      *string `json:"efiBootloaderPath,omitempty"`
}

// Notification email types (used for termination confirmation).
type notificationEmail struct {
	Id      int64  `json:"id"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// confirmTerminationOpts is the request body for the confirmTermination endpoint.
type confirmTerminationOpts struct {
	Token string `json:"token"`
}

// Regex to extract the termination token from the confirmation email.
var reTerminateToken = regexp.MustCompile(`.*/billing/confirmTerminate\?id=[[:alnum:]]+&token=([[:alnum:]]+).*`)

// poll calls fn repeatedly at interval until it returns done=true, ctx is
// cancelled, or timeout elapses. This is the shared polling skeleton for
// all long-running OVH operations (order delivery, task completion, email).
func poll(ctx context.Context, timeout, interval time.Duration, fn func() (done bool, err error)) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %v", timeout)
		}

		done, err := fn()
		if err != nil {
			return err
		}
		if done {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// setupOVHClient creates a new OVH API client from plugin config.
func (t *TargetPlugin) setupOVHClient() (*ovh.Client, error) {
	return ovh.NewClient(
		getConfigValue(t.config, configKeyEndpoint, configValueEndpointDefault),
		getConfigValue(t.config, configKeyApplicationKey, ""),
		getConfigValue(t.config, configKeyApplicationSecret, ""),
		getConfigValue(t.config, configKeyConsumerKey, ""),
	)
}

// fetchSubsidiary auto-detects the OVH subsidiary from the account profile.
// This matches the Terraform OVH provider's behavior when subsidiary is not
// explicitly configured.
func (t *TargetPlugin) fetchSubsidiary(ctx context.Context) (string, error) {
	me := struct {
		OvhSubsidiary string `json:"ovhSubsidiary"`
	}{}
	if err := t.ovh.GetWithContext(ctx, "/me", &me); err != nil {
		return "", fmt.Errorf("fetching account profile: %v", err)
	}
	if me.OvhSubsidiary == "" {
		return "", fmt.Errorf("account profile has no ovhSubsidiary")
	}
	return me.OvhSubsidiary, nil
}

// listServiceNames returns all OVH dedicated server service names on the
// account. This is a single API call (GET /dedicated/server) returning a
// flat list of strings — no per-server detail calls needed.
//
// Used as the remoteIDs whitelist for RunPreScaleInTasksWithRemoteCheck:
// it ensures we only drain Nomad nodes whose OVH server still exists.
func (t *TargetPlugin) listServiceNames(ctx context.Context) ([]string, error) {
	var names []string
	if err := t.ovh.GetWithContext(ctx, "/dedicated/server", &names); err != nil {
		return nil, fmt.Errorf("failed to list dedicated servers: %v", err)
	}
	return names, nil
}

// orderServer places an order for a new OVH dedicated server. The flow
// follows the same cart-based pattern as the Terraform OVH provider:
//
//  1. Create cart + assign
//  2. Add item (planCode, datacenter configuration)
//  3. Checkout + pay with default payment method
//  4. Wait for delivery + find service name from order details
//  5. Set display name if configured (via /services/{id})
//  6. Reinstall OS with customizations (hostname, SSH key, user data)
func (t *TargetPlugin) orderServer(ctx context.Context, config map[string]string) error {
	// 1. Create cart.
	cart := &orderCart{}
	cartOpts := &orderCartOpts{
		OvhSubsidiary: strings.ToUpper(getConfigValue(t.config, configKeyOvhSubsidiary, "")),
	}
	if err := t.ovh.PostWithContext(ctx, "/order/cart", cartOpts, cart); err != nil {
		return fmt.Errorf("creating order cart: %v", err)
	}
	t.logger.Debug("created order cart", "cart_id", cart.CartId)

	// Assign cart to the authenticated user.
	assignEndpoint := fmt.Sprintf("/order/cart/%s/assign", url.PathEscape(cart.CartId))
	if err := t.ovh.PostWithContext(ctx, assignEndpoint, nil, nil); err != nil {
		return fmt.Errorf("assigning order cart: %v", err)
	}

	// 2. Add item to cart. The product type (e.g. "eco", "baremetalServers")
	// determines the cart endpoint, matching the Terraform provider's range attribute.
	planCode := getConfigValue(config, configKeyPlanCode, "")
	productType := getConfigValue(config, configKeyProductType, configValueProductTypeDefault)

	item := &orderCartItem{}
	itemOpts := &orderCartItemOpts{
		PlanCode:    planCode,
		Duration:    orderDuration,
		PricingMode: orderPricingMode,
		Quantity:    1,
	}
	addEndpoint := fmt.Sprintf("/order/cart/%s/%s", url.PathEscape(cart.CartId), url.PathEscape(productType))
	if err := t.ovh.PostWithContext(ctx, addEndpoint, itemOpts, item); err != nil {
		return fmt.Errorf("adding %s item to cart: %v", productType, err)
	}
	t.logger.Debug("added item to cart", "item_id", item.ItemId)

	// Configure datacenter.
	datacenter := getConfigValue(config, configKeyDatacenter, "")
	configEndpoint := fmt.Sprintf("/order/cart/%s/item/%d/configuration",
		url.PathEscape(cart.CartId), item.ItemId)
	configOpts := &orderCartItemConfigOpts{
		Label: "dedicated_datacenter",
		Value: datacenter,
	}
	if err := t.ovh.PostWithContext(ctx, configEndpoint, configOpts, nil); err != nil {
		return fmt.Errorf("configuring datacenter: %v", err)
	}

	// 3. Checkout.
	checkout := &orderCheckout{}
	checkoutEndpoint := fmt.Sprintf("/order/cart/%s/checkout", url.PathEscape(cart.CartId))
	if err := t.ovh.PostWithContext(ctx, checkoutEndpoint, nil, checkout); err != nil {
		return fmt.Errorf("checking out cart: %v", err)
	}
	t.logger.Info("order created", "order_id", checkout.OrderID)

	// Pay with default payment method.
	var paymentIds []int64
	if err := t.ovh.GetWithContext(ctx, "/me/payment/method?default=true", &paymentIds); err != nil {
		return fmt.Errorf("getting default payment method: %v", err)
	}
	if len(paymentIds) == 0 {
		return fmt.Errorf("no default payment method found")
	}

	payEndpoint := fmt.Sprintf("/me/order/%d/pay", checkout.OrderID)
	payOpts := &paymentMethodOpts{}
	payOpts.PaymentMethod.Id = paymentIds[0]
	if err := t.ovh.PostWithContext(ctx, payEndpoint, payOpts, nil); err != nil {
		return fmt.Errorf("paying order %d: %v", checkout.OrderID, err)
	}
	t.logger.Info("order paid", "order_id", checkout.OrderID, "payment_method", paymentIds[0])

	// 4. Wait for delivery by polling order status.
	serviceName, err := t.waitForOrderDelivery(ctx, checkout.OrderID, planCode)
	if err != nil {
		return fmt.Errorf("waiting for order %d delivery: %v", checkout.OrderID, err)
	}
	t.logger.Info("server delivered", "service_name", serviceName)

	// 5. Set display name if configured (OVH IAM: serviceInfos → /services/{id}).
	if displayName := getConfigValue(config, configKeyDisplayName, ""); displayName != "" {
		var serviceInfos struct {
			ServiceID int64 `json:"serviceId"`
		}
		infoEndpoint := fmt.Sprintf("/dedicated/server/%s/serviceInfos", url.PathEscape(serviceName))
		if err := t.ovh.GetWithContext(ctx, infoEndpoint, &serviceInfos); err != nil {
			t.logger.Warn("failed to get serviceInfos for display name", "service_name", serviceName, "error", err)
		} else {
			body := struct {
				DisplayName string `json:"displayName"`
			}{DisplayName: displayName}
			svcEndpoint := fmt.Sprintf("/services/%d", serviceInfos.ServiceID)
			if err := t.ovh.PutWithContext(ctx, svcEndpoint, &body, nil); err != nil {
				t.logger.Warn("failed to set display name", "service_name", serviceName, "error", err)
			}
		}
	}

	// 6. Reinstall OS.
	if err := t.reinstallServer(ctx, serviceName, config); err != nil {
		return fmt.Errorf("reinstalling server %s: %v", serviceName, err)
	}

	return nil
}

// waitForOrderDelivery polls the order status until "delivered", then
// extracts the service name from the order details.
func (t *TargetPlugin) waitForOrderDelivery(ctx context.Context, orderID int64, planCode string) (string, error) {
	statusEndpoint := fmt.Sprintf("/me/order/%d/status", orderID)

	var serviceName string
	err := poll(ctx, orderDeliveryTimeout, orderDeliveryInterval, func() (bool, error) {
		var status string
		if err := t.ovh.GetWithContext(ctx, statusEndpoint, &status); err != nil {
			t.logger.Warn("error polling order status, retrying",
				"order_id", orderID, "error", err)
			return false, nil
		}

		t.logger.Debug("order status", "order_id", orderID, "status", status)
		if status != "delivered" {
			return false, nil
		}

		name, err := t.serviceNameFromOrder(ctx, orderID, planCode)
		if err != nil {
			return false, err
		}
		serviceName = name
		return true, nil
	})
	return serviceName, err
}

// serviceNameFromOrder extracts the service name from a delivered order's
// details. Follows the same pattern as the Terraform provider:
// - EU/CA endpoints: use the extension route (/details/{id}/extension)
// - US endpoint: use the operations route (/details/{id}/operations)
func (t *TargetPlugin) serviceNameFromOrder(ctx context.Context, orderID int64, planCode string) (string, error) {
	var detailIds []int64
	detailsEndpoint := fmt.Sprintf("/me/order/%d/details", orderID)
	if err := t.ovh.GetWithContext(ctx, detailsEndpoint, &detailIds); err != nil {
		return "", fmt.Errorf("getting order details: %v", err)
	}

	isUS := getConfigValue(t.config, configKeyEndpoint, configValueEndpointDefault) == "ovh-us"

	for _, detailId := range detailIds {
		// Try to match plan code to find the right detail.
		if !isUS {
			// EU/CA: extension route.
			ext := struct {
				Order struct {
					Plan struct {
						Code string `json:"code"`
					} `json:"plan"`
				} `json:"order"`
			}{}

			extEndpoint := fmt.Sprintf("/me/order/%d/details/%d/extension", orderID, detailId)
			if err := t.ovh.GetWithContext(ctx, extEndpoint, &ext); err != nil {
				continue
			}

			if ext.Order.Plan.Code != planCode {
				continue
			}
		} else {
			// US: operations route.
			var ops []struct {
				Resource struct {
					Name string `json:"name"`
				} `json:"resource"`
			}

			opsEndpoint := fmt.Sprintf("/me/order/%d/details/%d/operations", orderID, detailId)
			if err := t.ovh.GetWithContext(ctx, opsEndpoint, &ops); err != nil {
				continue
			}

			for _, op := range ops {
				if op.Resource.Name != "" {
					return op.Resource.Name, nil
				}
			}
			continue
		}

		// EU/CA: get domain from detail.
		detail := struct {
			Domain string `json:"domain"`
		}{}
		detailEndpoint := fmt.Sprintf("/me/order/%d/details/%d", orderID, detailId)
		if err := t.ovh.GetWithContext(ctx, detailEndpoint, &detail); err != nil {
			return "", fmt.Errorf("getting order detail %d: %v", detailId, err)
		}

		if detail.Domain != "" {
			return detail.Domain, nil
		}
	}

	return "", fmt.Errorf("service name not found in order %d details", orderID)
}

// reinstallServer triggers OS reinstallation and waits for completion.
func (t *TargetPlugin) reinstallServer(ctx context.Context, serviceName string, config map[string]string) error {
	os_ := getConfigValue(config, configKeyOperatingSystem, configValueOperatingSystemDefault)
	opts := &reinstallOpts{
		Os: os_,
	}

	// Build customizations only if any are configured.
	var c reinstallCustomize
	if hostname := getConfigValue(config, configKeyHostname, ""); hostname != "" {
		c.Hostname = &hostname
	}
	if sshKey := getConfigValue(config, configKeySSHKey, ""); sshKey != "" {
		c.SshKey = &sshKey
	}
	if script := getConfigValue(config, configKeyPostInstallationScript, ""); script != "" {
		c.PostInstallationScript = &script
	}
	if efi := getConfigValue(config, configKeyEfiBootloaderPath, ""); efi != "" {
		c.EfiBootloaderPath = &efi
	}

	// ConfigDrive userdata: inline value takes precedence over file path.
	if userData := getConfigValue(config, configKeyConfigDriveUserData, ""); userData != "" {
		c.ConfigDriveUserData = &userData
	} else if userDataFile := getConfigValue(config, configKeyConfigDriveUserDataFile, ""); userDataFile != "" {
		data, err := os.ReadFile(userDataFile)
		if err != nil {
			return fmt.Errorf("reading config drive user data file %s: %v", userDataFile, err)
		}
		s := string(data)
		c.ConfigDriveUserData = &s
	}
	if c != (reinstallCustomize{}) {
		opts.Customizations = &c
	}

	task := &ovhTask{}
	endpoint := fmt.Sprintf("/dedicated/server/%s/reinstall", url.PathEscape(serviceName))
	if err := t.ovh.PostWithContext(ctx, endpoint, opts, task); err != nil {
		// The OVH API can return an error even when the task was created.
		// If task.Id is non-zero, the task exists despite the error.
		// This matches the Terraform OVH provider's handling.
		if task.Id == 0 {
			return fmt.Errorf("calling POST %s: %v", endpoint, err)
		}
		t.logger.Warn("reinstall POST returned error but task was created",
			"task_id", task.Id, "error", err)
	}

	t.logger.Info("reinstall task created",
		"service_name", serviceName,
		"task_id", task.Id,
		"os", os_,
	)

	return t.waitForTask(ctx, serviceName, task.Id)
}

// waitForTask polls a dedicated server task until it reaches "done" status.
// Task statuses: init → todo → doing → done (or error/cancelled).
func (t *TargetPlugin) waitForTask(ctx context.Context, serviceName string, taskId int64) error {
	endpoint := fmt.Sprintf("/dedicated/server/%s/task/%d",
		url.PathEscape(serviceName), taskId)

	err := poll(ctx, taskTimeout, taskPollInterval, func() (bool, error) {
		task := &ovhTask{}
		if err := t.ovh.GetWithContext(ctx, endpoint, task); err != nil {
			// OVH API occasionally returns 404/500 for in-flight tasks.
			if errOvh, ok := err.(*ovh.APIError); ok && (errOvh.Code == 404 || errOvh.Code == 500) {
				t.logger.Debug("transient error polling task, retrying",
					"task_id", taskId, "error", err)
				return false, nil
			}
			return false, fmt.Errorf("polling: %v", err)
		}

		t.logger.Debug("task status", "task_id", taskId, "status", task.Status)

		switch task.Status {
		case "done":
			return true, nil
		case "error", "cancelled":
			return false, fmt.Errorf("ended with status %q: %s", task.Status, task.Comment)
		default:
			return false, nil // init, todo, doing — keep polling.
		}
	})
	if err != nil {
		return fmt.Errorf("task %d: %w", taskId, err)
	}
	return nil
}

// terminateServer requests termination of an OVH dedicated server and
// automatically confirms it via the notification email token. Follows the
// same pattern as the Terraform OVH provider's orderDelete.
func (t *TargetPlugin) terminateServer(ctx context.Context, serviceName string) error {
	// Record existing email notification IDs so we can find the new one.
	oldIds, err := t.notificationEmailIds(ctx)
	if err != nil {
		return fmt.Errorf("listing notification emails: %v", err)
	}

	// POST /dedicated/server/{serviceName}/terminate
	endpoint := fmt.Sprintf("/dedicated/server/%s/terminate", url.PathEscape(serviceName))
	if err := t.ovh.PostWithContext(ctx, endpoint, nil, nil); err != nil {
		if errOvh, ok := err.(*ovh.APIError); ok && (errOvh.Code == 404 || errOvh.Code == 460) {
			t.logger.Info("server already terminated or not found", "service_name", serviceName)
			return nil
		}
		return fmt.Errorf("calling POST %s: %v", endpoint, err)
	}

	t.logger.Info("termination requested, waiting for confirmation email",
		"service_name", serviceName)

	// Poll for the confirmation email containing the termination token.
	token, err := t.waitForTerminationToken(ctx, serviceName, oldIds)
	if err != nil {
		return fmt.Errorf("getting termination token for %s: %v", serviceName, err)
	}

	// Confirm termination with the token.
	confirmEndpoint := fmt.Sprintf("/dedicated/server/%s/confirmTermination",
		url.PathEscape(serviceName))
	confirmOpts := &confirmTerminationOpts{Token: token}
	if err := t.ovh.PostWithContext(ctx, confirmEndpoint, confirmOpts, nil); err != nil {
		return fmt.Errorf("confirming termination of %s: %v", serviceName, err)
	}

	t.logger.Info("termination confirmed", "service_name", serviceName)
	return nil
}

// waitForTerminationToken polls OVH notification emails for the termination
// confirmation link and extracts the token from it.
func (t *TargetPlugin) waitForTerminationToken(ctx context.Context, serviceName string, oldIds []int64) (string, error) {
	// The last old ID serves as the watermark — only check newer emails.
	var watermark int64
	if len(oldIds) > 0 {
		watermark = oldIds[len(oldIds)-1]
	}

	matches := []string{serviceName, "/billing/confirmTerminate"}

	var token string
	err := poll(ctx, emailTimeout, emailPollInterval, func() (bool, error) {
		email, err := t.findNewNotificationEmail(ctx, watermark, matches)
		if err != nil {
			t.logger.Debug("error checking notification emails, retrying", "error", err)
			return false, nil
		}
		if email == nil {
			return false, nil
		}

		tokenMatch := reTerminateToken.FindStringSubmatch(email.Body)
		if len(tokenMatch) != 2 {
			return false, fmt.Errorf("could not extract termination token from email %d", email.Id)
		}
		token = tokenMatch[1]
		return true, nil
	})
	return token, err
}

// notificationEmailIds returns sorted IDs of existing notification emails.
func (t *TargetPlugin) notificationEmailIds(ctx context.Context) ([]int64, error) {
	var ids []int64
	if err := t.ovh.GetWithContext(ctx, "/me/notification/email/history", &ids); err != nil {
		return nil, err
	}
	// IDs are typically sorted but ensure it for watermark comparison.
	slices.Sort(ids)
	return ids, nil
}

// findNewNotificationEmail looks for a notification email newer than
// watermark whose body contains all of the specified match strings.
func (t *TargetPlugin) findNewNotificationEmail(ctx context.Context, watermark int64, matches []string) (*notificationEmail, error) {
	ids, err := t.notificationEmailIds(ctx)
	if err != nil {
		return nil, err
	}

	for _, id := range ids {
		if id <= watermark {
			continue
		}

		email := &notificationEmail{}
		endpoint := fmt.Sprintf("/me/notification/email/history/%d", id)
		if err := t.ovh.GetWithContext(ctx, endpoint, email); err != nil {
			return nil, fmt.Errorf("getting notification email %d: %v", id, err)
		}

		allMatch := true
		for _, m := range matches {
			if !strings.Contains(email.Body, m) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return email, nil
		}
	}

	return nil, nil
}

// ovhNodeIDMap is the ClusterNodeIDLookupFunc callback that translates a
// Nomad node to its OVH service name. This follows the same pattern as
// awsNodeIDMap in the AWS ASG plugin.
//
// The OVH service name is stored as a Nomad node attribute during bootstrap.
// The attribute key is "unique.platform.ovh.service_name".
func ovhNodeIDMap(node *api.Node) (string, error) {
	const attrKey = "unique.platform.ovh.service_name"

	val, ok := node.Attributes[attrKey]
	if !ok || val == "" {
		return "", fmt.Errorf("node %s is missing attribute %q", node.ID, attrKey)
	}
	return val, nil
}
