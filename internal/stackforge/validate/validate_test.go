package validate

import (
	"testing"

	"stackforge/internal/stackforge/config"
)

func TestValidateFailsUnsafeConfigExposure(t *testing.T) {
	cfg := &config.Config{
		Cluster:      config.ClusterConfig{Name: "stackforge-production"},
		Nodes:        []config.NodeConfig{{Name: "node-1", Address: "203.0.113.11", PublicAddress: "203.0.113.11", Roles: []string{"consul-server", "nomad-server", "database", "control-plane"}}},
		Network:      config.NetworkConfig{AllowedAdminCIDRs: []string{"0.0.0.0/0"}, AllowedSSHCIDRs: []string{"1.2.3.4/32"}, AllowPublicInternalCommunication: true},
		Database:     config.DatabaseConfig{Engine: "postgres"},
		Traefik:      config.TraefikConfig{DashboardBasicAuth: true},
		ControlPlane: config.ControlPlaneConfig{Domain: "control.example.com", AdminAPIKeys: []string{"key"}},
	}
	report := Run(nil, cfg, nil, true, false)
	if report.Safe {
		t.Fatalf("expected unsafe report: %+v", report)
	}
}

func TestParsePreflightFailsNftablesOnly(t *testing.T) {
	var checks []Check
	parsePreflight("node-1", "os=ubuntu:24.04\nsudo=ok\napt=ok\nfirewall=nftables\nprivate_ip=10.0.0.11\nports=\n", false, func(node, name, status, message string) {
		checks = append(checks, Check{Node: node, Name: name, Status: status, Message: message})
	})
	for _, c := range checks {
		if c.Name == "firewall" && c.Status == "fail" {
			return
		}
	}
	t.Fatalf("expected nftables-only firewall failure: %+v", checks)
}
