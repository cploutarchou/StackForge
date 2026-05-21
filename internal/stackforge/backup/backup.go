package backup

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/remoteexec"
)

type Manifest struct {
	BackupID          string            `json:"backup_id"`
	ClusterName       string            `json:"cluster_name"`
	Timestamp         time.Time         `json:"timestamp"`
	Components        []string          `json:"components_included"`
	FilePaths         map[string]string `json:"file_paths"`
	Checksums         map[string]string `json:"checksums"`
	Warnings          []string          `json:"warnings"`
	Errors            []string          `json:"errors"`
	PlannedCommands   []CommandPlan     `json:"planned_commands,omitempty"`
	CreatedBy         string            `json:"created_by"`
	StackForgeVersion string            `json:"stackforge_version"`
}

type CommandPlan struct {
	Component string `json:"component"`
	Node      string `json:"node"`
	Command   string `json:"command"`
	Output    string `json:"output,omitempty"`
}

type Options struct {
	StateDir string
	Cluster  string
	DryRun   bool
	Executor remoteexec.Executor
}

type RestoreReport struct {
	BackupID        string        `json:"backup_id"`
	DryRun          bool          `json:"dry_run"`
	Restored        []string      `json:"restored"`
	Skipped         []string      `json:"skipped"`
	Warnings        []string      `json:"warnings"`
	CompletedAt     time.Time     `json:"completed_at"`
	FullRestore     bool          `json:"full_restore"`
	PlannedCommands []CommandPlan `json:"planned_commands,omitempty"`
	Errors          []string      `json:"errors,omitempty"`
}

type RestoreOptions struct {
	StateDir string
	BackupID string
	Yes      bool
	DryRun   bool
	Executor remoteexec.Executor
}

func Run(stateDir, cluster string) (*Manifest, error) {
	return RunWithOptions(Options{StateDir: stateDir, Cluster: cluster})
}

func RunWithOptions(opts Options) (*Manifest, error) {
	stateDir, cluster := opts.StateDir, opts.Cluster
	id := time.Now().UTC().Format("20060102T150405Z")
	dir := filepath.Join(stateDir, "backups", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	m := &Manifest{BackupID: id, ClusterName: cluster, Timestamp: time.Now().UTC(), Components: []string{"database", "consul", "nomad", "traefik", "stackforge-config", "inventory"}, FilePaths: map[string]string{}, Checksums: map[string]string{}, CreatedBy: "stackforge", StackForgeVersion: "dev"}
	copyIfExists(filepath.Join(stateDir, "inventory.yaml"), filepath.Join(dir, "inventory.yaml"), m)
	copyRedactedIfExists(filepath.Join(stateDir, "generated-secrets.yaml"), filepath.Join(dir, "generated-secrets.redacted.yaml"), m)
	inv, _ := inventory.Load(filepath.Join(stateDir, "inventory.yaml"))
	plans := BuildExportPlans(inv, dir)
	m.PlannedCommands = plans
	if opts.DryRun || opts.Executor == nil {
		for _, p := range plans {
			m.Warnings = append(m.Warnings, p.Component+" backup not executed; dry-run or no live executor configured")
			writeMarker(filepath.Join(dir, filepath.Base(p.Output)), "planned command: "+p.Command+"\n", m)
		}
	} else {
		for _, p := range plans {
			if err := runExport(context.Background(), opts.Executor, p, m); err != nil {
				m.Warnings = append(m.Warnings, err.Error())
				writeMarker(filepath.Join(dir, filepath.Base(p.Output)), err.Error()+"\n", m)
			} else {
				addFile(m, filepath.Join(dir, filepath.Base(p.Output)))
			}
		}
	}
	if err := WriteManifest(dir, m); err != nil {
		return nil, err
	}
	updateInventoryBackup(stateDir, m)
	return m, nil
}

func List(stateDir string) ([]Manifest, error) {
	root := filepath.Join(stateDir, "backups")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := LoadManifest(filepath.Join(root, e.Name(), "backup-manifest.json"))
		if err == nil {
			out = append(out, *m)
		}
	}
	return out, nil
}

