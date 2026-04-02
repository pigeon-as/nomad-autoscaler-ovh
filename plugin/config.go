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

// Per-policy config keys. Reinstall-related keys align with the Terraform OVH
// provider's snake_case attribute names for the reinstall customizations block.
// ovh_display_name is separate — it updates the service name via /services/{id}.
const (
	configKeyDatacenter              = "ovh_datacenter"
	configKeyPlanCode                = "ovh_plan_code"
	configKeyOperatingSystem         = "ovh_operating_system"
	configKeySSHKey                  = "ovh_ssh_key"
	configKeyHostname                = "ovh_hostname"
	configKeyDisplayName             = "ovh_display_name"
	configKeyPostInstallationScript  = "ovh_post_installation_script"
	configKeyConfigDriveUserData     = "ovh_config_drive_user_data"
	configKeyConfigDriveUserDataFile = "ovh_config_drive_user_data_file"
	configKeyProductType             = "ovh_product_type"
	configKeyEfiBootloaderPath       = "ovh_efi_bootloader_path"
)

// Default values.
const (
	configValueEndpointDefault        = "ovh-eu"
	configValueOperatingSystemDefault = "debian12_64"
	configValueProductTypeDefault     = "eco"
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
