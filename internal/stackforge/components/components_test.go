package components

import (
	"strings"
	"testing"

	"stackforge/internal/stackforge/inventory"
)

func TestDockerInstallCommandUsesOfficialAptRepoAndComposePlugin(t *testing.T) {
	cmd := DockerInstallCommand("deploy", false)
	for _, want := range []string{"download.docker.com", "docker-ce", "docker-compose-plugin", "systemctl enable docker", "docker info"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("missing %q in docker command: %s", want, cmd)
		}
	}
}

func TestBasePackageInstallPlan(t *testing.T) {
	cmd := BasePackagesCommand()
	for _, want := range []string{"curl", "jq", "ufw", "openssl", "iproute2", "apt-get install"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("missing %q in base package command: %s", want, cmd)
		}
	}
}

func TestPlanInstallAllIncludesRoleComponents(t *testing.T) {
	inv := &inventory.Inventory{Nodes: []inventory.Node{{Name: "node-1", PrivateIP: "10.0.0.10", Roles: []string{"nomad-client", "docker-host", "traefik"}, SSH: inventory.SSHInfo{User: "deploy"}}}}
	items, err := PlanInstall(inv, All, "node-1", true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, item := range items {
		got[item.Component] = true
	}
	for _, want := range []string{BasePackages, Docker, Nomad, Traefik} {
		if !got[want] {
			t.Fatalf("expected %s in plan, got %+v", want, items)
		}
	}
}

func TestParseStatus(t *testing.T) {
	statuses := ParseStatus("node-1", "docker_installed=yes\ndocker_version=26.1.0\ndocker_service=active\nlistening=0.0.0.0:80,127.0.0.1:4646\n")
	for _, st := range statuses {
		if st.Component == Docker {
			if !st.Installed || st.Version != "26.1.0" || st.Systemd != "active" || len(st.Ports) != 2 {
				t.Fatalf("unexpected docker status: %+v", st)
			}
			return
		}
	}
	t.Fatal("docker status not found")
}