func Restore(stateDir, backupID string, yes bool) error {
	_, err := RestoreWithOptions(stateDir, backupID, yes, false)
	return err
}

func RestoreWithOptions(stateDir, backupID string, yes bool, dryRun bool) (*RestoreReport, error) {
	return RestoreWithDetailedOptions(RestoreOptions{StateDir: stateDir, BackupID: backupID, Yes: yes, DryRun: dryRun})
}

func RestoreWithDetailedOptions(opts RestoreOptions) (*RestoreReport, error) {
	stateDir, backupID, yes, dryRun := opts.StateDir, opts.BackupID, opts.Yes, opts.DryRun
	if !yes && !dryRun {
		return nil, fmt.Errorf("restore is destructive; rerun with --yes after validating backup %s", backupID)
	}
	dir := filepath.Join(stateDir, "backups", backupID)
	m, err := LoadManifest(filepath.Join(dir, "backup-manifest.json"))
	if err != nil {
		return nil, err
	}
	if err := Verify(dir, m); err != nil {
		return nil, err
	}
	report := &RestoreReport{BackupID: backupID, DryRun: dryRun, CompletedAt: time.Now().UTC()}
	report.PlannedCommands = BuildRestorePlans(m)
	if dryRun {
		report.Warnings = append(report.Warnings, "dry-run restore: no files or remote services changed")
		report.Skipped = append(report.Skipped, m.Components...)
		writeRestoreReport(dir, report)
		return report, nil
	}
	for _, name := range []string{"inventory.yaml"} {
		src := filepath.Join(dir, name)
		if _, err := os.Stat(src); err == nil {
			copyIfExists(src, filepath.Join(stateDir, name), &Manifest{})
			report.Restored = append(report.Restored, name)
		} else {
			report.Skipped = append(report.Skipped, name)
			report.Warnings = append(report.Warnings, "missing "+name)
		}
	}
	for _, p := range report.PlannedCommands {
		if opts.Executor == nil || p.Node == "" || p.Node == "unknown" {
			report.Skipped = append(report.Skipped, p.Component)
			report.Warnings = append(report.Warnings, p.Component+" live restore command requires explicit remote executor context")
			continue
		}
		cmd, err := restoreCommandWithPayload(dir, p)
		if err != nil {
			report.Skipped = append(report.Skipped, p.Component)
			report.Errors = append(report.Errors, err.Error())
			continue
		}
		res, err := opts.Executor.Run(context.Background(), p.Node, remoteexec.Command{Command: cmd, Sudo: true, Timeout: 10 * time.Minute})
		if err != nil {
			report.Skipped = append(report.Skipped, p.Component)
			report.Errors = append(report.Errors, p.Component+" restore failed: "+err.Error()+" "+strings.TrimSpace(res.Stderr))
			continue
		}
		report.Restored = append(report.Restored, p.Component)
	}
	report.FullRestore = len(report.Skipped) == 0 && len(report.Warnings) == 0 && len(report.Errors) == 0
	writeRestoreReport(dir, report)
	updateInventoryRestore(stateDir, report)
	return report, nil
}

