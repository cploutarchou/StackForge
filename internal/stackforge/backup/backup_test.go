package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stackforge/internal/stackforge/remoteexec"
)

type fakeExec struct {
	results map[string]string
	errFor  string
}

func (f fakeExec) Run(ctx context.Context, node string, cmd remoteexec.Command) (remoteexec.Result, error) {
	for k, v := range f.results {
		if strings.Contains(cmd.Command, k) {
			return remoteexec.Result{Stdout: v}, nil
		}
	}
	return remoteexec.Result{Stdout: "ok"}, nil
}

func TestBackupManifestVerifies(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "inventory.yaml"), []byte("cluster_name: stackforge-production\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Run(state, "stackforge-production")
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(filepath.Join(state, "backups", m.BackupID), m); err != nil {
		t.Fatal(err)
	}
	if len(m.Warnings) == 0 {
		t.Fatal("expected warnings for unexecuted component exports")
	}
}

func TestRestoreRefusesWithoutYes(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "inventory.yaml"), []byte("cluster_name: stackforge-production\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Run(state, "stackforge-production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreWithOptions(state, m.BackupID, false, false); err == nil {
		t.Fatal("expected restore refusal without --yes")
	}
}

func TestRestoreDryRun(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "inventory.yaml"), []byte("cluster_name: stackforge-production\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Run(state, "stackforge-production")
	if err != nil {
		t.Fatal(err)
	}
	report, err := RestoreWithOptions(state, m.BackupID, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun || len(report.Skipped) == 0 {
		t.Fatalf("unexpected dry-run report: %+v", report)
	}
}

func TestRemoteBackupCommandGenerationUsesInventoryRoles(t *testing.T) {
	state := t.TempDir()
	inv := []byte("cluster_name: stackforge-production\nnodes:\n- name: db\n  private_ip: 10.0.0.11\n  roles: [database]\n- name: cp\n  private_ip: 10.0.0.12\n  roles: [consul-server, nomad-server, traefik, control-plane]\n")
	if err := os.WriteFile(filepath.Join(state, "inventory.yaml"), inv, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := RunWithOptions(Options{StateDir: state, Cluster: "stackforge-production", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.PlannedCommands) == 0 {
		t.Fatal("expected planned commands")
	}
	foundDB := false
	for _, p := range m.PlannedCommands {
		if p.Component == "database" && p.Node == "10.0.0.11" && strings.Contains(p.Command, "pg_dump") {
			foundDB = true
		}
	}
	if !foundDB {
		t.Fatalf("database backup plan missing: %+v", m.PlannedCommands)
	}
}

func TestBackupDryRunDoesNotPersistInventoryWarnings(t *testing.T) {
	state := t.TempDir()
	inv := []byte("cluster_name: stackforge-production\nenvironment: production\ninstall_status: installed\nwarnings: []\n")
	if err := os.WriteFile(filepath.Join(state, "inventory.yaml"), inv, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunWithOptions(Options{StateDir: state, Cluster: "stackforge-production", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(state, "inventory.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "backup not executed; dry-run or no live executor configured") {
		t.Fatalf("dry-run backup warning leaked into inventory:\n%s", s)
	}
	if strings.Contains(s, "last_backup_id:") {
		t.Fatalf("dry-run backup should not set last_backup_id:\n%s", s)
	}
}

func TestRestorePartialFailureReporting(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "inventory.yaml"), []byte("cluster_name: stackforge-production\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Run(state, "stackforge-production")
	if err != nil {
		t.Fatal(err)
	}
	report, err := RestoreWithDetailedOptions(RestoreOptions{StateDir: state, BackupID: m.BackupID, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.FullRestore || len(report.Skipped) == 0 {
		t.Fatalf("expected partial restore report, got %+v", report)
	}
	inv, err := os.ReadFile(filepath.Join(state, "inventory.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(inv), "last_restore_id") {
		t.Fatalf("inventory restore marker missing:\n%s", inv)
	}
}
