// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"fmt"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/plugins/base"
	"github.com/hashicorp/nomad-autoscaler/plugins/target"
	"github.com/hashicorp/nomad-autoscaler/sdk"
	"github.com/hashicorp/nomad-autoscaler/sdk/helper/nomad"
	"github.com/hashicorp/nomad-autoscaler/sdk/helper/scaleutils"
	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/ovh/go-ovh/ovh"
)

const (
	pluginName = "ovh-dedicated"
)

var (
	pluginInfo = &base.PluginInfo{
		Name:       pluginName,
		PluginType: sdk.PluginTypeTarget,
	}
)

// Assert that TargetPlugin meets the target.Target interface.
var _ target.Target = (*TargetPlugin)(nil)

// TargetPlugin is the OVH Dedicated Server implementation of the
// target.Target interface.
type TargetPlugin struct {
	config pluginConfig
	logger hclog.Logger
	ovh    *ovh.Client
	nomad  *nomadapi.Client

	// clusterUtils provides general cluster scaling utilities for querying
	// the state of node pools and performing scaling tasks.
	clusterUtils *scaleutils.ClusterScaleUtils
}

// NewOVHDedicatedPlugin returns the OVH Dedicated Server implementation of
// the target.Target interface.
func NewOVHDedicatedPlugin(log hclog.Logger) *TargetPlugin {
	return &TargetPlugin{
		logger: log,
	}
}

// SetConfig satisfies the SetConfig function on the base.Base interface.
func (t *TargetPlugin) SetConfig(config map[string]string) error {
	t.config = pluginConfig{}

	if err := t.config.parse(config); err != nil {
		return fmt.Errorf("failed to parse OVH plugin config: %v", err)
	}

	client, err := t.setupOVHClient()
	if err != nil {
		return fmt.Errorf("failed to setup OVH client: %v", err)
	}
	t.ovh = client

	// Auto-detect subsidiary from account profile if not configured,
	// matching the Terraform OVH provider pattern.
	if t.config.OvhSubsidiary == "" {
		sub, err := t.fetchSubsidiary()
		if err != nil {
			return fmt.Errorf("failed to auto-detect OVH subsidiary: %v", err)
		}
		t.config.OvhSubsidiary = sub
		t.logger.Info("auto-detected OVH subsidiary", "subsidiary", sub)
	}

	nomadConfig := nomad.ConfigFromNamespacedMap(config)
	nomadClient, err := nomadapi.NewClient(nomadConfig)
	if err != nil {
		return fmt.Errorf("failed to setup Nomad client: %v", err)
	}
	t.nomad = nomadClient

	clusterUtils, err := scaleutils.NewClusterScaleUtils(nomadConfig, t.logger)
	if err != nil {
		return err
	}

	// Store and set the remote ID callback function. OVH dedicated servers
	// are identified by their service name (e.g. "ns1234567.ip-1-2-3.eu").
	t.clusterUtils = clusterUtils
	t.clusterUtils.ClusterNodeIDLookupFunc = ovhNodeIDMap

	return nil
}

// PluginInfo satisfies the PluginInfo function on the base.Base interface.
func (t *TargetPlugin) PluginInfo() (*base.PluginInfo, error) {
	return pluginInfo, nil
}

// Scale satisfies the Scale function on the target.Target interface.
func (t *TargetPlugin) Scale(action sdk.ScalingAction, config map[string]string) error {
	// OVH can't support dry-run like Nomad, so just exit.
	if action.Count == sdk.StrategyActionMetaValueDryRunCount {
		return nil
	}

	ctx := context.Background()

	targetCfg, err := parseTargetConfig(config)
	if err != nil {
		return fmt.Errorf("failed to parse OVH target config: %v", err)
	}

	count, err := t.countPoolNodes(config)
	if err != nil {
		return fmt.Errorf("failed to count pool nodes: %v", err)
	}

	num, direction := t.calculateDirection(count, action.Count)

	switch direction {
	case "in":
		err = t.scaleIn(ctx, num, config)
	case "out":
		err = t.scaleOut(ctx, num, targetCfg)
	default:
		t.logger.Info("scaling not required",
			"current_count", count,
			"strategy_count", action.Count,
		)
		return nil
	}

	if err != nil {
		err = fmt.Errorf("failed to perform scaling action: %v", err)
	}
	return err
}

// Status satisfies the Status function on the target.Target interface.
func (t *TargetPlugin) Status(config map[string]string) (*sdk.TargetStatus, error) {
	// Perform our check of the Nomad node pool. If the pool is not ready, we
	// can exit here and avoid further checks as it won't affect the outcome.
	ready, err := t.clusterUtils.IsPoolReady(config)
	if err != nil {
		return nil, fmt.Errorf("failed to run Nomad node readiness check: %v", err)
	}
	if !ready {
		return &sdk.TargetStatus{Ready: ready}, nil
	}

	count, err := t.countPoolNodes(config)
	if err != nil {
		return nil, fmt.Errorf("failed to count pool nodes: %v", err)
	}

	resp := sdk.TargetStatus{
		Ready: true,
		Count: count,
		Meta:  make(map[string]string),
	}

	return &resp, nil
}

// countPoolNodes counts Nomad nodes that match the scaling policy's pool
// filter (datacenter, node_class, node_pool). Only "ready" nodes are counted.
//
// OVH has no provider-side scaling group (unlike AWS ASG or Azure VMSS), so
// the Nomad node pool IS the scaling group. This is the OVH equivalent of
// AWS's DescribeAutoScalingGroups or Azure's vmss.Get.
func (t *TargetPlugin) countPoolNodes(config map[string]string) (int64, error) {
	nodes, _, err := t.nomad.Nodes().List(nil)
	if err != nil {
		return 0, fmt.Errorf("listing Nomad nodes: %v", err)
	}

	targetDC := config[sdk.TargetConfigKeyDatacenter]
	targetClass := config[sdk.TargetConfigKeyClass]
	targetPool := config[sdk.TargetConfigKeyNodePool]

	var count int64
	for _, node := range nodes {
		if node.Status != "ready" {
			continue
		}
		if targetDC != "" && node.Datacenter != targetDC {
			continue
		}
		if targetClass != "" && node.NodeClass != targetClass {
			continue
		}
		if targetPool != "" && node.NodePool != targetPool {
			continue
		}
		count++
	}
	return count, nil
}

// calculateDirection determines the scaling direction and the number of
// instances to add or remove.
//
// NOTE: Unlike the AWS ASG and Azure VMSS plugins which return
// strategyDesired (total) for scale-out (because they set a desired
// capacity on the ASG/VMSS), this plugin returns the delta
// (strategyDesired - current). This is because OVH has no "desired
// capacity" API — we order individual servers, so scaleOut needs the
// count of new servers to order.
func (t *TargetPlugin) calculateDirection(current, strategyDesired int64) (int64, string) {
	if strategyDesired < current {
		return current - strategyDesired, "in"
	}
	if strategyDesired > current {
		return strategyDesired - current, "out"
	}
	return 0, ""
}
