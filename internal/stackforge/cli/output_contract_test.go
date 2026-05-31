package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"stackforge/internal/stackforge/inventory"
)

func TestConsulSnapshotSaveOutputContractDryRun(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })

	rootOpts = orig
	rootOpts.output = "json"
	rootOpts.dryRun = true
	rootOpts.cluster = "contract-consul"
	rootOpts.stateDir = t.TempDir()
	rootOpts.configPath = ""

	writeContractTestInventory(t, &inventory.Inventory{
		Environment:     "staging",
		ConsulEndpoints: []string{"http://10.0.0.20:8500"},
		Nodes: []inventory.Node{{
			Name:      "consul-1",
			Roles:     []string{"consul-server"},
			PrivateIP: "10.0.0.20",
		}},
	})

	out := runCommandJSON(t, func() error {
		return runConsulSnapshotSave("/var/backups/consul.snap", "", "dc1")
	})
	assertOutputContractKeys(t, out)
}

func TestNomadDrainNodeOutputContractDryRun(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })

	rootOpts = orig
	rootOpts.output = "json"
	rootOpts.dryRun = true
	rootOpts.cluster = "contract-nomad"
	rootOpts.stateDir = t.TempDir()
	rootOpts.configPath = ""

	writeContractTestInventory(t, &inventory.Inventory{
		Environment:    "staging",
		NomadEndpoints: []string{"http://10.0.0.10:4646"},
		Nodes: []inventory.Node{{
			Name:      "nomad-1",
			Roles:     []string{"nomad-server"},
			PrivateIP: "10.0.0.10",
		}},
	})

	out := runCommandJSON(t, func() error {
		return runNomadDrainNode("node-abc", false, "global", "dc1", "")
	})
	assertOutputContractKeys(t, out)
}

func TestTraefikConsulCatalogCheckOutputContract(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })

	rootOpts = orig
	rootOpts.output = "json"
	rootOpts.dryRun = true
	rootOpts.cluster = "contract-traefik"
	rootOpts.stateDir = t.TempDir()
	rootOpts.configPath = ""

	writeContractTestInventory(t, &inventory.Inventory{
		Environment:     "staging",
		ConsulEndpoints: []string{"http://10.0.0.20:8500"},
		Nodes: []inventory.Node{{
			Name:      "edge-1",
			Roles:     []string{"traefik", "consul-server"},
			PrivateIP: "10.0.0.20",
		}},
	})

	out := runCommandJSON(t, runTraefikConsulCatalogCheck)
	assertOutputContractKeys(t, out)
}

func writeContractTestInventory(t *testing.T, inv *inventory.Inventory) {
	t.Helper()
	path := filepath.Join(rootOpts.stateDir, rootOpts.cluster, "inventory.yaml")
	if err := inventory.Save(path, inv); err != nil {
		t.Fatalf("save inventory: %v", err)
	}
}

func runCommandJSON(t *testing.T, fn func() error) map[string]any {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = oldStdout
	if runErr != nil {
		t.Fatalf("command run failed: %v", runErr)
	}

	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("parse json output: %v\nraw=%s", err, string(b))
	}
	return out
}

func assertOutputContractKeys(t *testing.T, out map[string]any) {
	t.Helper()
	for _, key := range []string{"command", "cluster", "target", "dry_run", "status", "result", "warnings"} {
		if _, ok := out[key]; !ok {
			t.Fatalf("missing output contract key %q in %#v", key, out)
		}
	}
	if _, ok := out["warnings"].([]any); !ok {
		t.Fatalf("warnings should be an array, got %#v", out["warnings"])
	}
}
