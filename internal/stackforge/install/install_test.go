package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stackforge/internal/stackforge/config"
	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/remoteexec"
)

func TestDryRunWritesInventorySecretsAndReport(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg := &config.Config{
		Cluster:      config.ClusterConfig{Name: "stackforge-production", Environment: "production", Datacenter: "dc1"},
		SSH:          config.SSHConfig{User: "root", Port: 22, PrivateKeyPath: "~/.ssh/id_ed25519"},
		Nodes:        []config.NodeConfig{{Name: "node-1", Address: "10.0.0.11", PublicAddress: "203.0.113.11", Roles: []string{"consul-server", "nomad-server", "nomad-client", "traefik", "database", "control-plane"}}},
		Network:      config.NetworkConfig{AllowedAdminCIDRs: []string{"1.2.3.4/32"}, AllowedSSHCIDRs: []string{"1.2.3.4/32"}},
		Traefik:      config.TraefikConfig{DashboardEnabled: true, DashboardBasicAuth: true},
		Database:     config.DatabaseConfig{Engine: "postgres"},
		ControlPlane: config.ControlPlaneConfig{Domain: "control.example.com", APIPort: 8080, AdminAPIKeys: []string{"key"}},
	}
	state := t.TempDir()
	r, err := Run(context.Background(), Options{Config: cfg, StateDir: state, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Steps) == 0 {
		t.Fatal("expected steps")
	}
	for _, path := range []string{"inventory.yaml", "generated-secrets.yaml", "install-report.json"} {
		if _, err := os.Stat(filepath.Join(state, path)); err != nil {
			t.Fatal(err)
		}
	}
	inv, err := inventory.Load(filepath.Join(state, "inventory.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if inv.LastSuccessfulStep == "" {
		t.Fatal("expected last successful dry-run step")
	}
}

type fakeExecutor struct {
	result remoteexec.Result
	err    error
}

func (f fakeExecutor) Run(ctx context.Context, node string, cmd remoteexec.Command) (remoteexec.Result, error) {
	return f.result, f.err
}

func TestRemoteStepApplyIncludesStderrOnFailure(t *testing.T) {
	opts := Options{Executor: fakeExecutor{result: remoteexec.Result{Stderr: "permission denied"}, err: errors.New("EOF")}}
	step := remoteStep(opts, "node-1", "10.0.0.11", Step{ID: "node-1:control-plane", Node: "node-1", Role: "control-plane"}, "true", "false", "true")
	err := step.Apply(context.Background())
	if err == nil {
		t.Fatal("expected apply error")
	}
	if !strings.Contains(err.Error(), "EOF") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected wrapped stderr context, got %v", err)
	}
}

func TestInstallStackForgeBinaryCommandAvoidsInlinePayload(t *testing.T) {
	cmd := installStackForgeBinaryCommand()
	if strings.Contains(cmd, "base64 -d > /usr/local/bin/stackforge") {
		t.Fatalf("expected no inline binary payload in command: %s", cmd)
	}
	if !strings.Contains(cmd, "command -v stackforge") {
		t.Fatalf("expected command to rely on preinstalled stackforge binary: %s", cmd)
	}
}
