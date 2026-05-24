package install

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"stackforge/internal/stackforge/config"
	"stackforge/internal/stackforge/firewall"
	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/remoteexec"
	"stackforge/internal/stackforge/rollback"
	"stackforge/internal/stackforge/safety"
	"stackforge/internal/stackforge/secrets"
)

type Status string

const (
	StatusPending Status = "pending"
	StatusSkipped Status = "skipped"
	StatusRunning Status = "running"
	StatusOK      Status = "ok"
	StatusFailed  Status = "failed"
	StatusDryRun  Status = "dry-run"
)

type Step struct {
	ID                string
	Name              string
	Node              string
	Role              string
	Requires          []string
	DryRunDescription string
	Check             func(context.Context) (bool, string, error)
	Apply             func(context.Context) error
	Verify            func(context.Context) error
	Rollback          string
	RollbackID        string
	ChangedFiles      []string
	BackupFiles       []string
	FailureRecovery   string
	IdempotencyKey    string
	Status            Status
	Error             string
}

type StepRecord struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Node              string    `json:"node"`
	Role              string    `json:"role"`
	Status            Status    `json:"status"`
	Error             string    `json:"error,omitempty"`
	At                time.Time `json:"at"`
	Rollback          string    `json:"rollback,omitempty"`
	RollbackID        string    `json:"rollback_id,omitempty"`
	ChangedFiles      []string  `json:"changed_files,omitempty"`
	BackupFiles       []string  `json:"backup_files,omitempty"`
	Recovery          string    `json:"failure_recovery,omitempty"`
	IdempotencyKey    string    `json:"idempotency_key,omitempty"`
	DryRunDescription string    `json:"dry_run_description,omitempty"`
}

type Report struct {
	Cluster           string       `json:"cluster"`
	StartedAt         time.Time    `json:"started_at"`
	CompletedAt       time.Time    `json:"completed_at"`
	FailedSteps       []StepRecord `json:"failed_steps"`
	Steps             []StepRecord `json:"steps"`
	SuggestedRecovery []string     `json:"suggested_recovery"`
	Warnings          []string     `json:"warnings"`
}

type Options struct {
	Config             *config.Config
	StateDir           string
	DryRun             bool
	Resume             bool
	AllowNoFirewall    bool
	AllowExampleConfig bool
	AllowPublicSSH     bool
	ConfirmProduction  bool
	Executor           remoteexec.Executor
	SecretSet          *secrets.Secrets
	SecretPath         string
}

func Run(ctx context.Context, opts Options) (*Report, error) {
	if opts.Config == nil {
		return nil, errors.New("config is required")
	}
	if err := config.Validate(opts.Config); err != nil {
		return nil, err
	}
	if !opts.DryRun {
		report := safety.Check(opts.Config, safety.Options{Live: true, Production: strings.EqualFold(opts.Config.Cluster.Environment, "production"), ConfirmProduction: opts.ConfirmProduction, AllowExampleConfig: opts.AllowExampleConfig, AllowPublicSSH: opts.AllowPublicSSH})
		if err := report.Error(); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Join(opts.StateDir, "logs"), 0700); err != nil {
		return nil, err
	}
	secPath := filepath.Join(opts.StateDir, "generated-secrets.yaml")
	sec, created, err := secrets.LoadOrGenerate(secPath, opts.Config.ControlPlane.AdminAPIKeys, opts.Config.Traefik.DashboardEnabled)
	if err != nil {
		return nil, err
	}
	opts.SecretSet = sec
	opts.SecretPath = secPath
	inv := initialInventory(opts.Config)
	if opts.Resume {
		if existing, err := inventory.Load(filepath.Join(opts.StateDir, "inventory.yaml")); err == nil {
			inv = existing
			inventory.Normalize(inv)
			inv.InstallStatus = "installing"
		}
	}
	if err := inventory.Save(filepath.Join(opts.StateDir, "inventory.yaml"), inv); err != nil {
		return nil, err
	}
	steps, warnings, err := Plan(opts)
	if err != nil {
		return nil, err
	}
	if created {
		warnings = append(warnings, "generated local secrets file; chmod 600 applied")
	}
	report := &Report{Cluster: opts.Config.Cluster.Name, StartedAt: time.Now().UTC(), Warnings: warnings}
	completed := map[string]bool{}
	if opts.Resume {
		if prior, err := LoadReport(filepath.Join(opts.StateDir, "install-report.json")); err == nil {
			for _, s := range prior.Steps {
				if s.Status == StatusOK || s.Status == StatusSkipped || s.Status == StatusDryRun {
					completed[s.ID] = true
				}
			}
		}
	}
	for i := range steps {
		step := &steps[i]
		if completed[step.ID] {
			step.Status = StatusSkipped
			report.record(*step)
			inventory.MarkStepSuccess(inv, step.ID)
			_ = inventory.Save(filepath.Join(opts.StateDir, "inventory.yaml"), inv)
			continue
		}
		if opts.DryRun {
			step.Status = StatusDryRun
			report.record(*step)
			inventory.MarkStepSuccess(inv, step.ID)
			_ = inventory.Save(filepath.Join(opts.StateDir, "inventory.yaml"), inv)
			continue
		}
		ok, _, err := step.Check(ctx)
		if err != nil {
			step.Status = StatusFailed
			step.Error = err.Error()
			report.record(*step)
			report.fail(*step)
			inventory.MarkStepFailure(inv, step.ID, err.Error())
			_ = inventory.Save(filepath.Join(opts.StateDir, "inventory.yaml"), inv)
			writeReports(opts.StateDir, report)
			return report, err
		}
		if ok {
			if step.Verify != nil {
				if err := step.Verify(ctx); err != nil {
					step.Status = StatusFailed
					step.Error = err.Error()
					report.record(*step)
					report.fail(*step)
					inventory.MarkStepFailure(inv, step.ID, err.Error())
					_ = inventory.Save(filepath.Join(opts.StateDir, "inventory.yaml"), inv)
					writeReports(opts.StateDir, report)
					return report, err
				}
			}
			step.Status = StatusSkipped
			report.record(*step)
			observeStep(inv, *step)
			inventory.MarkStepSuccess(inv, step.ID)
			_ = inventory.Save(filepath.Join(opts.StateDir, "inventory.yaml"), inv)
			continue
		}
		step.Status = StatusRunning
		if step.RollbackID != "" {
			_ = rollback.Save(opts.StateDir, rollback.Record{
				ID:                 step.RollbackID,
				Node:               step.Node,
				NodeAddress:        nodeAddress(opts.Config, step.Node),
				Component:          step.Role,
				ChangedFiles:       step.ChangedFiles,
				BackupFiles:        step.BackupFiles,
				RestoreCommand:     rollbackCommandForStep(*step),
				ManualInstructions: step.Rollback,
				Reason:             "pre-change backup before " + step.Name,
				Status:             "available",
				SafeAutomatic:      step.Role != "firewall" && step.Role != "database",
			})
		}
		if err := step.Apply(ctx); err != nil {
			step.Status = StatusFailed
			step.Error = err.Error()
			report.record(*step)
			report.fail(*step)
			inventory.MarkStepFailure(inv, step.ID, err.Error())
			_ = inventory.Save(filepath.Join(opts.StateDir, "inventory.yaml"), inv)
			writeReports(opts.StateDir, report)
			return report, err
		}
		if step.Verify != nil {
			if err := step.Verify(ctx); err != nil {
				step.Status = StatusFailed
				step.Error = err.Error()
				report.record(*step)
				report.fail(*step)
				inventory.MarkStepFailure(inv, step.ID, err.Error())
				_ = inventory.Save(filepath.Join(opts.StateDir, "inventory.yaml"), inv)
				writeReports(opts.StateDir, report)
				return report, err
			}
		}
		step.Status = StatusOK
		report.record(*step)
		observeStep(inv, *step)
		inventory.MarkStepSuccess(inv, step.ID)
		_ = inventory.Save(filepath.Join(opts.StateDir, "inventory.yaml"), inv)
		writeReports(opts.StateDir, report)
	}
	inv.InstallStatus = "installed"
	inventory.Refresh(ctx, inv, opts.Executor)
	_ = inventory.Save(filepath.Join(opts.StateDir, "inventory.yaml"), inv)
	report.CompletedAt = time.Now().UTC()
	writeReports(opts.StateDir, report)
	return report, nil
}