func BuildExportPlans(inv *inventory.Inventory, dir string) []CommandPlan {
	if inv == nil {
		return []CommandPlan{
			{Component: "database", Node: "unknown", Command: "pg_dump --format=custom --file database.dump stackforge", Output: filepath.Join(dir, "database.dump")},
			{Component: "consul", Node: "unknown", Command: "consul snapshot save consul.snapshot", Output: filepath.Join(dir, "consul.snapshot")},
			{Component: "nomad", Node: "unknown", Command: "nomad job status -json && nomad job inspect -json", Output: filepath.Join(dir, "nomad-jobs.json")},
			{Component: "traefik", Node: "unknown", Command: "tar -C /etc -czf traefik-config.tgz traefik", Output: filepath.Join(dir, "traefik-config.tgz")},
			{Component: "stackforge-config", Node: "unknown", Command: "tar -C /etc -czf stackforge-config.tgz stackforge --exclude='*.env'", Output: filepath.Join(dir, "stackforge-config.tgz")},
		}
	}
	nodeForRole := func(role string) string {
		for _, n := range inv.Nodes {
			for _, r := range n.Roles {
				if r == role {
					return n.PrivateIP
				}
			}
		}
		return "unknown"
	}
	return []CommandPlan{
		{Component: "database", Node: nodeForRole("database"), Command: "sudo -u postgres pg_dump --format=custom --file /tmp/stackforge-database.dump stackforge && cat /tmp/stackforge-database.dump", Output: filepath.Join(dir, "database.dump")},
		{Component: "consul", Node: nodeForRole("consul-server"), Command: "consul snapshot save /tmp/stackforge-consul.snapshot && cat /tmp/stackforge-consul.snapshot", Output: filepath.Join(dir, "consul.snapshot")},
		{Component: "nomad", Node: nodeForRole("nomad-server"), Command: "nomad job status -json | jq -r '.[].ID' | while read j; do nomad job inspect -json \"$j\"; done", Output: filepath.Join(dir, "nomad-jobs.json")},
		{Component: "traefik", Node: nodeForRole("traefik"), Command: "tar -C /etc -czf - traefik 2>/dev/null", Output: filepath.Join(dir, "traefik-config.tgz")},
		{Component: "stackforge-config", Node: nodeForRole("control-plane"), Command: "tar -C /etc -czf - stackforge --exclude='stackforge.env' 2>/dev/null", Output: filepath.Join(dir, "stackforge-config.tgz")},
	}
}

func BuildRestorePlans(m *Manifest) []CommandPlan {
	nodeFor := func(component string) string {
		if m != nil {
			for _, p := range m.PlannedCommands {
				if p.Component == component {
					return p.Node
				}
			}
		}
		return "unknown"
	}
	return []CommandPlan{
		{Component: "database", Node: nodeFor("database"), Command: "pg_restore --clean --if-exists --dbname stackforge database.dump"},
		{Component: "consul", Node: nodeFor("consul"), Command: "consul snapshot restore consul.snapshot"},
		{Component: "nomad", Node: nodeFor("nomad"), Command: "nomad job run restored job definitions from nomad-jobs.json"},
		{Component: "traefik", Node: nodeFor("traefik"), Command: "restore /etc/traefik from traefik-config.tgz and restart traefik"},
		{Component: "stackforge-config", Node: nodeFor("stackforge-config"), Command: "restore /etc/stackforge non-secret config and restart stackforge-control-plane"},
	}
}

func restoreCommandWithPayload(dir string, p CommandPlan) (string, error) {
	file := filepath.Join(dir, filepath.Base(p.Output))
	if p.Output == "" {
		switch p.Component {
		case "database":
			file = filepath.Join(dir, "database.dump")
		case "consul":
			file = filepath.Join(dir, "consul.snapshot")
		case "nomad":
			file = filepath.Join(dir, "nomad-jobs.json")
		case "traefik":
			file = filepath.Join(dir, "traefik-config.tgz")
		case "stackforge-config":
			file = filepath.Join(dir, "stackforge-config.tgz")
		}
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(b)
	write := "tmp=$(mktemp); printf %s " + shellQuote(encoded) + " | base64 -d > \"$tmp\""
	switch p.Component {
	case "database":
		return write + " && sudo -u postgres pg_restore --clean --if-exists --dbname stackforge \"$tmp\" && rm -f \"$tmp\"", nil
	case "consul":
		return write + " && consul snapshot restore \"$tmp\" && rm -f \"$tmp\"", nil
	case "nomad":
		return write + " && jq -c '.' \"$tmp\" >/dev/null && echo 'Nomad restore requires operator review of exported job specs before nomad job run' >&2; exit 12", nil
	case "traefik":
		return write + " && " + "tar -C /etc -xzf \"$tmp\" && systemctl restart traefik && rm -f \"$tmp\"", nil
	case "stackforge-config":
		return write + " && " + "tar -C /etc -xzf \"$tmp\" && systemctl restart stackforge-control-plane && rm -f \"$tmp\"", nil
	default:
		return "", fmt.Errorf("unknown restore component %s", p.Component)
	}
}

func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	return &m, json.Unmarshal(b, &m)
}

