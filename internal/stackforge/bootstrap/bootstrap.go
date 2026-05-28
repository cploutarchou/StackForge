package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/remoteexec"
	sfssh "stackforge/internal/stackforge/ssh"
)

const (
	AuthPassword   = "password"
	AuthPrivateKey = "private-key"
)

type Node struct {
	Name           string   `json:"name"`
	Address        string   `json:"address"`
	PrivateIP      string   `json:"private_ip,omitempty"`
	User           string   `json:"ssh_user"`
	Port           int      `json:"ssh_port"`
	Auth           string   `json:"auth"`
	PublicKeyPath  string   `json:"public_key_path"`
	PrivateKeyPath string   `json:"private_key_path,omitempty"`
	Roles          []string `json:"roles,omitempty"`
}

type Step struct {
	Node    string `json:"node"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type Report struct {
	Cluster    string    `json:"cluster"`
	DryRun     bool      `json:"dry_run"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Steps      []Step    `json:"steps"`
	Warnings   []string  `json:"warnings,omitempty"`
	ReportPath string    `json:"report_path,omitempty"`
}

type Options struct {
	ClusterName             string
	Environment             string
	StateDir                string
	Nodes                   []Node
	DryRun                  bool
	PasswordReader          func(Node) (string, error)
	PasswordExecutorFactory func(Node, string) remoteexec.Executor
	KeyExecutorFactory      func(Node) remoteexec.Executor
}

func Run(ctx context.Context, opts Options) (*Report, error) {
	if opts.ClusterName == "" {
		opts.ClusterName = "stackforge-production"
	}
	if opts.Environment == "" {
		opts.Environment = "production"
	}
	if opts.StateDir == "" {
		return nil, errors.New("state dir is required")
	}
	if len(opts.Nodes) == 0 {
		return nil, errors.New("at least one node is required")
	}
	report := &Report{Cluster: opts.ClusterName, DryRun: opts.DryRun, StartedAt: time.Now().UTC()}
	inv, _ := inventory.Load(filepath.Join(opts.StateDir, "inventory.yaml"))
	if inv == nil {
		inv = &inventory.Inventory{ClusterName: opts.ClusterName, Environment: opts.Environment, InstallStatus: "bootstrapping", LastHealthCheckStatus: "pending", FirewallMode: "ufw", ComponentVersions: map[string]string{}, ServiceStatus: map[string]string{}}
	}
	for _, node := range opts.Nodes {
		normalizeNode(&node)
		publicKey, err := ValidatePublicKeyFile(node.PublicKeyPath)
		if err != nil {
			report.add(node.Name, "validate-public-key", "failed", err.Error())
			writeReport(opts.StateDir, report)
			return report, err
		}
		report.add(node.Name, "validate-public-key", status(opts.DryRun), expandHome(node.PublicKeyPath))
		if opts.DryRun {
			report.add(node.Name, "ssh-bootstrap-plan", "dry-run", dryRunMessage(node))
			continue
		}
		switch node.Auth {
		case AuthPassword:
			report.Warnings = append(report.Warnings, "password auth for "+node.Name+" is used only for initial bootstrap and is not stored")
			if opts.PasswordReader == nil {
				err := errors.New("password auth bootstrap requires a secure password prompt; refusing to read passwords from flags, environment, inventory, or logs")
				report.add(node.Name, "password-bootstrap", "failed", err.Error())
				writeReport(opts.StateDir, report)
				return report, err
			}
			password, err := opts.PasswordReader(node)
			if err != nil {
				report.add(node.Name, "password-bootstrap", "failed", err.Error())
				writeReport(opts.StateDir, report)
				return report, err
			}
			exec := passwordExecutor(opts, node, password)
			if _, err := exec.Run(ctx, node.Address, remoteexec.Command{Command: AuthorizedKeysAppendCommand(publicKey), Timeout: 30 * time.Second, Secrets: []string{password}}); err != nil {
				report.add(node.Name, "copy-public-key", "failed", err.Error())
				writeReport(opts.StateDir, report)
				return report, err
			}
			report.add(node.Name, "copy-public-key", "ok", "~/.ssh/authorized_keys updated idempotently")
		case AuthPrivateKey:
			exec := keyExecutor(opts, node)
			if _, err := exec.Run(ctx, node.Address, remoteexec.Command{Command: AuthorizedKeysAppendCommand(publicKey), Timeout: 30 * time.Second}); err != nil {
				report.add(node.Name, "ensure-public-key", "failed", err.Error())
				writeReport(opts.StateDir, report)
				return report, err
			}
			report.add(node.Name, "ensure-public-key", "ok", "~/.ssh/authorized_keys checked idempotently")
		default:
			err := fmt.Errorf("unsupported auth method %q", node.Auth)
			report.add(node.Name, "auth-method", "failed", err.Error())
			writeReport(opts.StateDir, report)
			return report, err
		}
		if _, err := keyExecutor(opts, node).Run(ctx, node.Address, remoteexec.Command{Command: "true", Timeout: 15 * time.Second}); err != nil {
			err = fmt.Errorf("key-based SSH verification failed for %s: %w", node.Name, err)
			report.add(node.Name, "verify-key-ssh", "failed", err.Error())
			writeReport(opts.StateDir, report)
			return report, err
		}
		report.add(node.Name, "verify-key-ssh", "ok", "passwordless SSH verified")
		upsertInventoryNode(inv, node)
		inventory.MarkStepSuccess(inv, node.Name+":ssh-bootstrap")
		if err := inventory.Save(filepath.Join(opts.StateDir, "inventory.yaml"), inv); err != nil {
			report.add(node.Name, "inventory-update", "failed", err.Error())
			writeReport(opts.StateDir, report)
			return report, err
		}
		report.add(node.Name, "inventory-update", "ok", filepath.Join(opts.StateDir, "inventory.yaml"))
	}
	if opts.DryRun {
		report.Warnings = append(report.Warnings, "dry-run only; no SSH connections opened and no inventory was modified")
	}
	report.FinishedAt = time.Now().UTC()
	writeReport(opts.StateDir, report)
	return report, nil
}

func ValidatePublicKeyFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("public key path is required")
	}
	b, err := os.ReadFile(expandHome(path))
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return "", errors.New("public key file is empty")
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key)); err != nil {
		return "", fmt.Errorf("invalid public key: %w", err)
	}
	return key, nil
}

func AuthorizedKeysAppendCommand(publicKey string) string {
	q := shellQuote(publicKey)
	return "set -e; install -d -m 0700 ~/.ssh; touch ~/.ssh/authorized_keys; chmod 0600 ~/.ssh/authorized_keys; grep -qxF -- " + q + " ~/.ssh/authorized_keys || printf '%s\n' " + q + " >> ~/.ssh/authorized_keys; chmod 0700 ~/.ssh; chmod 0600 ~/.ssh/authorized_keys"
}

func PrivateKeyPathForPublic(path string) string {
	path = expandHome(path)
	return strings.TrimSuffix(path, ".pub")
}

func upsertInventoryNode(inv *inventory.Inventory, node Node) {
	inventory.Normalize(inv)
	addr := node.PrivateIP
	if addr == "" {
		addr = node.Address
	}
	for i := range inv.Nodes {
		if inv.Nodes[i].Name == node.Name {
			inv.Nodes[i].Roles = node.Roles
			inv.Nodes[i].PrivateIP = addr
			inv.Nodes[i].PublicIP = node.Address
			inv.Nodes[i].SSH = inventory.SSHInfo{User: node.User, Port: node.Port, PrivateKeyPath: node.PrivateKeyPath}
			inv.Nodes[i].HealthStatus = "ssh-ready"
			return
		}
	}
	inv.Nodes = append(inv.Nodes, inventory.Node{Name: node.Name, Roles: node.Roles, PrivateIP: addr, PublicIP: node.Address, SSH: inventory.SSHInfo{User: node.User, Port: node.Port, PrivateKeyPath: node.PrivateKeyPath}, Components: map[string]string{}, Services: map[string]string{}, HealthStatus: "ssh-ready"})
}

func normalizeNode(n *Node) {
	if n.Port == 0 {
		n.Port = 22
	}
	if n.User == "" {
		n.User = "root"
	}
	if n.Auth == "" {
		n.Auth = AuthPrivateKey
	}
	if n.PublicKeyPath == "" {
		n.PublicKeyPath = "~/.ssh/id_ed25519.pub"
	}
	if n.PrivateKeyPath == "" {
		n.PrivateKeyPath = PrivateKeyPathForPublic(n.PublicKeyPath)
	}
	if n.PrivateIP == "" {
		n.PrivateIP = n.Address
	}
}

func passwordExecutor(opts Options, node Node, password string) remoteexec.Executor {
	if opts.PasswordExecutorFactory != nil {
		return opts.PasswordExecutorFactory(node, password)
	}
	return sfssh.NewPasswordExecutor(node.User, node.Port, password)
}

func keyExecutor(opts Options, node Node) remoteexec.Executor {
	if opts.KeyExecutorFactory != nil {
		return opts.KeyExecutorFactory(node)
	}
	return sfssh.NewExecutor(node.User, node.Port, node.PrivateKeyPath)
}

func dryRunMessage(node Node) string {
	return fmt.Sprintf("would contact %s as %s:%d using auth=%s; would read public key %s; would create ~/.ssh and update ~/.ssh/authorized_keys; would verify key-based SSH; would update inventory.yaml without passwords", node.Address, node.User, node.Port, node.Auth, expandHome(node.PublicKeyPath))
}

func status(dry bool) string {
	if dry {
		return "dry-run"
	}
	return "ok"
}

func (r *Report) add(node, name, status, message string) {
	r.Steps = append(r.Steps, Step{Node: node, Name: name, Status: status, Message: message})
}

func writeReport(dir string, report *Report) {
	if dir == "" || report == nil {
		return
	}
	_ = os.MkdirAll(dir, 0700)
	report.ReportPath = filepath.Join(dir, "bootstrap-report.json")
	b, _ := json.MarshalIndent(report, "", "  ")
	_ = os.WriteFile(report.ReportPath, b, 0600)
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