func Plan(opts Options) ([]Step, []string, error) {
	cfg := opts.Config
	fw, err := firewall.BuildPlanWithOptions(cfg, firewall.Options{AllowPublicSSH: opts.AllowPublicSSH})
	if err != nil && !opts.AllowNoFirewall {
		return nil, nil, err
	}
	warnings := []string{}
	if err != nil && opts.AllowNoFirewall {
		warnings = append(warnings, "firewall validation bypassed by --allow-no-firewall")
	}
	var steps []Step
	for _, n := range cfg.Nodes {
		node := n
		addr := node.Address
		run := func(command string, sudo bool, secretValues ...string) func(context.Context) error {
			return func(ctx context.Context) error {
				if opts.Executor == nil {
					return errors.New("remote executor is required for live install")
				}
				_, err := opts.Executor.Run(ctx, addr, remoteexec.Command{Command: command, Sudo: sudo, Timeout: 5 * time.Minute, Secrets: secretValues})
				return err
			}
		}
		check := func(command string, sudo ...bool) func(context.Context) (bool, string, error) {
			return func(ctx context.Context) (bool, string, error) {
				if opts.Executor == nil {
					return false, "", errors.New("remote executor is required for live install")
				}
				cmd := remoteexec.Command{Command: command, Timeout: 30 * time.Second}
				if len(sudo) > 0 {
					cmd.Sudo = sudo[0]
				}
				res, err := opts.Executor.Run(ctx, addr, cmd)
				return err == nil && res.ExitCode == 0, strings.TrimSpace(res.Stdout), nil
			}
		}
		verify := func(command string, sudo ...bool) func(context.Context) error {
			return func(ctx context.Context) error {
				if opts.Executor == nil {
					return errors.New("remote executor is required for live install")
				}
				cmd := remoteexec.Command{Command: command, Timeout: 45 * time.Second}
				if len(sudo) > 0 {
					cmd.Sudo = sudo[0]
				}
				res, err := opts.Executor.Run(ctx, addr, cmd)
				if err != nil {
					return fmt.Errorf("%s: %s", err, strings.TrimSpace(res.Stderr))
				}
				return nil
			}
		}
		steps = append(steps,
			Step{ID: node.Name + ":ssh", Name: "Validate SSH connectivity", Node: node.Name, DryRunDescription: "connect via SSH and run true", Check: check("true"), Apply: run("true", false), Verify: verify("true"), Rollback: "Fix SSH credentials/network, then run stackforge install --resume.", FailureRecovery: "Confirm the node address, SSH user, port, and key path.", IdempotencyKey: node.Name + "/ssh"},
			Step{ID: node.Name + ":os", Name: "Detect supported OS", Node: node.Name, DryRunDescription: "read /etc/os-release and reject unsupported Debian/Ubuntu releases", Check: check("test -r /etc/os-release && . /etc/os-release && case \"$ID:$VERSION_ID\" in debian:12*|debian:13*|ubuntu:22.04|ubuntu:24.04|ubuntu:26.04) exit 0;; *) exit 1;; esac"), Apply: run("grep -E '^(ID|VERSION_ID|PRETTY_NAME)=' /etc/os-release", false), Verify: verify("test -r /etc/os-release && . /etc/os-release && case \"$ID:$VERSION_ID\" in debian:12*|debian:13*|ubuntu:22.04|ubuntu:24.04|ubuntu:26.04) exit 0;; *) exit 1;; esac"), Rollback: "Install Debian 12+ or Ubuntu 22.04/24.04/26.04 before resuming.", FailureRecovery: "Use a supported Debian/Ubuntu node or update the config to target a supported node.", IdempotencyKey: node.Name + "/os"},
			Step{ID: node.Name + ":base-packages", Name: "Install base packages", Node: node.Name, DryRunDescription: "install only missing base packages: curl wget unzip ca-certificates gnupg lsb-release jq ufw systemd openssl iproute2 tar gzip", Check: check("for p in curl wget unzip ca-certificates gnupg lsb-release jq ufw systemd openssl iproute2 tar gzip; do dpkg -s \"$p\" >/dev/null 2>&1 || exit 1; done"), Apply: run(basePackagesCommand(), true), Verify: verify("command -v curl >/dev/null && command -v jq >/dev/null && command -v ufw >/dev/null && command -v systemctl >/dev/null"), Rollback: "Base packages are non-destructive; fix apt errors and resume.", FailureRecovery: "Inspect apt locks and package repository errors on the node.", IdempotencyKey: node.Name + "/base-packages"},
		)
		if !opts.AllowNoFirewall {
			cmds := strings.Join(firewall.UFWCommands(fw), " && ")
			steps = append(steps, Step{ID: node.Name + ":firewall", Name: "Configure firewall", Node: node.Name, Role: "firewall", RollbackID: rollback.NewID(node.Name, "firewall"), DryRunDescription: firewallDryRun(fw), Check: check(firewall.UFWDetectCommand(), true), Apply: run("command -v ufw >/dev/null || { echo 'ufw is required unless --allow-no-firewall is set' >&2; exit 1; }; "+firewall.BackupCommand()+" && "+cmds, true), Verify: verify(firewall.VerifyUFWCommand(), true), Rollback: "Firewall rollback is not applied automatically because it can cut off SSH. Use console access and restore the saved ufw status from /var/lib/stackforge/firewall before retrying.", FailureRecovery: "Use console access if SSH is blocked, then rerun stackforge install --resume.", IdempotencyKey: node.Name + "/firewall", ChangedFiles: []string{"/etc/ufw"}, BackupFiles: []string{"/var/lib/stackforge/firewall/ufw-before-<timestamp>.txt"}})
		}
		componentSeen := map[string]bool{}
		for _, role := range node.Roles {
			switch role {
			case "consul-server":
				if !componentSeen["consul"] {
					steps = append(steps, consulStep(opts, node.Name, addr, role))
					componentSeen["consul"] = true
				}
			case "nomad-server", "nomad-client":
				if !componentSeen["nomad"] {
					steps = append(steps, nomadStep(opts, node.Name, addr, role))
					componentSeen["nomad"] = true
				}
			case "traefik":
				if !componentSeen["traefik"] {
					steps = append(steps, traefikStep(opts, node.Name, addr))
					componentSeen["traefik"] = true
				}
			case "database":
				if !componentSeen[cfg.Database.Engine] {
					if cfg.Database.Engine != "postgres" {
						return nil, nil, fmt.Errorf("live install supports PostgreSQL only; database.engine=%s is not implemented", cfg.Database.Engine)
					}
					steps = append(steps, postgresStep(opts, node.Name, addr))
					componentSeen[cfg.Database.Engine] = true
				}
			case "control-plane":
				steps = append(steps, controlPlaneStep(opts, node.Name, addr))
			}
		}
	}
	steps = append(steps, Step{ID: "local:reports", Name: "Generate install reports", Node: "local", DryRunDescription: "write install-report.json and STACKFORGE_INSTALL_REPORT.md", Check: func(context.Context) (bool, string, error) { return false, "", nil }, Apply: func(context.Context) error { return nil }, Verify: func(context.Context) error { return nil }, Rollback: "Reports can be regenerated by rerunning install --resume.", FailureRecovery: "Rerun stackforge install --resume to regenerate reports.", IdempotencyKey: "local/reports"})
	return steps, warnings, nil
}

