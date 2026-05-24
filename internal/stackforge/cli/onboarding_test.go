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
		Cluster: config.ClusterConfig{Name: "stackforge-staging", Environment: "staging"},
		SSH:     config.SSHConfig{User: "deploy", Port: 2222, PrivateKeyPath: "/tmp/key"},
		Nodes: []config.NodeConfig{{
			Name:          "node-1",
			Address:       "10.0.0.10",
			PublicAddress: "203.0.113.10",
			Roles:         []string{"consul-server", "nomad-client", "docker-host"},
		}},
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
