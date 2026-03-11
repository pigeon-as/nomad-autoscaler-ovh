// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import "fmt"

// pluginConfig holds the agent-level configuration for the OVH target plugin.
// These are set once via the autoscaler agent config and are used for all
// scaling operations.
type pluginConfig struct {
	// Endpoint is the OVH API endpoint (e.g. "ovh-eu", "ovh-ca", "ovh-us").
	Endpoint string

	// ApplicationKey is the OVH API application key.
	ApplicationKey string

	// ApplicationSecret is the OVH API application secret.
	ApplicationSecret string

	// ConsumerKey is the OVH API consumer key.
	ConsumerKey string

	// OvhSubsidiary is the OVH subsidiary for ordering (e.g. "FR", "DE",
	// "GB"). If empty, auto-detected from GET /me (matching the Terraform
	// OVH provider pattern).
	OvhSubsidiary string
}

// targetConfig holds per-policy configuration for the OVH target plugin.
// These are set in the scaling policy and may vary between policies.
type targetConfig struct {
	// Datacenter is the OVH datacenter where new servers should be
	// provisioned (e.g. "gra3", "bhs8", "sgp1").
	Datacenter string

	// PlanCode is the OVH plan code (SKU) for new servers
	// (e.g. "24adv-1", "24sk10").
	PlanCode string

	// OSTemplate is the OS template to install on new servers
	// (e.g. "debian12_64").
	OSTemplate string

	// SSHKey is the SSH public key content to inject during server
	// installation. The OVH reinstall API expects the actual key content,
	// not a key name.
	SSHKey string

	// UserData is the base64-encoded post-installation script to run on new
	// servers during OS installation. Used to bootstrap the server into the
	// WireGuard mesh and Nomad cluster. Passed to the OVH API as
	// customizations.postInstallationScript.
	UserData string

	// ProductType is the OVH order cart product type (e.g. "eco",
	// "baremetalServers"). Determines which cart endpoint is used for
	// ordering. Matches the Terraform provider's "range" attribute.
	ProductType string
}

// Agent-level config keys.
const (
	configKeyEndpoint          = "ovh_endpoint"
	configKeyApplicationKey    = "ovh_application_key"
	configKeyApplicationSecret = "ovh_application_secret"
	configKeyConsumerKey       = "ovh_consumer_key"
	configKeyOvhSubsidiary     = "ovh_subsidiary"
)

// Per-policy config keys.
const (
	configKeyDatacenter  = "ovh_datacenter"
	configKeyPlanCode    = "ovh_plan_code"
	configKeyOSTemplate  = "ovh_os_template"
	configKeySSHKey      = "ovh_ssh_key"
	configKeyUserData    = "ovh_user_data"
	configKeyProductType = "ovh_product_type"
)

// Default values.
const (
	configValueEndpointDefault    = "ovh-eu"
	configValueOSTemplateDefault  = "debian12_64"
	configValueProductTypeDefault = "eco"
)

func (c *pluginConfig) parse(config map[string]string) error {
	c.Endpoint = getConfigValue(config, configKeyEndpoint, configValueEndpointDefault)
	c.ApplicationKey = getConfigValue(config, configKeyApplicationKey, "")
	c.ApplicationSecret = getConfigValue(config, configKeyApplicationSecret, "")
	c.ConsumerKey = getConfigValue(config, configKeyConsumerKey, "")
	c.OvhSubsidiary = getConfigValue(config, configKeyOvhSubsidiary, "")

	if c.ApplicationKey == "" || c.ApplicationSecret == "" || c.ConsumerKey == "" {
		return fmt.Errorf("ovh_application_key, ovh_application_secret, and ovh_consumer_key are required")
	}

	return nil
}

func parseTargetConfig(config map[string]string) (*targetConfig, error) {
	tc := &targetConfig{
		Datacenter:  getConfigValue(config, configKeyDatacenter, ""),
		PlanCode:    getConfigValue(config, configKeyPlanCode, ""),
		OSTemplate:  getConfigValue(config, configKeyOSTemplate, configValueOSTemplateDefault),
		SSHKey:      getConfigValue(config, configKeySSHKey, ""),
		UserData:    getConfigValue(config, configKeyUserData, ""),
		ProductType: getConfigValue(config, configKeyProductType, configValueProductTypeDefault),
	}

	return tc, nil
}

func getConfigValue(config map[string]string, key, defaultValue string) string {
	value, ok := config[key]
	if !ok {
		return defaultValue
	}
	return value
}