func initialInventory(cfg *config.Config) *inventory.Inventory {
	inv := &inventory.Inventory{ClusterName: cfg.Cluster.Name, Environment: cfg.Cluster.Environment, Datacenter: cfg.Cluster.Datacenter, InstallStatus: "installing", FirewallMode: "ufw", ComponentVersions: map[string]string{}, ServiceStatus: map[string]string{}}
	for _, n := range cfg.Nodes {
		inv.Nodes = append(inv.Nodes, inventory.Node{Name: n.Name, Roles: n.Roles, PrivateIP: n.Address, PublicIP: n.PublicAddress, SSH: inventory.SSHInfo{User: cfg.SSH.User, Port: cfg.SSH.Port, PrivateKeyPath: cfg.SSH.PrivateKeyPath}, Components: map[string]string{}, Services: map[string]string{}})
		for _, role := range n.Roles {
			switch role {
			case "consul-server":
				inv.ConsulEndpoints = append(inv.ConsulEndpoints, "http://"+n.Address+":8500")
			case "nomad-server":
				inv.NomadEndpoints = append(inv.NomadEndpoints, "http://"+n.Address+":4646")
			case "traefik":
				inv.TraefikEndpoints = append(inv.TraefikEndpoints, "http://"+n.PublicAddress)
			case "database":
				inv.DatabaseEndpoint = n.Address
			case "control-plane":
				inv.ControlPlaneEndpoint = fmt.Sprintf("http://%s:%d", n.Address, cfg.ControlPlane.APIPort)
			}
		}
	}
	return inv
}

