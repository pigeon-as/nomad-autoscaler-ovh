// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"testing"

	"github.com/hashicorp/nomad/api"
)

func TestCalculateDirection(t *testing.T) {
	p := &TargetPlugin{}

	tests := []struct {
		name    string
		current int64
		desired int64
		wantNum int64
		wantDir string
	}{
		{"scale in", 5, 3, 2, "in"},
		{"scale out", 3, 5, 2, "out"},
		{"no change", 3, 3, 0, ""},
		{"scale in to zero", 2, 0, 2, "in"},
		{"scale out from zero", 0, 3, 3, "out"},
		{"scale in by one", 4, 3, 1, "in"},
		{"scale out by one", 3, 4, 1, "out"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			num, dir := p.calculateDirection(tt.current, tt.desired)
			if num != tt.wantNum {
				t.Errorf("num = %d, want %d", num, tt.wantNum)
			}
			if dir != tt.wantDir {
				t.Errorf("dir = %q, want %q", dir, tt.wantDir)
			}
		})
	}
}

func TestOvhNodeIDMap(t *testing.T) {
	tests := []struct {
		name    string
		node    *api.Node
		want    string
		wantErr bool
	}{
		{
			name: "valid attribute",
			node: &api.Node{
				Attributes: map[string]string{
					"unique.platform.ovh.service_name": "ns1234567.ip-1-2-3.eu",
				},
			},
			want: "ns1234567.ip-1-2-3.eu",
		},
		{
			name: "missing attribute",
			node: &api.Node{
				Attributes: map[string]string{},
			},
			wantErr: true,
		},
		{
			name: "empty attribute",
			node: &api.Node{
				Attributes: map[string]string{
					"unique.platform.ovh.service_name": "",
				},
			},
			wantErr: true,
		},
		{
			name: "nil attributes",
			node: &api.Node{
				Attributes: nil,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ovhNodeIDMap(tt.node)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTargetConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]string
		wantErr bool
		check   func(*testing.T, *targetConfig)
	}{
		{
			name: "empty config uses defaults",
			config: map[string]string{},
			check: func(t *testing.T, tc *targetConfig) {
				if tc.OSTemplate != configValueOSTemplateDefault {
					t.Errorf("OSTemplate = %q, want default %q", tc.OSTemplate, configValueOSTemplateDefault)
				}
				if tc.ProductType != configValueProductTypeDefault {
					t.Errorf("ProductType = %q, want default %q", tc.ProductType, configValueProductTypeDefault)
				}
			},
		},
		{
			name: "all fields",
			config: map[string]string{
				"ovh_datacenter":          "gra3",
				"ovh_plan_code":           "24sk10",
				"ovh_os_template":         "ubuntu2204_64",
				"ovh_ssh_key":             "ssh-ed25519 AAAA...",
				"ovh_post_install_script": "#!/bin/bash\necho hi",
				"ovh_user_data_file":      "/opt/pigeon/worker-userdata.sh",
				"ovh_product_type":        "baremetalServers",
			},
			check: func(t *testing.T, tc *targetConfig) {
				if tc.Datacenter != "gra3" {
					t.Errorf("Datacenter = %q", tc.Datacenter)
				}
				if tc.PlanCode != "24sk10" {
					t.Errorf("PlanCode = %q", tc.PlanCode)
				}
				if tc.OSTemplate != "ubuntu2204_64" {
					t.Errorf("OSTemplate = %q", tc.OSTemplate)
				}
				if tc.SSHKey != "ssh-ed25519 AAAA..." {
					t.Errorf("SSHKey = %q", tc.SSHKey)
				}
				if tc.PostInstallScript != "#!/bin/bash\necho hi" {
					t.Errorf("PostInstallScript = %q", tc.PostInstallScript)
				}
				if tc.UserDataFile != "/opt/pigeon/worker-userdata.sh" {
					t.Errorf("UserDataFile = %q", tc.UserDataFile)
				}
				if tc.ProductType != "baremetalServers" {
					t.Errorf("ProductType = %q", tc.ProductType)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc, err := parseTargetConfig(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, tc)
			}
		})
	}
}

func TestPluginConfig_parse(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]string
		wantErr bool
		check   func(*testing.T, *pluginConfig)
	}{
		{
			name:    "missing required keys",
			config:  map[string]string{},
			wantErr: true,
		},
		{
			name: "partial keys",
			config: map[string]string{
				"ovh_application_key": "ak",
			},
			wantErr: true,
		},
		{
			name: "all required keys",
			config: map[string]string{
				"ovh_application_key":    "ak",
				"ovh_application_secret": "as",
				"ovh_consumer_key":       "ck",
			},
			check: func(t *testing.T, pc *pluginConfig) {
				if pc.Endpoint != configValueEndpointDefault {
					t.Errorf("Endpoint = %q, want default %q", pc.Endpoint, configValueEndpointDefault)
				}
				if pc.OvhSubsidiary != "" {
					t.Errorf("OvhSubsidiary = %q, want empty (auto-detect)", pc.OvhSubsidiary)
				}
			},
		},
		{
			name: "custom endpoint and subsidiary",
			config: map[string]string{
				"ovh_application_key":    "ak",
				"ovh_application_secret": "as",
				"ovh_consumer_key":       "ck",
				"ovh_endpoint":           "ovh-ca",
				"ovh_subsidiary":         "CA",
			},
			check: func(t *testing.T, pc *pluginConfig) {
				if pc.Endpoint != "ovh-ca" {
					t.Errorf("Endpoint = %q", pc.Endpoint)
				}
				if pc.OvhSubsidiary != "CA" {
					t.Errorf("OvhSubsidiary = %q", pc.OvhSubsidiary)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := &pluginConfig{}
			err := pc.parse(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, pc)
			}
		})
	}
}
