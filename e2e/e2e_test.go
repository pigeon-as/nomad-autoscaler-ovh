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

var requiredEnv = []string{
	"OVH_APPLICATION_KEY",
	"OVH_APPLICATION_SECRET",
	"OVH_CONSUMER_KEY",
	"E2E_PLAN_CODE",
}

func TestMain(m *testing.M) {
	for _, key := range requiredEnv {
		if os.Getenv(key) == "" {
			fmt.Fprintf(os.Stderr, "required env var %s not set\n", key)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
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

// newServiceNames returns service names that appeared after a scale operation
// by diffing the current list against a baseline snapshot.
func newServiceNames(p *plugin.TargetPlugin, before []string) ([]string, error) {
	after, err := p.ListServiceNames()
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(before))
	for _, n := range before {
		known[n] = true
	}
	var added []string
	for _, n := range after {
		if !known[n] {
			added = append(added, n)
		}
	}
	return added, nil
}

// --- tests ---

// TestStatus verifies the plugin connects to Nomad and OVH and returns
// a valid status. Uses a non-existent node_pool so count is 0 regardless
// of whether a Nomad dev agent is running.
func TestStatus(t *testing.T) {
	p := setupPlugin(t)
	cfg := policyConfig()
	cfg["node_pool"] = "e2e-nonexistent"

	status, err := p.Status(cfg)
	must.NoError(t, err)
	must.True(t, status.Ready)
	must.Eq(t, int64(0), status.Count)
}

// TestScaleLifecycle orders the cheapest Eco server, verifies delivery,
// then terminates it. This test incurs real OVH costs and takes 20-60 minutes.
func TestScaleLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running scale lifecycle test")
	}

	p := setupPlugin(t)
	cfg := policyConfig()
	ctx := context.Background()

	// Snapshot service names before ordering so we can diff afterward.
	before, err := p.ListServiceNames()
	must.NoError(t, err)

	// Safety-net cleanup: terminate any servers added during this test.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		defer cancel()

		added, _ := newServiceNames(p, before)
		for _, name := range added {
			t.Logf("cleanup: terminating %s", name)
			p.TerminateServer(cleanupCtx, name) //nolint:errcheck
		}
	})

	// 1. Scale out: order 1 server.
	t.Log("ordering server (this takes 10-40 minutes)...")
	err = p.Scale(sdk.ScalingAction{Count: 1}, cfg)
	must.NoError(t, err)

	// 2. Verify a new service name appeared.
	added, err := newServiceNames(p, before)
	must.NoError(t, err)
	must.Eq(t, 1, len(added))
	t.Logf("server delivered: %s", added[0])

	// Note: we don't assert on Status().Count here because the ordered
	// server hasn't joined Nomad (no ovh_user_data bootstrap in e2e).
	// The OVH service-name diff above is the delivery verification.

	// 3. Terminate the server.
	t.Log("terminating server (this takes 1-10 minutes)...")
	err = p.TerminateServer(ctx, added[0])
	must.NoError(t, err)
	t.Logf("server terminated: %s", added[0])
}
