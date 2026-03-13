// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

//go:build e2e

// Run: make e2e (requires a running nomad dev agent: make dev)

package e2e

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ovh/go-ovh/ovh"
	"github.com/shoenig/test/must"
)

const autoscalerAddr = "http://127.0.0.1:8080"

var (
	autoscalerProc *exec.Cmd
	tmpDir         string
)

// TestMain starts the autoscaler subprocess with our plugin binary and
// runs all tests. The Nomad dev agent must already be running (make dev).
func TestMain(m *testing.M) {
	for _, key := range []string{"OVH_APPLICATION_KEY", "OVH_APPLICATION_SECRET", "OVH_CONSUMER_KEY"} {
		if os.Getenv(key) == "" {
			fmt.Fprintf(os.Stderr, "required env var %s not set\n", key)
			os.Exit(1)
		}
	}
	if _, err := exec.LookPath("nomad-autoscaler"); err != nil {
		fmt.Fprintln(os.Stderr, "nomad-autoscaler not found on PATH")
		os.Exit(1)
	}

	var err error
	tmpDir, err = os.MkdirTemp("", "ovh-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp dir: %v\n", err)
		os.Exit(1)
	}

	if err := startAutoscaler(); err != nil {
		fmt.Fprintf(os.Stderr, "starting autoscaler: %v\n", err)
		os.RemoveAll(tmpDir)
		os.Exit(1)
	}

	code := m.Run()

	if autoscalerProc != nil && autoscalerProc.Process != nil {
		autoscalerProc.Process.Kill()
		autoscalerProc.Wait() //nolint:errcheck
	}
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

// --- autoscaler lifecycle ---

func startAutoscaler() error {
	// Copy built plugin binary into a temp plugin dir.
	pluginDir := filepath.Join(tmpDir, "plugins")
	os.MkdirAll(pluginDir, 0o755)

	bin := findPluginBinary()
	if bin == "" {
		return fmt.Errorf("plugin binary not found (run make build)")
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		return fmt.Errorf("reading plugin binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, filepath.Base(bin)), data, 0o755); err != nil {
		return fmt.Errorf("copying plugin binary: %v", err)
	}

	// Policy dir — write lifecycle policy if env vars are set.
	policyDir := filepath.Join(tmpDir, "policies")
	os.MkdirAll(policyDir, 0o755)

	if lifecycleEnvSet() {
		if err := writeLifecyclePolicy(policyDir); err != nil {
			return fmt.Errorf("writing policy: %v", err)
		}
	}

	// Generate autoscaler agent config with credentials from env.
	cfg := fmt.Sprintf(`log_level  = "DEBUG"
plugin_dir = %q

nomad {
  address = "http://127.0.0.1:4646"
}

http {
  bind_address = "127.0.0.1"
  bind_port    = 8080
}

policy {
  dir = %q
}

target "ovh-dedicated" {
  driver = "ovh-dedicated"
  config = {
    ovh_application_key    = %q
    ovh_application_secret = %q
    ovh_consumer_key       = %q
    ovh_endpoint           = %q
    ovh_subsidiary         = %q
  }
}
`, pluginDir, policyDir,
		os.Getenv("OVH_APPLICATION_KEY"),
		os.Getenv("OVH_APPLICATION_SECRET"),
		os.Getenv("OVH_CONSUMER_KEY"),
		envOrDefault("OVH_ENDPOINT", "ovh-eu"),
		envOrDefault("OVH_SUBSIDIARY", "FR"))

	cfgPath := filepath.Join(tmpDir, "autoscaler.hcl")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		return fmt.Errorf("writing config: %v", err)
	}

	// Start autoscaler agent.
	autoscalerProc = exec.Command("nomad-autoscaler", "agent", "-config", cfgPath)
	autoscalerProc.Stdout = os.Stdout
	autoscalerProc.Stderr = os.Stderr
	if err := autoscalerProc.Start(); err != nil {
		return fmt.Errorf("starting autoscaler: %v", err)
	}

	// Wait for health.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(autoscalerAddr + "/v1/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("autoscaler not healthy after 30s")
}

