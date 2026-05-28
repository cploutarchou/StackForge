package cli

import (
	"bufio"
	"bytes"
	"testing"

	"stackforge/internal/stackforge/bootstrap"
	"stackforge/internal/stackforge/config"
)

func TestOnboardRoleAssignmentFromConfig(t *testing.T) {
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "stackforge-staging", Environment: "staging", Datacenter: "dc1"},
		SSH:     config.SSHConfig{User: "deploy", Port: 2222, PrivateKeyPath: "/tmp/key"},
		Nodes: []config.NodeConfig{{
			Name:          "node-1",
			Address:       "10.0.0.10",
			PublicAddress: "203.0.113.10",
			Roles:         []string{"consul-server", "nomad-server", "traefik", "database", "control-plane"},
		}},
		ControlPlane: config.ControlPlaneConfig{APIPort: 8080},
	}
	inv := inventoryFromConfig(cfg)
	if len(inv.Nodes) != 1 {
		t.Fatalf("expected one node, got %d", len(inv.Nodes))
	}
	node := inv.Nodes[0]
	if node.Name != "node-1" || node.PrivateIP != "10.0.0.10" || node.PublicIP != "203.0.113.10" {
		t.Fatalf("unexpected inventory node: %+v", node)
	}
	if node.SSH.User != "deploy" || node.SSH.Port != 2222 || node.SSH.PrivateKeyPath != "/tmp/key" {
		t.Fatalf("unexpected SSH inventory: %+v", node.SSH)
	}
	if node.HealthStatus != "pending-onboarding" {
		t.Fatalf("unexpected node health status: %q", node.HealthStatus)
	}
	if inv.LastHealthCheckStatus != "pending" || inv.FirewallMode != "ufw" {
		t.Fatalf("unexpected inventory defaults: health=%q firewall=%q", inv.LastHealthCheckStatus, inv.FirewallMode)
	}
	if len(inv.ConsulEndpoints) != 1 || inv.ConsulEndpoints[0] != "http://10.0.0.10:8500" {
		t.Fatalf("unexpected consul endpoints: %+v", inv.ConsulEndpoints)
	}
	if len(inv.NomadEndpoints) != 1 || inv.NomadEndpoints[0] != "http://10.0.0.10:4646" {
		t.Fatalf("unexpected nomad endpoints: %+v", inv.NomadEndpoints)
	}
	if len(inv.TraefikEndpoints) != 1 || inv.TraefikEndpoints[0] != "http://203.0.113.10" {
		t.Fatalf("unexpected traefik endpoints: %+v", inv.TraefikEndpoints)
	}
	if inv.DatabaseEndpoint != "10.0.0.10" {
		t.Fatalf("unexpected database endpoint: %q", inv.DatabaseEndpoint)
	}
	if inv.ControlPlaneEndpoint != "http://10.0.0.10:8080" {
		t.Fatalf("unexpected control plane endpoint: %q", inv.ControlPlaneEndpoint)
	}
}

func TestBootstrapNodesFromConfigUsesPublicAddressAndRoles(t *testing.T) {
	cfg := &config.Config{
		SSH: config.SSHConfig{User: "root", Port: 22, PrivateKeyPath: "/tmp/key"},
		Nodes: []config.NodeConfig{{
			Name:          "node-1",
			Address:       "10.0.0.10",
			PublicAddress: "203.0.113.10",
			Roles:         []string{"database"},
		}},
	}
	nodes := bootstrapNodesFromConfig(cfg, bootstrap.AuthPrivateKey, "/tmp/key.pub")
	if len(nodes) != 1 {
		t.Fatalf("expected one bootstrap node, got %d", len(nodes))
	}
	if nodes[0].Address != "203.0.113.10" || nodes[0].PrivateIP != "10.0.0.10" || nodes[0].Roles[0] != "database" {
		t.Fatalf("unexpected bootstrap node: %+v", nodes[0])
	}
}

func TestConfirmationRefusesWithoutTTY(t *testing.T) {
	if err := confirmText("onboard stackforge-staging"); err == nil {
		t.Fatal("expected non-TTY confirmation refusal")
	}
}

func TestAskBoolDefault(t *testing.T) {
	if !askBool(bufio.NewReader(bytes.NewBufferString("\n")), "Install Docker", true) {
		t.Fatal("expected default true")
	}
}
