// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/plugins"
	"github.com/pigeon-as/nomad-autoscaler-ovh/plugin"
)

func main() {
	plugins.Serve(factory)
}

func factory(log hclog.Logger) interface{} {
	return plugin.NewOVHDedicatedPlugin(log)
}