func consulStep(opts Options, node, addr, role string) Step {
	token := ""
	if opts.SecretSet != nil {
		token = opts.SecretSet.ConsulBootstrapToken
	}
	verify := "systemctl is-active --quiet consul && consul version >/dev/null && curl -fsS http://127.0.0.1:8500/v1/status/leader >/dev/null && curl -fsS http://127.0.0.1:8500/v1/status/peers >/dev/null"
	if opts.Config.Consul.ACLEnabled {
		verify += " && curl -fsS -H " + shellQuote("X-Consul-Token: "+token) + " http://127.0.0.1:8500/v1/acl/token/self >/dev/null"
	}
	return serviceStep(opts, node, addr, "consul", role, "Consul", "consul version", installConsulCommand(opts.Config, opts.SecretSet), verify, "Restore the most recent /etc/consul.d backup, then restart consul.", "Inspect journalctl -u consul and /etc/consul.d before resuming.")
}

func nomadStep(opts Options, node, addr, role string) Step {
	token := ""
	if opts.SecretSet != nil {
		token = opts.SecretSet.NomadBootstrapToken
	}
	verify := "systemctl is-active --quiet nomad && nomad version >/dev/null && curl -fsS http://127.0.0.1:4646/v1/status/leader >/dev/null"
	if role == "nomad-client" || opts.Config.Nomad.ClientEnabled {
		verify += " && curl -fsS http://127.0.0.1:4646/v1/nodes >/dev/null"
	}
	if opts.Config.Nomad.ACLEnabled && token != "" {
		verify += " && NOMAD_TOKEN=" + shellQuote(token) + " nomad acl token self >/dev/null"
	}
	return serviceStep(opts, node, addr, "nomad", role, "Nomad", "nomad version", installNomadCommand(opts.Config, node, role, opts.SecretSet), verify, "Restore the most recent /etc/nomad.d backup, then restart nomad.", "Inspect journalctl -u nomad and /etc/nomad.d before resuming.")
}

func traefikStep(opts Options, node, addr string) Step {
	dashboardGuard := "true"
	if opts.Config.Traefik.DashboardEnabled {
		dashboardGuard = "test " + shellQuote(fmt.Sprint(opts.Config.Traefik.DashboardBasicAuth)) + " = 'true'"
	}
	return serviceStep(opts, node, addr, "traefik", "traefik", "Traefik", "traefik version", installTraefikCommand(opts.Config), "systemctl is-active --quiet traefik && ss -ltn '( sport = :80 or sport = :443 )' | grep -Eq ':80|:443' && "+dashboardGuard, "Restore the most recent /etc/traefik backup, then restart traefik.", "Inspect journalctl -u traefik and validate dashboard exposure before resuming.")
}

func postgresStep(opts Options, node, addr string) Step {
	return serviceStep(opts, node, addr, "postgres", "database", "PostgreSQL", "systemctl is-active --quiet postgresql && sudo -u postgres psql -tAc \"SELECT 1 FROM pg_database WHERE datname='stackforge'\" | grep -q 1", installPostgresCommand(opts.SecretSet), "systemctl is-active --quiet postgresql && sudo -u postgres psql -d stackforge -tAc 'SELECT 1' | grep -q 1 && ! ss -ltnp | grep -E ':5432 .*0\\.0\\.0\\.0|:5432 .*:::'", "Do not drop existing databases. Restore PostgreSQL config backup if bind settings were changed.", "Inspect journalctl -u postgresql and pg_hba/postgresql.conf; rerun after fixing connectivity.")
}

func controlPlaneStep(opts Options, node, addr string) Step {
	env := ""
	secretValues := []string{}
	if opts.SecretSet != nil {
		env = opts.SecretSet.Env("postgres://stackforge:" + opts.SecretSet.DatabasePassword + "@127.0.0.1:5432/stackforge?sslmode=disable")
		secretValues = opts.SecretSet.Values()
	}
	checkCommand := "systemctl list-unit-files stackforge-control-plane.service | grep -q stackforge-control-plane && systemctl is-active --quiet stackforge-control-plane"
	applyCommand := installControlPlaneCommand(env)
	verifyCommand := "systemctl is-active --quiet stackforge-control-plane && curl -fsS http://127.0.0.1:" + fmt.Sprint(opts.Config.ControlPlane.APIPort) + "/health >/dev/null && test \"$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:" + fmt.Sprint(opts.Config.ControlPlane.APIPort) + "/api/v1/domains)\" = '401'"
	return remoteStep(opts, node, addr, Step{ID: node + ":stackforge-control-plane", Name: "Install StackForge control plane systemd service", Node: node, Role: "control-plane", DryRunDescription: "deploy /etc/stackforge/stackforge.env, install stackforge-control-plane.service, start service, verify /health and authenticated /api/v1", IdempotencyKey: node + "/stackforge-control-plane", Rollback: "Disable stackforge-control-plane.service and restore /etc/stackforge backups.", FailureRecovery: "Inspect journalctl -u stackforge-control-plane and /etc/stackforge/stackforge.env permissions.", ChangedFiles: []string{"/usr/local/bin/stackforge", "/etc/stackforge/stackforge.env", "/etc/systemd/system/stackforge-control-plane.service"}, BackupFiles: []string{"/etc/stackforge.stackforge.<timestamp>.tgz", "/usr/local/bin/stackforge.stackforge.<timestamp>.bak"}}, checkCommand, applyCommand, verifyCommand, secretValues...)
}

