package safety

import (
	"strings"
	"testing"

	"stackforge/internal/stackforge/config"
)

func TestCheckRefusalPaths(t *testing.T) {
	tests := []struct {
		name string
		edit func(*config.Config)
		opts Options
		code string
	}{
		{
			name: "example IP",
			edit: func(cfg *config.Config) { cfg.Nodes[0].Address = "10.0.0.11" },
			opts: Options{Live: true, ConfirmProduction: true},
			code: "example-config",
		},
		{
			name: "documentation IP range",
			edit: func(cfg *config.Config) { cfg.Nodes[0].PublicAddress = "203.0.113.25" },
			opts: Options{Live: true, ConfirmProduction: true},
			code: "example-config",
		},
		{
			name: "example domain",
			edit: func(cfg *config.Config) { cfg.ControlPlane.Domain = "control.example.com" },
			opts: Options{Live: true, ConfirmProduction: true},
			code: "example-config",
		},
		{
			name: "example cluster",
			edit: func(cfg *config.Config) { cfg.Cluster.Name = "stackforge-demo" },
			opts: Options{Live: true, ConfirmProduction: true},
			code: "example-cluster-name",
		},
		{
			name: "production without confirmation",
			edit: func(cfg *config.Config) { cfg.Cluster.Environment = "production" },
			opts: Options{Live: true},
			code: "production-confirmation-required",
		},
		{
			name: "public admin CIDR",
			edit: func(cfg *config.Config) { cfg.Network.AllowedAdminCIDRs = []string{"0.0.0.0/0"} },
			opts: Options{Live: true, ConfirmProduction: true},
			code: "public-admin-cidr",
		},
		{
			name: "public SSH CIDR",
			edit: func(cfg *config.Config) { cfg.Network.AllowedSSHCIDRs = []string{"0.0.0.0/0"} },
			opts: Options{Live: true, ConfirmProduction: true},
			code: "public-ssh-cidr",
		},
		{
			name: "public database role",
			edit: func(cfg *config.Config) {
				cfg.Nodes[0].PublicAddress = cfg.Nodes[0].Address
			},
			opts: Options{Live: true, ConfirmProduction: true},
			code: "public-database",
		},
		{
			name: "public consul nomad API",
			edit: func(cfg *config.Config) {
				cfg.Network.AllowPublicInternalCommunication = true
			},
			opts: Options{Live: true, ConfirmProduction: true},
			code: "public-internal-api",
		},
		{
			name: "traefik dashboard without auth",
			edit: func(cfg *config.Config) {
				cfg.Traefik.DashboardEnabled = true
				cfg.Traefik.DashboardBasicAuth = false
			},
			opts: Options{Live: true, ConfirmProduction: true},
			code: "traefik-dashboard-auth",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := safeConfig()
			tt.edit(cfg)
			report := Check(cfg, tt.opts)
			if report.Safe {
				t.Fatalf("expected unsafe report")
			}
			if !hasCode(report, tt.code) {
				t.Fatalf("expected code %s in %+v", tt.code, report.Findings)
			}
		})
	}
}

func TestCheckAllowsExplicitPublicSSH(t *testing.T) {
	cfg := safeConfig()
	cfg.Network.AllowedSSHCIDRs = []string{"0.0.0.0/0"}
	report := Check(cfg, Options{Live: true, ConfirmProduction: true, AllowPublicSSH: true})
	if hasCode(report, "public-ssh-cidr") {
		t.Fatalf("did not expect public SSH refusal with override: %+v", report.Findings)
	}
}

func TestCheckWarnsForRootSSH(t *testing.T) {
	cfg := safeConfig()
	cfg.SSH.User = "root"
	report := Check(cfg, Options{Live: true, ConfirmProduction: true})
	if !hasCode(report, "ssh-root") {
		t.Fatalf("expected root SSH warning")
	}
	if err := report.Error(); err != nil && strings.Contains(err.Error(), "ssh-root") {
		t.Fatalf("root SSH should warn but not fail")
	}
}

func safeConfig() *config.Config {
	return &config.Config{
		Cluster: config.ClusterConfig{Name: "stackforge-staging", Environment: "staging", Datacenter: "dc1"},
		SSH:     config.SSHConfig{User: "deployer", Port: 22, PrivateKeyPath: "~/.ssh/id_ed25519"},
		Nodes: []config.NodeConfig{{
			Name:          "node-1",
			Address:       "10.20.0.11",
			PublicAddress: "198.51.100.11",
			Roles:         []string{"consul-server", "nomad-server", "nomad-client", "traefik", "database", "control-plane"},
		}},
		Network: config.NetworkConfig{
			PrivateInterface:  "eth1",
			PublicInterface:   "eth0",
			AllowedAdminCIDRs: []string{"198.51.100.50/32"},
			AllowedSSHCIDRs:   []string{"198.51.100.50/32"},
		},
		Consul:       config.ComponentConfig{ACLEnabled: true},
		Nomad:        config.ComponentConfig{ACLEnabled: true, ClientEnabled: true},
		Traefik:      config.TraefikConfig{DashboardEnabled: true, DashboardBasicAuth: true, DashboardDomain: "traefik.staging.test"},
		Database:     config.DatabaseConfig{Engine: "postgres"},
		ControlPlane: config.ControlPlaneConfig{Domain: "control.staging.test", APIPort: 8080, AdminAPIKeys: []string{"key"}},
	}
}

func hasCode(report Report, code string) bool {
	for _, f := range report.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}
