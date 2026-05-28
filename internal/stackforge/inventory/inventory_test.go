package inventory

import (
	"path/filepath"
	"testing"
)

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.yaml")
	in := &Inventory{ClusterName: "stackforge-production", Environment: "production", Datacenter: "dc1"}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.ClusterName != in.ClusterName {
		t.Fatalf("cluster = %s", out.ClusterName)
	}
}

func TestApplyObservationHarvestsRemoteState(t *testing.T) {
	inv := &Inventory{Nodes: []Node{{Name: "node-1", Components: map[string]string{}, Services: map[string]string{}, Versions: map[string]string{}, Leaders: map[string]string{}}}, ComponentVersions: map[string]string{}, ServiceStatus: map[string]string{}}
	stdout := "os_name=Ubuntu 24.04 LTS\nos_version=24.04\nkernel=6.8.0\nobserved_ips=10.0.0.11,203.0.113.11\nfirewall=ufw\nlistening=tcp/127.0.0.1:5432,tcp/0.0.0.0:443\nconsul_version=1.20.0\nconsul_service=active\nconsul_leader=10.0.0.11:8300\nnomad_version=1.9.0\nnomad_service=active\ntraefik_version=3.3.3\npostgres_version=16\nstackforge_service=active\n"
	ApplyObservation(inv, "node-1", stdout, nil)
	n := inv.Nodes[0]
	if n.OSVersion != "24.04" || n.Kernel == "" || n.Firewall != "ufw" {
		t.Fatalf("observation not applied: %+v", n)
	}
	if inv.ComponentVersions["node-1/consul"] != "1.20.0" || inv.ServiceStatus["node-1/stackforge-control-plane"] != "active" {
		t.Fatalf("component/service maps not updated: %+v %+v", inv.ComponentVersions, inv.ServiceStatus)
	}
}

func TestNormalizeAppliesProductionReadyDefaultsAndDedupesWarnings(t *testing.T) {
	inv := &Inventory{
		Warnings: []string{"EOF", "", "EOF", "  "},
		Nodes: []Node{{
			Name:     "node-1",
			Warnings: []string{"warn-a", "warn-a", ""},
		}},
	}
	Normalize(inv)
	if inv.InstallStatus != "pending" {
		t.Fatalf("install status = %q, want pending", inv.InstallStatus)
	}
	if inv.LastHealthCheckStatus != "pending" {
		t.Fatalf("health status = %q, want pending", inv.LastHealthCheckStatus)
	}
	if inv.FirewallMode != "ufw" {
		t.Fatalf("firewall mode = %q, want ufw", inv.FirewallMode)
	}
	if len(inv.Warnings) != 0 {
		t.Fatalf("ephemeral warnings should be filtered: %+v", inv.Warnings)
	}
	if inv.Nodes[0].HealthStatus != "pending" {
		t.Fatalf("node health status = %q, want pending", inv.Nodes[0].HealthStatus)
	}
	if len(inv.Nodes[0].Warnings) != 1 || inv.Nodes[0].Warnings[0] != "warn-a" {
		t.Fatalf("node warnings not deduped/trimmed: %+v", inv.Nodes[0].Warnings)
	}
}
