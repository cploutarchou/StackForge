package cli

import (
	"testing"

	"stackforge/internal/stackforge/config"
	"stackforge/internal/stackforge/inventory"
)

func TestBuildTraefikConsulCatalogFindingsMissingInventoryData(t *testing.T) {
	inv := &inventory.Inventory{Nodes: []inventory.Node{{Name: "node-1", Roles: []string{"control-plane"}}}}
	findings := buildTraefikConsulCatalogFindings(inv, nil, false)
	if len(findings) < 2 {
		t.Fatalf("expected blocker findings, got %+v", findings)
	}
}

func TestBuildTraefikConsulCatalogFindingsWithConfig(t *testing.T) {
	inv := &inventory.Inventory{
		Nodes:           []inventory.Node{{Name: "node-1", Roles: []string{"traefik", "consul-server"}}},
		ConsulEndpoints: []string{"http://10.0.0.10:8500"},
	}
	cfg := &config.Config{}
	cfg.Traefik.DashboardEnabled = true
	cfg.Traefik.DashboardBasicAuth = false
	findings := buildTraefikConsulCatalogFindings(inv, cfg, true)
	if len(findings) == 0 {
		t.Fatalf("expected findings, got none")
	}
	hasWarning := false
	for _, finding := range findings {
		if finding["code"] == "traefik.dashboard_auth" {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Fatalf("expected dashboard auth warning, got %+v", findings)
	}
}

func TestSelectTraefikNodePrefersRole(t *testing.T) {
	inv := &inventory.Inventory{Nodes: []inventory.Node{
		{Name: "node-1", Roles: []string{"control-plane"}},
		{Name: "node-2", Roles: []string{"traefik"}},
	}}
	n, err := selectTraefikNode(inv)
	if err != nil {
		t.Fatalf("selectTraefikNode returned error: %v", err)
	}
	if n.Name != "node-2" {
		t.Fatalf("expected node-2, got %q", n.Name)
	}
}
