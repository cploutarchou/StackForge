package inventory

import (
	"context"
	"reflect"
	"strings"
	"time"

	"stackforge/internal/stackforge/remoteexec"
)

type RefreshOptions struct {
	StateDir string
	Executor remoteexec.Executor
	Timeout  time.Duration
}

func ObservationCommand() string {
	return `set +e
echo "os_name=$( . /etc/os-release 2>/dev/null; echo ${PRETTY_NAME:-unknown})"
echo "os_version=$( . /etc/os-release 2>/dev/null; echo ${VERSION_ID:-unknown})"
echo "kernel=$(uname -r 2>/dev/null || true)"
echo "observed_ips=$(hostname -I 2>/dev/null | tr ' ' ',' | sed 's/,$//')"
echo "firewall=$(if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q active; then echo ufw; elif command -v nft >/dev/null 2>&1 && nft list ruleset >/dev/null 2>&1; then echo nftables; else echo none; fi)"
echo "listening=$(ss -ltnupH 2>/dev/null | awk '{print $1 "/" $5}' | tr '\n' ',' | sed 's/,$//')"
echo "consul_version=$(consul version 2>/dev/null | head -n1 | awk '{print $2}')"
echo "consul_service=$(systemctl is-active consul 2>/dev/null || true)"
echo "consul_leader=$(curl -fsS http://127.0.0.1:8500/v1/status/leader 2>/dev/null | tr -d '"' || true)"
echo "consul_members=$(curl -fsS http://127.0.0.1:8500/v1/status/peers 2>/dev/null | tr -d '[]" ' || true)"
echo "nomad_version=$(nomad version 2>/dev/null | head -n1 | awk '{print $2}')"
echo "nomad_service=$(systemctl is-active nomad 2>/dev/null || true)"
echo "nomad_leader=$(curl -fsS http://127.0.0.1:4646/v1/status/leader 2>/dev/null | tr -d '"' || true)"
echo "nomad_nodes=$(curl -fsS http://127.0.0.1:4646/v1/nodes 2>/dev/null | jq -r 'length' 2>/dev/null || true)"
echo "traefik_version=$(traefik version 2>/dev/null | awk '/Version:/ {print $2; exit}')"
echo "traefik_service=$(systemctl is-active traefik 2>/dev/null || true)"
echo "postgres_version=$(sudo -u postgres psql -tAc 'SHOW server_version' 2>/dev/null | xargs || true)"
echo "postgres_service=$(systemctl is-active postgresql 2>/dev/null || true)"
echo "stackforge_service=$(systemctl is-active stackforge-control-plane 2>/dev/null || true)"
echo "stackforge_health=$(curl -fsS http://127.0.0.1:8080/health 2>/dev/null | tr -d '\n' || true)"`
}

func ApplyObservation(inv *Inventory, nodeName, stdout string, warnings []string) {
	Normalize(inv)
	now := time.Now().UTC()
	for i := range inv.Nodes {
		if inv.Nodes[i].Name != nodeName {
			continue
		}
		data := parseObservation(stdout)
		n := &inv.Nodes[i]
		n.OSName = data["os_name"]
		n.OSVersion = data["os_version"]
		n.Kernel = data["kernel"]
		n.ObservedIPs = splitCSV(data["observed_ips"])
		n.Firewall = valueOr(data["firewall"], "unknown")
		n.Listening = splitCSV(data["listening"])
		n.LastObserved = now
		n.Warnings = append(n.Warnings, warnings...)
		setIf(n.Versions, "consul", data["consul_version"])
		setIf(n.Services, "consul", data["consul_service"])
		setIf(n.Leaders, "consul", data["consul_leader"])
		setIf(n.Components, "consul_members", data["consul_members"])
		setIf(n.Versions, "nomad", data["nomad_version"])
		setIf(n.Services, "nomad", data["nomad_service"])
		setIf(n.Leaders, "nomad", data["nomad_leader"])
		setIf(n.Components, "nomad_nodes", data["nomad_nodes"])
		setIf(n.Versions, "traefik", data["traefik_version"])
		setIf(n.Services, "traefik", data["traefik_service"])
		setIf(n.Versions, "postgres", data["postgres_version"])
		setIf(n.Services, "postgres", data["postgres_service"])
		setIf(n.Services, "stackforge-control-plane", data["stackforge_service"])
		setIf(n.Components, "stackforge_health", data["stackforge_health"])
		for component, version := range n.Versions {
			if version != "" {
				inv.ComponentVersions[n.Name+"/"+component] = version
			}
		}
		for service, status := range n.Services {
			if status != "" {
				inv.ServiceStatus[n.Name+"/"+service] = status
			}
		}
		if n.Firewall != "" && n.Firewall != "unknown" {
			inv.FirewallMode = n.Firewall
		}
		n.HealthStatus = "observed"
	}
	MarkHealthCheck(inv, "observed", warnings)
}

func Refresh(ctx context.Context, inv *Inventory, exec remoteexec.Executor) []string {
	Normalize(inv)
	var warnings []string
	if exec == nil || (reflect.ValueOf(exec).Kind() == reflect.Ptr && reflect.ValueOf(exec).IsNil()) {
		warnings = append(warnings, "inventory refresh skipped: no live executor configured")
		MarkHealthCheck(inv, "inventory-only", warnings)
		return warnings
	}
	for _, n := range inv.Nodes {
		res, err := exec.Run(ctx, n.PrivateIP, remoteexec.Command{Command: ObservationCommand(), Sudo: true, Timeout: 45 * time.Second})
		nodeWarnings := []string{}
		if err != nil {
			nodeWarnings = append(nodeWarnings, "inventory refresh failed for "+n.Name+": "+err.Error())
			warnings = append(warnings, nodeWarnings...)
		}
		ApplyObservation(inv, n.Name, res.Stdout, nodeWarnings)
	}
	return warnings
}

func parseObservation(stdout string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func setIf(m map[string]string, k, v string) {
	if strings.TrimSpace(v) != "" {
		m[k] = strings.TrimSpace(v)
	}
}

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}