func serviceStep(opts Options, node, addr, component, role, display, checkCommand, applyCommand, verifyCommand, rollback, recovery string) Step {
	changed, backups := filesForComponent(component)
	var secretValues []string
	if opts.SecretSet != nil {
		secretValues = opts.SecretSet.Values()
	}
	return remoteStep(opts, node, addr, Step{ID: node + ":" + component, Name: "Install " + display, Node: node, Role: role, DryRunDescription: "install/configure/start/verify " + display, IdempotencyKey: node + "/" + component, Rollback: rollback, FailureRecovery: recovery, ChangedFiles: changed, BackupFiles: backups}, checkCommand, applyCommand, verifyCommand, secretValues...)
}

func filesForComponent(component string) ([]string, []string) {
	switch component {
	case "consul":
		return []string{"/etc/consul.d/stackforge.hcl"}, []string{"/etc/consul.d.stackforge.<timestamp>.tgz"}
	case "nomad":
		return []string{"/etc/nomad.d/stackforge.hcl"}, []string{"/etc/nomad.d.stackforge.<timestamp>.tgz"}
	case "traefik":
		return []string{"/etc/traefik/traefik.yaml", "/etc/systemd/system/traefik.service"}, []string{"/etc/traefik.stackforge.<timestamp>.tgz"}
	case "postgres":
		return []string{"/etc/postgresql/*/main/postgresql.conf"}, []string{"/etc/postgresql/*.stackforge.<timestamp>.tgz"}
	default:
		return nil, nil
	}
}

func remoteStep(opts Options, node, addr string, step Step, checkCommand, applyCommand, verifyCommand string, secretValues ...string) Step {
	if step.RollbackID == "" && step.Role != "" && step.Node != "local" {
		step.RollbackID = rollback.NewID(node, strings.ReplaceAll(step.Role, "/", "-"))
	}
	step.Check = func(ctx context.Context) (bool, string, error) {
		if opts.Executor == nil {
			return false, "", errors.New("remote executor is required for live install")
		}
		res, err := opts.Executor.Run(ctx, addr, remoteexec.Command{Command: checkCommand, Sudo: true, Timeout: 45 * time.Second, Secrets: secretValues})
		return err == nil && res.ExitCode == 0, strings.TrimSpace(res.Stdout), nil
	}
	step.Apply = func(ctx context.Context) error {
		if opts.Executor == nil {
			return errors.New("remote executor is required for live install")
		}
		res, err := opts.Executor.Run(ctx, addr, remoteexec.Command{Command: applyCommand, Sudo: true, Timeout: 10 * time.Minute, Secrets: secretValues})
		if err == nil && opts.SecretSet != nil && opts.SecretPath != "" {
			if changed := updateBootstrapSecrets(opts.SecretSet, res.Stdout); changed {
				_ = secrets.Save(opts.SecretPath, opts.SecretSet)
			}
		}
		return err
	}
	step.Verify = func(ctx context.Context) error {
		if opts.Executor == nil {
			return errors.New("remote executor is required for live install")
		}
		res, err := opts.Executor.Run(ctx, addr, remoteexec.Command{Command: verifyCommand, Sudo: true, Timeout: 90 * time.Second, Secrets: secretValues})
		if err != nil {
			return fmt.Errorf("%s: %s", err, strings.TrimSpace(res.Stderr))
		}
		return nil
	}
	return step
}

func updateBootstrapSecrets(sec *secrets.Secrets, stdout string) bool {
	changed := false
	for _, line := range strings.Split(stdout, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || value == "" {
			continue
		}
		switch key {
		case "STACKFORGE_CONSUL_BOOTSTRAP_TOKEN":
			sec.ConsulBootstrapToken = value
			changed = true
		case "STACKFORGE_NOMAD_BOOTSTRAP_TOKEN":
			sec.NomadBootstrapToken = value
			changed = true
		}
	}
	return changed
}

func basePackagesCommand() string {
	pkgs := "curl wget unzip ca-certificates gnupg lsb-release jq ufw systemd openssl iproute2 tar gzip apt-transport-https"
	return "missing=''; for p in " + pkgs + "; do dpkg -s \"$p\" >/dev/null 2>&1 || missing=\"$missing $p\"; done; if [ -n \"$missing\" ]; then apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y $missing; fi"
}

func installConsulCommand(cfg *config.Config, sec *secrets.Secrets) string {
	token := ""
	gossip := ""
	if sec != nil {
		token = sec.ConsulBootstrapToken
		gossip = sec.ConsulGossipKey
	}
	encryptLine := ""
	if cfg.Consul.EncryptGossip && gossip != "" {
		encryptLine = fmt.Sprintf("encrypt = %q\n", gossip)
	}
	tokenBlock := ""
	if cfg.Consul.ACLEnabled && token != "" {
		tokenBlock = fmt.Sprintf(`tokens {
  initial_management = %q
}
`, token)
	}
	configBody := fmt.Sprintf(`datacenter = %q
data_dir = "/opt/consul"
server = true
bootstrap_expect = %d
client_addr = "127.0.0.1"
bind_addr = "{{ GetPrivateIP }}"
%s
ui_config { enabled = %t }
acl {
  enabled = %t
  default_policy = "deny"
  enable_token_persistence = true
}
%s`, cfg.Cluster.Datacenter, countRole(cfg, "consul-server"), encryptLine, cfg.Consul.UIEnabled, cfg.Consul.ACLEnabled, tokenBlock)
	cmd := "set -e; " + hashicorpRepoCommand() + " && DEBIAN_FRONTEND=noninteractive apt-get install -y consul && install -d -m 0750 /etc/consul.d /opt/consul && " + backupDirCommand("/etc/consul.d") + " && " + writeBase64Command("/etc/consul.d/stackforge.hcl", configBody, "0640") + " && { chown -R consul:consul /opt/consul /etc/consul.d || true; } && systemctl enable consul && systemctl restart consul && " + waitHTTPCommand("http://127.0.0.1:8500/v1/status/leader")
	if cfg.Consul.ACLEnabled && token != "" {
		cmd += " && curl -fsS -H " + shellQuote("X-Consul-Token: "+token) + " http://127.0.0.1:8500/v1/acl/token/self >/dev/null && echo STACKFORGE_CONSUL_BOOTSTRAP_TOKEN=" + shellQuote(token)
	}
	return cmd
}

