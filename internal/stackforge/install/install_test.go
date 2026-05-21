package install

import (
	"context"
	"path/filepath"
	"testing"

	"stackforge/internal/stackforge/config"
	"stackforge/internal/stackforge/inventory"
)

func TestDryRunWritesInventorySecretsAndReport(t *testing.T) {
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
		if _, err := filepath.Abs(filepath.Join(state, path)); err != nil {
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
