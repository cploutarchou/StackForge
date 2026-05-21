package config

import "testing"

func TestValidateRejectsPublicInternalCommunication(t *testing.T) {
	cfg := validConfig()
	cfg.Nodes[0].Address = "203.0.113.10"
	cfg.Nodes[0].PublicAddress = "203.0.113.10"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected public internal communication rejection")
	}
}

func TestValidateAcceptsSingleNode(t *testing.T) {
	cfg := validConfig()
	if err := Validate(cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func validConfig() *Config {
	return &Config{
		Cluster:      ClusterConfig{Name: "stackforge-production", Environment: "production", Datacenter: "dc1"},
		SSH:          SSHConfig{User: "root", Port: 22, PrivateKeyPath: "~/.ssh/id_ed25519"},
		Nodes:        []NodeConfig{{Name: "node-1", Address: "10.0.0.11", PublicAddress: "203.0.113.11", Roles: []string{"consul-server", "nomad-server", "nomad-client", "traefik", "database", "control-plane"}}},
		Network:      NetworkConfig{AllowedAdminCIDRs: []string{"1.2.3.4/32"}, AllowedSSHCIDRs: []string{"1.2.3.4/32"}},
		Traefik:      TraefikConfig{DashboardEnabled: true, DashboardBasicAuth: true},
		Database:     DatabaseConfig{Engine: "postgres"},
		ControlPlane: ControlPlaneConfig{Domain: "control.example.com", APIPort: 8080, AdminAPIKeys: []string{"key"}},
	}
}