func installNomadCommand(cfg *config.Config, node, role string, sec *secrets.Secrets) string {
	serverEnabled := role == "nomad-server"
	clientEnabled := role == "nomad-client" || cfg.Nomad.ClientEnabled
	token := ""
	if sec != nil {
		token = sec.NomadBootstrapToken
	}
	configBody := fmt.Sprintf(`datacenter = %q
data_dir = "/opt/nomad"
bind_addr = "0.0.0.0"
server {
  enabled = %t
  bootstrap_expect = %d
}
client {
  enabled = %t
}
acl {
  enabled = %t
}
`, cfg.Cluster.Datacenter, serverEnabled, countRole(cfg, "nomad-server"), clientEnabled, cfg.Nomad.ACLEnabled)
	cmd := "set -e; " + hashicorpRepoCommand() + " && DEBIAN_FRONTEND=noninteractive apt-get install -y nomad && install -d -m 0750 /etc/nomad.d /opt/nomad && " + backupDirCommand("/etc/nomad.d") + " && " + writeBase64Command("/etc/nomad.d/stackforge.hcl", configBody, "0640") + " && { chown -R nomad:nomad /opt/nomad /etc/nomad.d || true; } && systemctl enable nomad && systemctl restart nomad && " + waitHTTPCommand("http://127.0.0.1:4646/v1/status/leader")
	if cfg.Nomad.ACLEnabled && serverEnabled {
		cmd += " && if NOMAD_TOKEN=" + shellQuote(token) + " nomad acl token self >/dev/null 2>&1; then echo STACKFORGE_NOMAD_BOOTSTRAP_TOKEN=" + shellQuote(token) + "; else err=$(mktemp); if out=$(nomad acl bootstrap -json 2>\"$err\"); then boot=$(printf '%s' \"$out\" | jq -r '.SecretID'); test -n \"$boot\" && echo STACKFORGE_NOMAD_BOOTSTRAP_TOKEN=$boot; elif grep -qi bootstrapped \"$err\"; then echo 'Nomad ACLs are already bootstrapped but the stored StackForge token is invalid or missing' >&2; exit 15; else cat \"$err\" >&2; exit 1; fi; rm -f \"$err\"; fi"
	}
	return cmd
}

func installTraefikCommand(cfg *config.Config) string {
	if cfg.Traefik.DashboardEnabled && !cfg.Traefik.DashboardBasicAuth {
		return "echo 'Traefik dashboard would be public without protection' >&2; exit 1"
	}
	dashboard := "false"
	insecure := "false"
	if cfg.Traefik.DashboardEnabled {
		dashboard = "true"
	}
	acme := ""
	if cfg.Traefik.CertResolver != "" && cfg.Traefik.Email != "" {
		acme = fmt.Sprintf(`certificatesResolvers:
  %s:
    acme:
      email: %q
      storage: /var/lib/traefik/acme.json
      httpChallenge:
        entryPoint: web
`, cfg.Traefik.CertResolver, cfg.Traefik.Email)
	}
	configBody := `entryPoints:
  web:
    address: ":80"
  websecure:
    address: ":443"
providers:
  file:
    directory: /etc/traefik/dynamic
    watch: true
api:
  dashboard: ` + dashboard + `
  insecure: ` + insecure + `
` + acme
	dynamicBody := "http:\n  routers: {}\n  middlewares: {}\n"
	unit := `[Unit]
Description=Traefik
After=network-online.target

[Service]
ExecStart=/usr/local/bin/traefik --configFile=/etc/traefik/traefik.yaml
Restart=on-failure
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
`
	return "set -e; " + installTraefikBinaryCommand(cfg.Traefik.Version) + " && install -d -m 0755 /etc/traefik/dynamic && install -d -m 0700 /var/lib/traefik && touch /var/lib/traefik/acme.json && chmod 0600 /var/lib/traefik/acme.json && " + backupDirCommand("/etc/traefik") + " && " + writeBase64Command("/etc/traefik/traefik.yaml", configBody, "0644") + " && " + writeBase64Command("/etc/traefik/dynamic/stackforge.yaml", dynamicBody, "0644") + " && " + writeBase64Command("/etc/systemd/system/traefik.service", unit, "0644") + " && systemctl daemon-reload && systemctl enable traefik && systemctl restart traefik"
}

