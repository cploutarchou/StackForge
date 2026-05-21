package verify

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/remoteexec"
)

type fakeExec struct {
	stdout string
}

func (f fakeExec) Run(ctx context.Context, node string, cmd remoteexec.Command) (remoteexec.Result, error) {
	return remoteexec.Result{Stdout: f.stdout}, nil
}

func TestRunPassesWithObservedState(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "generated-secrets.yaml"), []byte("secret: redacted\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inv := &inventory.Inventory{
		ClusterName: "stackforge-staging",
		Nodes: []inventory.Node{{
			Name:      "node-1",
			PrivateIP: "10.20.0.11",
			Roles:     []string{"consul-server", "nomad-server", "traefik", "database", "control-plane"},
			SSH:       inventory.SSHInfo{User: "deployer", Port: 22, PrivateKeyPath: "~/.ssh/id_ed25519"},
		}},
	}
	stdout := `stackforge_service=active
stackforge_health=OK
stackforge_api_code=401
postgres_service=active
postgres_query=1
postgres_migrations=t
postgres_public=no
consul_service=active
consul_leader=10.20.0.11:8300
consul_members=10.20.0.11:8300
nomad_service=active
nomad_leader=10.20.0.11:4647
traefik_service=active
traefik_ports=0.0.0.0:80,0.0.0.0:443
firewall=ufw
remote_env_mode=600
`
	report := Run(context.Background(), state, inv, fakeExec{stdout: stdout})
	if !report.Safe {
		t.Fatalf("expected safe report: %+v", report.Errors)
	}
}

func TestRunFailsPublicDatabase(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "generated-secrets.yaml"), []byte("secret: redacted\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inv := &inventory.Inventory{ClusterName: "stackforge-staging", Nodes: []inventory.Node{{Name: "db-1", PrivateIP: "10.20.0.11", Roles: []string{"database"}}}}
	report := Run(context.Background(), state, inv, fakeExec{stdout: "postgres_service=active\npostgres_query=1\npostgres_migrations=t\npostgres_public=yes\nfirewall=ufw\n"})
	if report.Safe {
		t.Fatalf("expected unsafe report")
	}
}
