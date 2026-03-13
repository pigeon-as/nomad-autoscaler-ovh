// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"fmt"

	"github.com/hashicorp/nomad-autoscaler/sdk/helper/scaleutils"
)

// scaleIn removes num servers from the pool. It follows the AWS ASG plugin
// pattern: pre-scale tasks → terminate (tracking per-instance success/failure)
// → post-scale tasks on successes, failure recovery on failures.
//
// OVH has no provider-side scaling group, so the Nomad node pool is the source
// of truth. The flat list of all OVH service names (GET /dedicated/server) is
// used as the remoteIDs whitelist — the SDK intersects this with Nomad nodes
// matched by pool filter and ClusterNodeIDLookupFunc (ovhNodeIDMap).
func (t *TargetPlugin) scaleIn(ctx context.Context, num int64, config map[string]string) error {
	// GET /dedicated/server — flat list of all service names on the account.
	// Used as remoteIDs whitelist so RunPreScaleInTasksWithRemoteCheck only
	// considers Nomad nodes whose OVH server still exists.
	remoteIDs, err := t.listServiceNames(ctx)
	if err != nil {
		return fmt.Errorf("failed to list OVH service names: %v", err)
	}

	// RunPreScaleInTasksWithRemoteCheck performs:
	// 1. Identify Nomad nodes that match the pool (datacenter/node_class/node_pool)
	// 2. Filter to only nodes whose remote ID is in our server list
	// 3. Select nodes for removal using the configured node_selector_strategy
	// 4. Drain the selected nodes
	ids, err := t.clusterUtils.RunPreScaleInTasksWithRemoteCheck(ctx, config, remoteIDs, int(num))
	if err != nil {
		return fmt.Errorf("failed to perform pre scale-in tasks: %v", err)
	}

	// Terminate each identified server, continuing on failure so we can
	// recover nodes that were drained but not terminated. This follows
	// the AWS ASG plugin's partial-failure pattern.
	var successes, failures []scaleutils.NodeResourceID

	for _, node := range ids {
		t.logger.Info("terminating OVH dedicated server",
			"node_id", node.NomadNodeID,
			"service_name", node.RemoteResourceID,
		)
		if err := t.terminateServer(ctx, node.RemoteResourceID); err != nil {
			t.logger.Error("failed to terminate server",
				"service_name", node.RemoteResourceID, "error", err)
			failures = append(failures, node)
			continue
		}
		successes = append(successes, node)
	}

	// Toggle eligibility back on nodes we failed to terminate so they
	// return to service. Matches AWS ASG's RunPostScaleInTasksOnFailure.
	var failedTaskErr error
	if len(failures) > 0 {
		failedTaskErr = t.clusterUtils.RunPostScaleInTasksOnFailure(failures)
	}

	// Run post-scale tasks (purge) on successfully terminated nodes.
	if len(successes) > 0 {
		if err := t.clusterUtils.RunPostScaleInTasks(ctx, config, successes); err != nil {
			t.logger.Error("failed to perform post scale-in tasks", "error", err)
		}
	}

	if len(failures) > 0 {
		t.logger.Warn("partial scale-in",
			"success_num", len(successes), "failed_num", len(failures))
		if failedTaskErr != nil {
			return failedTaskErr
		}
		return fmt.Errorf("partial scale-in: %d of %d nodes failed to terminate",
			len(failures), len(successes)+len(failures))
	}
	return nil
}

// scaleOut orders num new OVH dedicated servers.
//
// OVH dedicated server delivery takes 2–10 minutes. The autoscaler policy
// MUST use an adequate cooldown (e.g. cooldown = "15m") to prevent
// re-triggering while servers are being delivered and joining the cluster.
// Unlike AWS ASG/Azure VMSS/GCE MIG, OVH has no provider-side "desired count"
// that updates instantly, so the policy cooldown is the only mechanism
// preventing double-ordering.
func (t *TargetPlugin) scaleOut(ctx context.Context, num int64, config map[string]string) error {
	planCode := getConfigValue(config, configKeyPlanCode, "")
	datacenter := getConfigValue(config, configKeyDatacenter, "")

	for i := int64(0); i < num; i++ {
		t.logger.Info("ordering new OVH dedicated server",
			"plan_code", planCode,
			"datacenter", datacenter,
			"count", fmt.Sprintf("%d/%d", i+1, num),
		)

		if err := t.orderServer(ctx, config); err != nil {
			return fmt.Errorf("failed to order OVH server: %v", err)
		}
	}

	return nil
}