func installPostgresCommand(sec *secrets.Secrets) string {
	password := ""
	if sec != nil {
		password = sec.DatabasePassword
	}
	escapedPassword := strings.ReplaceAll(password, "'", "''")
	migration := `CREATE TABLE IF NOT EXISTS stackforge_schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMP NOT NULL DEFAULT now());
INSERT INTO stackforge_schema_migrations(version) VALUES ('001_stackforge_control_plane') ON CONFLICT DO NOTHING;`
	return "set -e; DEBIAN_FRONTEND=noninteractive apt-get install -y postgresql postgresql-client && systemctl enable postgresql && systemctl start postgresql && " +
		"if ! sudo -u postgres psql -tAc \"SELECT 1 FROM pg_roles WHERE rolname='stackforge'\" | grep -q 1; then sudo -u postgres psql -c \"CREATE ROLE stackforge LOGIN PASSWORD '" + escapedPassword + "'\"; fi && " +
		"if ! sudo -u postgres psql -tAc \"SELECT 1 FROM pg_database WHERE datname='stackforge'\" | grep -q 1; then sudo -u postgres createdb -O stackforge stackforge; fi && " +
		backupDirCommand("/etc/postgresql") + " && find /etc/postgresql -name postgresql.conf -exec sh -c \"cp -n '$1' '$1.stackforge.bak' && sed -i \\\"s/^#*listen_addresses.*/listen_addresses = '127.0.0.1'/\\\" '$1'\" sh {} \\; && systemctl restart postgresql && " +
		"printf %s " + shellQuote(base64.StdEncoding.EncodeToString([]byte(migration))) + " | base64 -d | sudo -u postgres psql -d stackforge"
}

func installControlPlaneCommand(env string) string {
	envCmd := "install -d -m 0750 /etc/stackforge /var/lib/stackforge && touch /etc/stackforge/stackforge.env && chmod 0600 /etc/stackforge/stackforge.env"
	if env != "" {
		envCmd = secrets.DeployEnvCommand(env, nil).Command
	}
	unit := `[Unit]
Description=StackForge control plane
After=network-online.target postgresql.service

[Service]
EnvironmentFile=/etc/stackforge/stackforge.env
ExecStart=/usr/local/bin/stackforge serve
Restart=on-failure
User=root

[Install]
WantedBy=multi-user.target
`
	return "set -e; " + backupDirCommand("/etc/stackforge") + " && " + installStackForgeBinaryCommand() + " && " + envCmd + " && " + writeBase64Command("/etc/systemd/system/stackforge-control-plane.service", unit, "0644") + " && systemctl daemon-reload && systemctl enable stackforge-control-plane && systemctl restart stackforge-control-plane && " + secrets.VerifyRemoteEnvPermissionsCommand().Command
}

func hashicorpRepoCommand() string {
	return "install -m 0755 -d /etc/apt/keyrings && curl -fsSL https://apt.releases.hashicorp.com/gpg | gpg --dearmor -o /etc/apt/keyrings/hashicorp-archive-keyring.gpg.tmp && mv /etc/apt/keyrings/hashicorp-archive-keyring.gpg.tmp /etc/apt/keyrings/hashicorp-archive-keyring.gpg && chmod 0644 /etc/apt/keyrings/hashicorp-archive-keyring.gpg && . /etc/os-release && echo \"deb [signed-by=/etc/apt/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $VERSION_CODENAME main\" > /etc/apt/sources.list.d/hashicorp.list && apt-get update"
}

func installTraefikBinaryCommand(version string) string {
	if version == "" || version == "latest-stable" {
		version = "v3.3.3"
	}
	version = strings.TrimPrefix(version, "traefik-")
	return "if command -v traefik >/dev/null 2>&1; then install -m 0755 $(command -v traefik) /usr/local/bin/traefik; else arch=$(dpkg --print-architecture); case \"$arch\" in amd64) ta=amd64;; arm64) ta=arm64;; *) echo \"unsupported Traefik architecture $arch\" >&2; exit 1;; esac; tmp=$(mktemp -d); curl -fsSL -o \"$tmp/traefik.tgz\" https://github.com/traefik/traefik/releases/download/" + shellQuote(version) + "/traefik_" + shellQuote(strings.TrimPrefix(version, "v")) + "_linux_${ta}.tar.gz; tar -C \"$tmp\" -xzf \"$tmp/traefik.tgz\" traefik; install -m 0755 \"$tmp/traefik\" /usr/local/bin/traefik; rm -rf \"$tmp\"; fi; /usr/local/bin/traefik version >/dev/null"
}

func installStackForgeBinaryCommand() string {
	exe, err := os.Executable()
	if err == nil && filepath.Base(exe) == "stackforge" {
		if b, readErr := os.ReadFile(exe); readErr == nil {
			encoded := base64.StdEncoding.EncodeToString(b)
			return "if [ -x /usr/local/bin/stackforge ]; then cp /usr/local/bin/stackforge /usr/local/bin/stackforge.stackforge.$(date -u +%Y%m%dT%H%M%SZ).bak; fi && printf %s " + shellQuote(encoded) + " | base64 -d > /usr/local/bin/stackforge && chmod 0755 /usr/local/bin/stackforge"
		}
	}
	return "command -v stackforge >/dev/null 2>&1 && install -m 0755 $(command -v stackforge) /usr/local/bin/stackforge || test -x /usr/local/bin/stackforge || { echo 'run install from a built stackforge binary or preinstall /usr/local/bin/stackforge on the target' >&2; exit 1; }"
}

func waitHTTPCommand(url string) string {
	return "ok=0; for i in $(seq 1 30); do if curl -fsS " + shellQuote(url) + " >/dev/null; then ok=1; break; fi; sleep 2; done; test \"$ok\" = 1"
}

func backupDirCommand(dir string) string {
	return "if [ -d " + shellQuote(dir) + " ]; then tar -C " + shellQuote(filepath.Dir(dir)) + " -czf " + shellQuote(dir+".stackforge.$(date -u +%Y%m%dT%H%M%SZ).tgz") + " " + shellQuote(filepath.Base(dir)) + "; fi"
}

func writeBase64Command(path, body, mode string) string {
	return "printf %s " + shellQuote(base64.StdEncoding.EncodeToString([]byte(body))) + " | base64 -d > " + shellQuote(path) + " && chmod " + shellQuote(mode) + " " + shellQuote(path)
}

