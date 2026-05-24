package firewall

import (
	"strings"
	"testing"

	"stackforge/internal/stackforge/config"
)

func TestBuildPlanRestrictsDatabase(t *testing.T) {
	cfg := &config.Config{
		SSH:          config.SSHConfig{Port: 22},
		Nodes:        []config.NodeConfig{{Name: "n1", Address: "10.0.0.11"}},
		Network:      config.NetworkConfig{AllowedAdminCIDRs: []string{"1.2.3.4/32"}, AllowedSSHCIDRs: []string{"1.2.3.4/32"}},
		Database:     config.DatabaseConfig{Engine: "postgres"},
		ControlPlane: config.ControlPlaneConfig{APIPort: 8080},
	}
	plan, err := BuildPlan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range plan.Rules {
		if r.Port == 5432 && r.Source == "0.0.0.0/0" {
			t.Fatal("database exposed publicly")
		}
	}
}

func TestBuildPlanRequiresAdminCIDR(t *testing.T) {
	_, err := BuildPlan(&config.Config{SSH: config.SSHConfig{Port: 22}, Network: config.NetworkConfig{AllowedSSHCIDRs: []string{"1.2.3.4/32"}}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildPlanRejectsPublicAdminCIDR(t *testing.T) {
	_, err := BuildPlan(&config.Config{SSH: config.SSHConfig{Port: 22}, Network: config.NetworkConfig{AllowedSSHCIDRs: []string{"1.2.3.4/32"}, AllowedAdminCIDRs: []string{"0.0.0.0/0"}}})
	if err == nil {
		t.Fatal("expected public admin CIDR rejection")
	}
}

func TestBuildPlanRejectsPublicSSHUnlessOverride(t *testing.T) {
	cfg := &config.Config{SSH: config.SSHConfig{Port: 22}, Nodes: []config.NodeConfig{{Name: "n1", Address: "10.0.0.11"}}, Network: config.NetworkConfig{AllowedSSHCIDRs: []string{"0.0.0.0/0"}, AllowedAdminCIDRs: []string{"1.2.3.4/32"}}, Database: config.DatabaseConfig{Engine: "postgres"}, ControlPlane: config.ControlPlaneConfig{APIPort: 8080}}
	if _, err := BuildPlan(cfg); err == nil {
		t.Fatal("expected public SSH rejection")
	}
	if _, err := BuildPlanWithOptions(cfg, Options{AllowPublicSSH: true}); err != nil {
		t.Fatalf("expected override to allow public SSH, got %v", err)
	}
}

func TestValidateRejectsPublicInternalExposure(t *testing.T) {
	cases := []Rule{
		{Port: 5432, Source: "0.0.0.0/0", Purpose: "postgres database"},
		{Port: 4646, Source: "0.0.0.0/0", Purpose: "Nomad HTTP/UI"},
		{Port: 8500, Source: "0.0.0.0/0", Purpose: "Consul HTTP/UI"},
		{Port: 8080, Source: "0.0.0.0/0", Purpose: "Traefik dashboard"},
	}
	for _, rule := range cases {
		if err := Validate(Plan{Rules: []Rule{rule}}); err == nil {
			t.Fatalf("expected rejection for %+v", rule)
		}
	}
}

func TestValidateAllowsPublicHTTPAndHTTPS(t *testing.T) {
	err := Validate(Plan{Rules: []Rule{
		{Port: 80, Source: "0.0.0.0/0", Purpose: "public HTTP"},
		{Port: 443, Source: "0.0.0.0/0", Purpose: "public HTTPS"},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUFWCommandGenerationIncludesApplyAndEnable(t *testing.T) {
	cmds := UFWCommands(Plan{Rules: []Rule{{Port: 443, Protocol: "tcp", Source: "0.0.0.0/0", Purpose: "public HTTPS"}}})
	joined := strings.Join(cmds, "\n")
	for _, want := range []string{"ufw --force reset", "ufw allow from 0.0.0.0/0 to any port 443 proto tcp", "ufw --force enable"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
}

func TestUFWDetectRefusesNftablesOnly(t *testing.T) {
	cmd := UFWDetectCommand()
	if !strings.Contains(cmd, "nftables-only") || !strings.Contains(cmd, "exit 2") {
		t.Fatalf("expected nftables-only refusal command, got %s", cmd)
	}
}