func findPluginBinary() string {
	for _, p := range []string{
		"../build/nomad-autoscaler-ovh",
		"build/nomad-autoscaler-ovh",
	} {
		if _, err := os.Stat(p); err == nil {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}
	return ""
}

// --- policy helpers ---

func lifecycleEnvSet() bool {
	return os.Getenv("E2E_PLAN_CODE") != ""
}

func writeLifecyclePolicy(dir string) error {
	policy := fmt.Sprintf(`scaling "e2e" {
  enabled = true
  min     = 1
  max     = 1

  policy {
    evaluation_interval = "10s"
    cooldown            = "5m"
    on_check_error      = "ignore"

    check "placeholder" {
      source = "nomad-apm"
      query  = "percentage-allocated_cpu"

      strategy "target-value" {
        target = 70
      }
    }

    target "ovh-dedicated" {
      datacenter           = "dc1"
      node_class           = "ovh-e2e"
      ovh_datacenter       = %q
      ovh_plan_code        = %q
      ovh_operating_system  = %q
      ovh_product_type     = %q
    }
  }
}
`, envOrDefault("E2E_DATACENTER", "gra3"),
		os.Getenv("E2E_PLAN_CODE"),
		envOrDefault("E2E_OS_TEMPLATE", "debian12_64"),
		envOrDefault("E2E_PRODUCT_TYPE", "eco"))

	return os.WriteFile(filepath.Join(dir, "e2e.hcl"), []byte(policy), 0o644)
}

// --- OVH helpers ---

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func newOVHClient(t *testing.T) *ovh.Client {
	t.Helper()
	client, err := ovh.NewClient(
		envOrDefault("OVH_ENDPOINT", "ovh-eu"),
		os.Getenv("OVH_APPLICATION_KEY"),
		os.Getenv("OVH_APPLICATION_SECRET"),
		os.Getenv("OVH_CONSUMER_KEY"),
	)
	must.NoError(t, err)
	return client
}

func listServiceNames(client *ovh.Client) ([]string, error) {
	var names []string
	if err := client.Get("/dedicated/server", &names); err != nil {
		return nil, err
	}
	return names, nil
}

// --- tests ---

// TestPluginHealthy verifies the autoscaler loaded our plugin binary via
// go-plugin RPC. If SetConfig fails or the binary is broken, the autoscaler
// won't report healthy.
func TestPluginHealthy(t *testing.T) {
	resp, err := http.Get(autoscalerAddr + "/v1/health")
	must.NoError(t, err)
	defer resp.Body.Close()
	must.Eq(t, 200, resp.StatusCode)
}

// TestScaleLifecycle verifies the autoscaler evaluates our scaling policy
// and creates an OVH server (min=1 enforcement on empty pool).
//
// This test incurs real OVH costs. Requires E2E_PLAN_CODE env var.
func TestScaleLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: -short")
	}
	if !lifecycleEnvSet() {
		t.Skip("skipping: E2E_PLAN_CODE required")
	}

	client := newOVHClient(t)

	// Snapshot service names before.
	before, err := listServiceNames(client)
	must.NoError(t, err)

	// Cleanup any servers created during this test.
	t.Cleanup(func() {
		after, _ := listServiceNames(client)
		known := make(map[string]bool, len(before))
		for _, n := range before {
			known[n] = true
		}
		for _, n := range after {
			if !known[n] {
				t.Logf("cleanup: terminating %s", n)
				// Best-effort terminate — POST /dedicated/server/{name}/terminate
				client.Post("/dedicated/server/"+n+"/terminate", nil, nil) //nolint:errcheck
			}
		}
	})

	// Wait for autoscaler to create a server (min=1 policy).
	t.Log("waiting for autoscaler to order server...")
	deadline := time.After(20 * time.Minute)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for server creation")
		default:
		}

		after, _ := listServiceNames(client)
		known := make(map[string]bool, len(before))
		for _, n := range before {
			known[n] = true
		}
		for _, n := range after {
			if !known[n] {
				t.Logf("server created by autoscaler: %s", n)
				return
			}
		}
		time.Sleep(30 * time.Second)
	}
}