func firewallDryRun(plan firewall.Plan) string {
	var lines []string
	lines = append(lines, "apply firewall mode "+plan.Mode+" with rules:")
	for _, r := range plan.Rules {
		lines = append(lines, fmt.Sprintf("%s %s/%d from %s exposure=%s reason=%s", r.Node, r.Protocol, r.Port, r.Source, r.Exposure, r.Purpose))
	}
	return strings.Join(lines, "\n")
}

func observeStep(inv *inventory.Inventory, s Step) {
	inventory.Normalize(inv)
	if s.Node != "" && s.Node != "local" {
		for i := range inv.Nodes {
			if inv.Nodes[i].Name != s.Node {
				continue
			}
			if s.Role != "" {
				inv.Nodes[i].Services[s.Role] = "verified"
				inv.ServiceStatus[s.Node+"/"+s.Role] = "verified"
			}
			switch s.Role {
			case "consul-server":
				inv.Nodes[i].Components["consul"] = "installed"
				inv.ComponentVersions["consul"] = "installed"
			case "nomad-server", "nomad-client":
				inv.Nodes[i].Components["nomad"] = "installed"
				inv.ComponentVersions["nomad"] = "installed"
			case "traefik":
				inv.Nodes[i].Components["traefik"] = "installed"
				inv.ComponentVersions["traefik"] = "installed"
			case "database":
				inv.Nodes[i].Components["postgres"] = "installed"
				inv.ComponentVersions["postgres"] = "installed"
			case "control-plane":
				inv.Nodes[i].Components["stackforge-control-plane"] = "installed"
				inv.ComponentVersions["stackforge-control-plane"] = "installed"
			}
		}
	}
}

func countRole(cfg *config.Config, role string) int {
	n := 0
	for _, node := range cfg.Nodes {
		for _, r := range node.Roles {
			if r == role {
				n++
			}
		}
	}
	if n == 0 {
		return 1
	}
	return n
}

func nodeAddress(cfg *config.Config, name string) string {
	if cfg == nil {
		return ""
	}
	for _, n := range cfg.Nodes {
		if n.Name == name {
			return n.Address
		}
	}
	return ""
}

func rollbackCommandForStep(step Step) string {
	switch step.Role {
	case "consul-server":
		return "set -e; latest=$(ls -1t /etc/consul.d.stackforge.*.tgz 2>/dev/null | head -n1); test -n \"$latest\"; tar -C /etc -xzf \"$latest\"; systemctl restart consul"
	case "nomad-server", "nomad-client":
		return "set -e; latest=$(ls -1t /etc/nomad.d.stackforge.*.tgz 2>/dev/null | head -n1); test -n \"$latest\"; tar -C /etc -xzf \"$latest\"; systemctl restart nomad"
	case "traefik":
		return "set -e; latest=$(ls -1t /etc/traefik.stackforge.*.tgz 2>/dev/null | head -n1); test -n \"$latest\"; tar -C /etc -xzf \"$latest\"; systemctl restart traefik"
	case "control-plane":
		return "set -e; latest=$(ls -1t /etc/stackforge.stackforge.*.tgz 2>/dev/null | head -n1); if [ -n \"$latest\" ]; then tar -C /etc -xzf \"$latest\"; fi; systemctl restart stackforge-control-plane"
	default:
		return ""
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func (r *Report) record(s Step) {
	r.Steps = append(r.Steps, StepRecord{ID: s.ID, Name: s.Name, Node: s.Node, Role: s.Role, Status: s.Status, Error: s.Error, At: time.Now().UTC(), Rollback: s.Rollback, RollbackID: s.RollbackID, ChangedFiles: s.ChangedFiles, BackupFiles: s.BackupFiles, Recovery: s.FailureRecovery, IdempotencyKey: s.IdempotencyKey, DryRunDescription: s.DryRunDescription})
}

func (r *Report) fail(s Step) {
	rec := StepRecord{ID: s.ID, Name: s.Name, Node: s.Node, Role: s.Role, Status: s.Status, Error: s.Error, At: time.Now().UTC(), Rollback: s.Rollback, RollbackID: s.RollbackID, ChangedFiles: s.ChangedFiles, BackupFiles: s.BackupFiles, Recovery: s.FailureRecovery, IdempotencyKey: s.IdempotencyKey, DryRunDescription: s.DryRunDescription}
	r.FailedSteps = append(r.FailedSteps, rec)
	if s.FailureRecovery != "" {
		r.SuggestedRecovery = append(r.SuggestedRecovery, s.FailureRecovery)
	}
	r.SuggestedRecovery = append(r.SuggestedRecovery, s.Rollback, "Inspect logs with journalctl/systemctl on the failed node, then run stackforge install --resume.")
}

func LoadReport(path string) (*Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	return &r, json.Unmarshal(b, &r)
}

func writeReports(dir string, report *Report) {
	b, _ := json.MarshalIndent(report, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "install-report.json"), b, 0600)
	_ = os.WriteFile("stackforge-install-report.json", b, 0644)
	md := "# StackForge Install Report\n\n"
	md += "Cluster: " + report.Cluster + "\n\n"
	for _, s := range report.Steps {
		line := fmt.Sprintf("- %s `%s` on `%s`", strings.ToUpper(string(s.Status)), s.ID, s.Node)
		if s.Error != "" {
			line += ": " + s.Error
		}
		md += line + "\n"
	}
	if len(report.SuggestedRecovery) > 0 {
		md += "\n## Suggested Recovery\n"
		for _, item := range report.SuggestedRecovery {
			md += "- " + item + "\n"
		}
	}
	_ = os.WriteFile("STACKFORGE_INSTALL_REPORT.md", []byte(md), 0644)
}
