// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import "fmt"

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
	configKeyDatacenter        = "ovh_datacenter"
	configKeyPlanCode          = "ovh_plan_code"
	configKeyOSTemplate        = "ovh_os_template"
	configKeySSHKey            = "ovh_ssh_key"
	configKeyPostInstallScript = "ovh_post_install_script"
	configKeyUserDataFile      = "ovh_user_data_file"
	configKeyProductType       = "ovh_product_type"
)

// Default values.
const (
	configValueEndpointDefault    = "ovh-eu"
	configValueOSTemplateDefault  = "debian12_64"
	configValueProductTypeDefault = "eco"
)

// validatePluginConfig checks that required agent-level OVH credentials
// are present in the config map.
func validatePluginConfig(config map[string]string) error {
	ak := getConfigValue(config, configKeyApplicationKey, "")
	as := getConfigValue(config, configKeyApplicationSecret, "")
	ck := getConfigValue(config, configKeyConsumerKey, "")
	if ak == "" || as == "" || ck == "" {
		return fmt.Errorf("ovh_application_key, ovh_application_secret, and ovh_consumer_key are required")
	}
	return nil
}

func getConfigValue(config map[string]string, key, defaultValue string) string {
	value, ok := config[key]
	if !ok {
		return defaultValue
	}
	return value
}
