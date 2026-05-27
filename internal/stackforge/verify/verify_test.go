package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/remoteexec"
)

type fakeExec struct {
	stdout  string
	failFor map[string]error
	seen    []string
}

func (f *fakeExec) Run(ctx context.Context, node string, cmd remoteexec.Command) (remoteexec.Result, error) {
	f.seen = append(f.seen, node)
	if err := f.failFor[node]; err != nil {
		return remoteexec.Result{Stderr: "dial failed"}, err
	}
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
	exec := &fakeExec{stdout: stdout}
	report := Run(context.Background(), state, inv, exec)
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
	report := Run(context.Background(), state, inv, &fakeExec{stdout: "postgres_service=active\npostgres_query=1\npostgres_migrations=t\npostgres_public=yes\nfirewall=ufw\n"})
	if report.Safe {
		t.Fatalf("expected unsafe report")
	}
}

func TestRunFallsBackToPublicAddress(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "generated-secrets.yaml"), []byte("secret: redacted\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inv := &inventory.Inventory{ClusterName: "stackforge-staging", Nodes: []inventory.Node{{Name: "node-1", PrivateIP: "10.20.0.11", PublicIP: "93.184.216.34", Roles: []string{"traefik"}}}}
	exec := &fakeExec{
		stdout:  "traefik_service=active\ntraefik_ports=0.0.0.0:80,0.0.0.0:443\nfirewall=ufw\n",
		failFor: map[string]error{"10.20.0.11": errors.New("connect: no route to host")},
	}
	report := Run(context.Background(), state, inv, exec)
	if !report.Safe {
		t.Fatalf("expected public fallback to pass: %+v", report.Errors)
	}
	if len(exec.seen) < 2 || exec.seen[0] != "10.20.0.11" || exec.seen[1] != "93.184.216.34" {
		t.Fatalf("expected private then public attempt, got %+v", exec.seen)
	}
}

func TestCommandUsesShellSafeMigrationQuery(t *testing.T) {
	cmd := Command()
	if !strings.Contains(cmd, "to_regclass($$public.domains$$)") {
		t.Fatalf("expected dollar-quoted to_regclass query in command, got: %s", cmd)
	}
	if strings.Contains(cmd, `to_regclass('public.domains')`) && strings.Contains(cmd, `\\\"SELECT`) {
		t.Fatalf("command still appears to contain the old escaped quoting pattern")
	}
	if !strings.Contains(cmd, "ss -ltnH '( sport = :5432 )'") {
		t.Fatalf("expected postgres public detection to use filtered ss query")
	}
}

func TestIsHealthyValue(t *testing.T) {
	if !isHealthyValue("ok") {
		t.Fatal("expected plain ok to pass")
	}
	if !isHealthyValue(`{"status":"ok"}`) {
		t.Fatal("expected JSON health payload to pass")
	}
	if isHealthyValue(`{"status":"degraded"}`) {
		t.Fatal("did not expect degraded status to pass")
	}
}
