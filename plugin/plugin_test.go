// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"testing"

	"github.com/hashicorp/nomad/api"
	"github.com/shoenig/test/must"
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
			must.EqOp(t, tt.wantNum, num)
			must.EqOp(t, tt.wantDir, dir)
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
				must.Error(t, err)
				return
			}
			must.NoError(t, err)
			must.EqOp(t, tt.want, got)
		})
	}
}

func TestGetConfigValue(t *testing.T) {
	tests := []struct {
		name     string
		config   map[string]string
		key      string
		default_ string
		want     string
	}{
		{"present key", map[string]string{"k": "v"}, "k", "d", "v"},
		{"missing key uses default", map[string]string{}, "k", "d", "d"},
		{"empty value returned as-is", map[string]string{"k": ""}, "k", "d", ""},
		{"os default", map[string]string{}, configKeyOperatingSystem, configValueOperatingSystemDefault, configValueOperatingSystemDefault},
		{"product_type default", map[string]string{}, configKeyProductType, configValueProductTypeDefault, configValueProductTypeDefault},
		{"endpoint default", map[string]string{}, configKeyEndpoint, configValueEndpointDefault, configValueEndpointDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getConfigValue(tt.config, tt.key, tt.default_)
			must.EqOp(t, tt.want, got)
		})
	}
}

func TestValidatePluginConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]string
		wantErr bool
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
		},
		{
			name: "all keys with extras",
			config: map[string]string{
				"ovh_application_key":    "ak",
				"ovh_application_secret": "as",
				"ovh_consumer_key":       "ck",
				"ovh_endpoint":           "ovh-ca",
				"ovh_subsidiary":         "CA",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePluginConfig(tt.config)
			if tt.wantErr {
				must.Error(t, err)
				return
			}
			must.NoError(t, err)
		})
	}
}
