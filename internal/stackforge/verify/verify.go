package verify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"stackforge/internal/stackforge/backup"
	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/remoteexec"
	"stackforge/internal/stackforge/uninstall"
)

type Check struct {
	Node    string `json:"node"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type Report struct {
	Cluster string   `json:"cluster"`
	Safe    bool     `json:"safe"`
	Checks  []Check  `json:"checks"`
	Errors  []string `json:"errors,omitempty"`
}

func Run(ctx context.Context, stateDir string, inv *inventory.Inventory, exec remoteexec.Executor) Report {
	cluster := ""
	if inv != nil {
		cluster = inv.ClusterName
	}
	report := Report{Cluster: cluster, Safe: true}
	add := func(node, name, status, message string) {
		report.Checks = append(report.Checks, Check{Node: node, Name: name, Status: status, Message: message})
		if status == "fail" {
			report.Safe = false
			report.Errors = append(report.Errors, strings.TrimSpace(node+" "+name+": "+message))
		}
	}
	if inv == nil {
		add("local", "inventory", "fail", "inventory is required")
		return report
	}
	if exec == nil {
		add("local", "remote-executor", "fail", "verify requires live SSH access from inventory/config")
		return report
	}
	verifyLocalState(stateDir, add)
	for _, n := range inv.Nodes {
		res, address, errText := runNodeCommand(ctx, exec, n)
		if errText != "" {
			add(n.Name, "ssh", "fail", errText)
			continue
		}
		add(n.Name, "ssh", "ok", "verified via "+address)
		data := parse(res.Stdout)
		checkNode(n, data, add)
	}
	if _, err := backup.RunWithOptions(backup.Options{StateDir: stateDir, Cluster: inv.ClusterName, DryRun: true}); err != nil {
		add("local", "backup-dry-run", "fail", err.Error())
	} else {
		add("local", "backup-dry-run", "ok", "backup command can plan without live changes")
	}
	plan := uninstall.BuildPlan(true)
	if len(plan.RemoveServices) == 0 {
		add("local", "uninstall-dry-run", "fail", "uninstall plan is empty")
	} else {
		add("local", "uninstall-dry-run", "ok", "uninstall plan generated with preserve-data")
	}
	return report
}

func runNodeCommand(ctx context.Context, exec remoteexec.Executor, n inventory.Node) (remoteexec.Result, string, string) {
	var errors []string
	addresses := nodeAddresses(n)
	if len(addresses) == 0 {
		return remoteexec.Result{}, "", "node has no private or public address in inventory"
	}
	for _, addr := range addresses {
		res, err := exec.Run(ctx, addr, remoteexec.Command{Command: Command(), Sudo: true, Timeout: 60 * time.Second})
		if err == nil {
			return res, addr, ""
		}
		msg := err.Error()
		if strings.TrimSpace(res.Stderr) != "" {
			msg += ": " + strings.TrimSpace(res.Stderr)
		}
		errors = append(errors, addr+" -> "+msg)
	}
	return remoteexec.Result{}, "", strings.Join(errors, "; ")
}

func nodeAddresses(n inventory.Node) []string {
	seen := map[string]bool{}
	var out []string
	for _, addr := range []string{n.PrivateIP, n.PublicIP} {
		addr = strings.TrimSpace(addr)
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		out = append(out, addr)
	}
	return out
}

func Command() string {
	return `set +e
echo "stackforge_service=$(systemctl is-active stackforge-control-plane 2>/dev/null || true)"
echo "stackforge_health=$(curl -fsS http://127.0.0.1:8080/health 2>/dev/null | tr -d '\n' || true)"
echo "stackforge_api_code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/v1/domains 2>/dev/null || true)"
echo "postgres_service=$(systemctl is-active postgresql 2>/dev/null || true)"
echo "postgres_query=$(sudo -u postgres psql -d stackforge -tAc 'SELECT 1' 2>/dev/null | xargs || true)"
echo "postgres_migrations=$(sudo -u postgres psql -d stackforge -tAc 'SELECT to_regclass($$public.domains$$) IS NOT NULL' 2>/dev/null | xargs || true)"
echo "postgres_public=$(ss -ltnH '( sport = :5432 )' 2>/dev/null | awk '{print $4}' | grep -Ev '^(127\.0\.0\.1|\[::1\]|\[::ffff:127\.0\.0\.1\]):5432$' >/dev/null && echo yes || echo no)"
echo "consul_service=$(systemctl is-active consul 2>/dev/null || true)"
echo "consul_leader=$(curl -fsS http://127.0.0.1:8500/v1/status/leader 2>/dev/null | tr -d '"' || true)"
echo "consul_members=$(curl -fsS http://127.0.0.1:8500/v1/status/peers 2>/dev/null | tr -d '[]" ' || true)"
echo "nomad_service=$(systemctl is-active nomad 2>/dev/null || true)"
echo "nomad_leader=$(curl -fsS http://127.0.0.1:4646/v1/status/leader 2>/dev/null | tr -d '"' || true)"
echo "traefik_service=$(systemctl is-active traefik 2>/dev/null || true)"
echo "traefik_ports=$(ss -ltnH 2>/dev/null | awk '{print $4}' | tr '\n' ',' || true)"
echo "firewall=$(if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q active; then echo ufw; else echo none; fi)"
echo "remote_env_mode=$(stat -c '%a' /etc/stackforge/stackforge.env 2>/dev/null || true)"`
}

func verifyLocalState(stateDir string, add func(string, string, string, string)) {
	path := filepath.Join(stateDir, "generated-secrets.yaml")
	info, err := os.Stat(path)
	if err != nil {
		add("local", "generated-secrets-permissions", "fail", err.Error())
		return
	}
	if info.Mode().Perm() != 0600 {
		add("local", "generated-secrets-permissions", "fail", fmt.Sprintf("%s mode is %o, want 600", path, info.Mode().Perm()))
		return
	}
	add("local", "generated-secrets-permissions", "ok", path+" mode 600")
}

func checkNode(n inventory.Node, data map[string]string, add func(string, string, string, string)) {
	if hasRole(n, "control-plane") {
		expect(n.Name, "control-plane-service", data["stackforge_service"] == "active", data["stackforge_service"], add)
		expect(n.Name, "health", isHealthyValue(data["stackforge_health"]), data["stackforge_health"], add)
		expect(n.Name, "api-auth", data["stackforge_api_code"] == "401", "HTTP "+data["stackforge_api_code"], add)
		expect(n.Name, "remote-env-permissions", data["remote_env_mode"] == "600", "mode "+data["remote_env_mode"], add)
	}
	if hasRole(n, "database") {
		expect(n.Name, "database-service", data["postgres_service"] == "active", data["postgres_service"], add)
		expect(n.Name, "database-reachable", data["postgres_query"] == "1", data["postgres_query"], add)
		expect(n.Name, "database-migrations", data["postgres_migrations"] == "t" || data["postgres_migrations"] == "true", data["postgres_migrations"], add)
		expect(n.Name, "database-not-public", data["postgres_public"] == "no", data["postgres_public"], add)
	}
	if hasRole(n, "consul-server") {
		expect(n.Name, "consul-service", data["consul_service"] == "active", data["consul_service"], add)
		expect(n.Name, "consul-members", data["consul_members"] != "", data["consul_members"], add)
	}
	if hasRole(n, "nomad-server") {
		expect(n.Name, "nomad-service", data["nomad_service"] == "active", data["nomad_service"], add)
		expect(n.Name, "nomad-leader", data["nomad_leader"] != "", data["nomad_leader"], add)
	}
	if hasRole(n, "traefik") {
		expect(n.Name, "traefik-service", data["traefik_service"] == "active", data["traefik_service"], add)
		ports := data["traefik_ports"]
		expect(n.Name, "traefik-ports", strings.Contains(ports, ":80") && strings.Contains(ports, ":443"), ports, add)
	}
	expect(n.Name, "firewall", data["firewall"] == "ufw", data["firewall"], add)
}

func isHealthyValue(v string) bool {
	v = strings.TrimSpace(v)
	if strings.EqualFold(v, "ok") {
		return true
	}
	v = strings.ToLower(v)
	return strings.Contains(v, `"status":"ok"`) || strings.Contains(v, `'status':'ok'`)
}

func expect(node, name string, ok bool, message string, add func(string, string, string, string)) {
	if ok {
		add(node, name, "ok", message)
		return
	}
	add(node, name, "fail", message)
}

func parse(stdout string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			out[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return out
}

func hasRole(n inventory.Node, role string) bool {
	for _, r := range n.Roles {
		if r == role {
			return true
		}
	}
	return false
}
