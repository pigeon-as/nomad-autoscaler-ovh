// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

//go:build e2e

// Run: make e2e (requires a running nomad dev agent: make dev)

package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/sdk"
	"github.com/pigeon-as/nomad-autoscaler-ovh/plugin"
	"github.com/shoenig/test/must"
)

const groupID = "e2e-autoscaler"

var requiredEnv = []string{
	"OVH_APPLICATION_KEY",
	"OVH_APPLICATION_SECRET",
	"OVH_CONSUMER_KEY",
	"E2E_PLAN_CODE",
	"E2E_REVERSE_DOMAIN",
}

func TestMain(m *testing.M) {
	for _, key := range requiredEnv {
		if os.Getenv(key) == "" {
			fmt.Fprintf(os.Stderr, "required env var %s not set\n", key)
			os.Exit(1)
		}
	}

	// Clean up any servers left over from a previous run.
	cleanupGroup()

	code := m.Run()

	cleanupGroup()
	os.Exit(code)
}

// cleanupGroup terminates all OVH servers in the e2e group. Best-effort;
// errors are logged to stderr so setup/teardown doesn't mask test failures.
func cleanupGroup() {
	p, err := newPlugin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cleanup: plugin init failed: %v\n", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	names, err := p.ListGroupServers(ctx, groupID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cleanup: list servers failed: %v\n", err)
		return
	}

	for _, name := range names {
		fmt.Fprintf(os.Stderr, "cleanup: terminating %s\n", name)
		if err := p.TerminateServer(ctx, name); err != nil {
			fmt.Fprintf(os.Stderr, "cleanup: failed to terminate %s: %v\n", name, err)
		}
	}
}

// --- helpers ---

func newPlugin() (*plugin.TargetPlugin, error) {
	p := plugin.NewOVHDedicatedPlugin(hclog.New(&hclog.LoggerOptions{
		Name:  "ovh-e2e",
		Level: hclog.Debug,
	}))
	err := p.SetConfig(map[string]string{
		"ovh_application_key":    os.Getenv("OVH_APPLICATION_KEY"),
		"ovh_application_secret": os.Getenv("OVH_APPLICATION_SECRET"),
		"ovh_consumer_key":       os.Getenv("OVH_CONSUMER_KEY"),
		"ovh_endpoint":           envOrDefault("OVH_ENDPOINT", "ovh-eu"),
		"ovh_subsidiary":         envOrDefault("OVH_SUBSIDIARY", "FR"),
		"ovh_reverse_domain":     os.Getenv("E2E_REVERSE_DOMAIN"),
	})
	return p, err
}

func setupPlugin(t *testing.T) *plugin.TargetPlugin {
	t.Helper()
	p, err := newPlugin()
	must.NoError(t, err)
	return p
}

func policyConfig() map[string]string {
	return map[string]string{
		"ovh_group_id":    groupID,
		"ovh_datacenter":  envOrDefault("E2E_DATACENTER", "gra3"),
		"ovh_plan_code":   os.Getenv("E2E_PLAN_CODE"),
		"ovh_os_template": envOrDefault("E2E_OS_TEMPLATE", "debian12_64"),
		"datacenter":      "dc1",
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// --- tests ---

// TestStatus verifies the plugin connects to Nomad and OVH and returns
// a valid status for an empty scaling group.
func TestStatus(t *testing.T) {
	p := setupPlugin(t)
	cfg := policyConfig()

	status, err := p.Status(cfg)
	must.NoError(t, err)
	must.True(t, status.Ready)
	must.Eq(t, int64(0), status.Count)
}

// TestScaleLifecycle orders the cheapest Eco server, verifies delivery
// and reverse DNS, then terminates it. This test incurs real OVH costs
// and takes 20-60 minutes.
func TestScaleLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running scale lifecycle test")
	}

	p := setupPlugin(t)
	cfg := policyConfig()
	ctx := context.Background()

	// Safety-net cleanup: terminate any remaining servers in the e2e group
	// even if the test panics or is killed.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		defer cancel()

		names, _ := p.ListGroupServers(cleanupCtx, groupID)
		for _, name := range names {
			t.Logf("cleanup: terminating %s", name)
			p.TerminateServer(cleanupCtx, name) //nolint:errcheck
		}
	})

	// 1. Scale out: order 1 server.
	t.Log("ordering server (this takes 10-40 minutes)...")
	err := p.Scale(sdk.ScalingAction{Count: 1}, cfg)
	must.NoError(t, err)

	// 2. Verify server appeared in the group via reverse DNS.
	names, err := p.ListGroupServers(ctx, groupID)
	must.NoError(t, err)
	must.Eq(t, 1, len(names))
	t.Logf("server delivered: %s", names[0])

	// 3. Status should reflect the new server.
	status, err := p.Status(cfg)
	must.NoError(t, err)
	must.Eq(t, int64(1), status.Count)

	// 4. Terminate the server.
	t.Log("terminating server (this takes 1-10 minutes)...")
	err = p.TerminateServer(ctx, names[0])
	must.NoError(t, err)
	t.Logf("server terminated: %s", names[0])
}