func WriteManifest(dir string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "backup-manifest.json"), b, 0600)
}

func Verify(dir string, m *Manifest) error {
	for name, sum := range m.Checksums {
		path := filepath.Join(dir, filepath.Base(m.FilePaths[name]))
		got, err := checksum(path)
		if err != nil {
			return err
		}
		if got != sum {
			return fmt.Errorf("checksum mismatch for %s", name)
		}
	}
	return nil
}

func copyIfExists(src, dst string, m *Manifest) {
	in, err := os.Open(src)
	if err != nil {
		if m.Warnings != nil {
			m.Warnings = append(m.Warnings, "missing "+filepath.Base(src))
		}
		return
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		if m.Errors != nil {
			m.Errors = append(m.Errors, err.Error())
		}
		return
	}
	_, _ = io.Copy(out, in)
	_ = out.Close()
	addFile(m, dst)
}

func copyRedactedIfExists(src, dst string, m *Manifest) {
	b, err := os.ReadFile(src)
	if err != nil {
		if m.Warnings != nil {
			m.Warnings = append(m.Warnings, "missing "+filepath.Base(src))
		}
		return
	}
	lines := strings.Split(string(b), "\n")
	for i, line := range lines {
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			if strings.TrimSpace(parts[1]) != "" {
				lines[i] = parts[0] + ": [REDACTED]"
			}
		}
	}
	if err := os.WriteFile(dst, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		m.Errors = append(m.Errors, err.Error())
		return
	}
	addFile(m, dst)
}

func writeMarker(path, content string, m *Manifest) {
	_ = os.WriteFile(path, []byte(content), 0600)
	addFile(m, path)
}

func runExport(ctx context.Context, exec remoteexec.Executor, p CommandPlan, m *Manifest) error {
	if p.Node == "" || p.Node == "unknown" {
		return fmt.Errorf("%s backup skipped: no inventory node found", p.Component)
	}
	res, err := exec.Run(ctx, p.Node, remoteexec.Command{Command: p.Command, Sudo: true, Timeout: 10 * time.Minute})
	if err != nil {
		return fmt.Errorf("%s backup failed: %w", p.Component, err)
	}
	if err := os.WriteFile(p.Output, []byte(res.Stdout), 0600); err != nil {
		return err
	}
	return nil
}

func writeRestoreReport(dir string, report *RestoreReport) {
	b, _ := json.MarshalIndent(report, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "restore-report.json"), b, 0600)
}

func updateInventoryBackup(stateDir string, m *Manifest) {
	path := filepath.Join(stateDir, "inventory.yaml")
	inv, err := inventory.Load(path)
	if err != nil {
		return
	}
	inventory.MarkBackup(inv, m.BackupID, m.Warnings)
	_ = inventory.Save(path, inv)
}

func updateInventoryRestore(stateDir string, report *RestoreReport) {
	path := filepath.Join(stateDir, "inventory.yaml")
	inv, err := inventory.Load(path)
	if err != nil {
		return
	}
	inventory.MarkRestore(inv, report.BackupID, report.Warnings)
	_ = inventory.Save(path, inv)
}

func addFile(m *Manifest, path string) {
	if m.FilePaths == nil || m.Checksums == nil {
		return
	}
	name := filepath.Base(path)
	m.FilePaths[name] = path
	if sum, err := checksum(path); err == nil {
		m.Checksums[name] = sum
	}
}

func checksum(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
