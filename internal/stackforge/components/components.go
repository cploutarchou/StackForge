package components

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/remoteexec"
)

const (
	BasePackages = "base-packages"
	Docker       = "docker"
	Consul       = "consul"
	Nomad        = "nomad"
	Traefik      = "traefik"
	Postgres     = "postgres"
	ControlPlane = "stackforge-control-plane"
	All          = "all"
)

type InstallItem struct {
	Node      string `json:"node"`
	Address   string `json:"address"`
	Component string `json:"component"`
	Command   string `json:"command"`
	DryRun    bool   `json:"dry_run"`
	Status    string `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Status struct {
	Node      string            `json:"node"`
	Component string            `json:"component"`
	Installed bool              `json:"installed"`
	Version   string            `json:"version,omitempty"`
	Systemd   string            `json:"systemd_status,omitempty"`
	Ports     []string          `json:"listening_ports,omitempty"`
	Warnings  []string          `json:"warnings,omitempty"`
	Raw       map[string]string `json:"raw,omitempty"`
}

func PlanInstall(inv *inventory.Inventory, component, nodeName string, dryRun bool) ([]InstallItem, error) {
	if inv == nil {
		return nil, errors.New("inventory is required")
	}
	component = normalizeComponent(component)
	var out []InstallItem
	for _, node := range inv.Nodes {
		if nodeName != "" && node.Name != nodeName {
			continue
		}
		for _, c := range componentsForNode(node, component) {
			cmd, err := InstallCommand(c, node.SSH.User)
			if err != nil {
				return nil, err
			}
			addr := node.PrivateIP
			if addr == "" {
				addr = node.PublicIP
			}
			out = append(out, InstallItem{Node: node.Name, Address: addr, Component: c, Command: cmd, DryRun: dryRun})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no matching nodes for component=%s node=%s", component, nodeName)
	}
	return out, nil
}

func RunInstall(ctx context.Context, inv *inventory.Inventory, exec remoteexec.Executor, component, nodeName string, dryRun bool) ([]InstallItem, error) {
	items, err := PlanInstall(inv, component, nodeName, dryRun)
	if err != nil {
		return nil, err
	}
	if dryRun {
		for i := range items {
			items[i].Status = "dry-run"
		}
		return items, nil
	}
	if exec == nil {
		return items, errors.New("remote executor is required for live component install")
	}
	for i := range items {
		res, err := exec.Run(ctx, items[i].Address, remoteexec.Command{Command: items[i].Command, Sudo: true, Timeout: 10 * time.Minute})
		if err != nil {
			items[i].Status = "failed"
			items[i].Error = strings.TrimSpace(res.Stderr)
			if items[i].Error == "" {
				items[i].Error = err.Error()
			}
			return items, err
		}
		items[i].Status = "ok"
	}
	return items, nil
}

func InstallCommand(component, sshUser string) (string, error) {
	switch normalizeComponent(component) {
	case BasePackages:
		return BasePackagesCommand(), nil
	case Docker:
		return DockerInstallCommand(sshUser, false), nil
	case Consul:
		return "command -v consul >/dev/null 2>&1 && systemctl is-active --quiet consul || { echo 'consul role install is managed by stackforge install/onboard because it needs cluster config and secrets' >&2; exit 2; }", nil
	case Nomad:
		return "command -v nomad >/dev/null 2>&1 && systemctl is-active --quiet nomad || { echo 'nomad role install is managed by stackforge install/onboard because it needs cluster config and secrets' >&2; exit 2; }", nil
	case Traefik:
		return "command -v traefik >/dev/null 2>&1 && systemctl is-active --quiet traefik || { echo 'traefik role install is managed by stackforge install/onboard because it needs routing config' >&2; exit 2; }", nil
	case Postgres:
		return "systemctl is-active --quiet postgresql || { echo 'postgres role install is managed by stackforge install/onboard because it needs generated secrets' >&2; exit 2; }", nil
	case ControlPlane:
		return "systemctl is-active --quiet stackforge-control-plane || { echo 'control-plane install is managed by stackforge install/onboard because it needs generated secrets' >&2; exit 2; }", nil
	default:
		return "", fmt.Errorf("unknown component %q", component)
	}
}

func BasePackagesCommand() string {
	pkgs := "curl wget unzip ca-certificates gnupg lsb-release jq ufw systemd openssl iproute2 tar gzip apt-transport-https"
	return "set -e; missing=''; for p in " + pkgs + "; do dpkg -s \"$p\" >/dev/null 2>&1 || missing=\"$missing $p\"; done; if [ -n \"$missing\" ]; then apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y $missing; fi"
}

func DockerInstallCommand(sshUser string, addUser bool) string {
	cmd := "set -e; if command -v docker >/dev/null 2>&1 && docker version --format '{{.Server.Version}}' >/dev/null 2>&1; then exit 0; fi; " +
		"apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl gnupg lsb-release; " +
		"install -m 0755 -d /etc/apt/keyrings; " +
		"curl -fsSL https://download.docker.com/linux/$(. /etc/os-release && echo \"$ID\")/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg.tmp; " +
		"mv /etc/apt/keyrings/docker.gpg.tmp /etc/apt/keyrings/docker.gpg; chmod 0644 /etc/apt/keyrings/docker.gpg; " +
		". /etc/os-release; echo \"deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/$ID $VERSION_CODENAME stable\" > /etc/apt/sources.list.d/docker.list; " +
		"apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin; " +
		"systemctl enable docker && systemctl start docker && docker info >/dev/null"
	if addUser && sshUser != "" && sshUser != "root" {
		cmd += "; usermod -aG docker " + shellQuote(sshUser)
	}
	return cmd
}

func StatusCommand() string {
	return `set +e
echo "docker_installed=$(command -v docker >/dev/null 2>&1 && echo yes || echo no)"
echo "docker_version=$(docker version --format '{{.Server.Version}}' 2>/dev/null || true)"
echo "docker_service=$(systemctl is-active docker 2>/dev/null || true)"
echo "consul_installed=$(command -v consul >/dev/null 2>&1 && echo yes || echo no)"
echo "consul_version=$(consul version 2>/dev/null | head -n1 | awk '{print $2}')"
echo "consul_service=$(systemctl is-active consul 2>/dev/null || true)"
echo "nomad_installed=$(command -v nomad >/dev/null 2>&1 && echo yes || echo no)"
echo "nomad_version=$(nomad version 2>/dev/null | head -n1 | awk '{print $2}')"
echo "nomad_service=$(systemctl is-active nomad 2>/dev/null || true)"
echo "traefik_installed=$(command -v traefik >/dev/null 2>&1 && echo yes || echo no)"
echo "traefik_version=$(traefik version 2>/dev/null | awk '/Version:/ {print $2; exit}')"
echo "traefik_service=$(systemctl is-active traefik 2>/dev/null || true)"
echo "postgres_installed=$(command -v psql >/dev/null 2>&1 && echo yes || echo no)"
echo "postgres_version=$(sudo -u postgres psql -tAc 'SHOW server_version' 2>/dev/null | xargs || true)"
echo "postgres_service=$(systemctl is-active postgresql 2>/dev/null || true)"
echo "stackforge_control_plane_installed=$(systemctl list-unit-files stackforge-control-plane.service 2>/dev/null | grep -q stackforge-control-plane && echo yes || echo no)"
echo "stackforge_control_plane_service=$(systemctl is-active stackforge-control-plane 2>/dev/null || true)"
echo "listening=$(ss -ltnH 2>/dev/null | awk '{print $4}' | tr '\n' ',' | sed 's/,$//')"`
}

func ParseStatus(node string, stdout string) []Status {
	data := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			data[k] = v
		}
	}
	ports := splitCSV(data["listening"])
	return []Status{
		componentStatus(node, Docker, data["docker_installed"], data["docker_version"], data["docker_service"], ports, data),
		componentStatus(node, Consul, data["consul_installed"], data["consul_version"], data["consul_service"], ports, data),
		componentStatus(node, Nomad, data["nomad_installed"], data["nomad_version"], data["nomad_service"], ports, data),
		componentStatus(node, Traefik, data["traefik_installed"], data["traefik_version"], data["traefik_service"], ports, data),
		componentStatus(node, Postgres, data["postgres_installed"], data["postgres_version"], data["postgres_service"], ports, data),
		componentStatus(node, ControlPlane, data["stackforge_control_plane_installed"], "", data["stackforge_control_plane_service"], ports, data),
	}
}

func componentStatus(node, component, installed, version, service string, ports []string, raw map[string]string) Status {
	s := Status{Node: node, Component: component, Installed: installed == "yes", Version: version, Systemd: service, Ports: ports, Raw: raw}
	if s.Installed && service != "" && service != "active" {
		s.Warnings = append(s.Warnings, "installed but systemd service is not active")
	}
	return s
}

func componentsForNode(node inventory.Node, requested string) []string {
	if requested != All {
		return []string{requested}
	}
	seen := map[string]bool{BasePackages: true}
	out := []string{BasePackages}
	for _, role := range node.Roles {
		switch role {
		case "docker-host", "nomad-client":
			seen[Docker] = true
			if role == "nomad-client" {
				seen[Nomad] = true
			}
		case "consul-server":
			seen[Consul] = true
		case "nomad-server":
			seen[Nomad] = true
		case "traefik":
			seen[Traefik] = true
		case "database":
			seen[Postgres] = true
		case "control-plane":
			seen[ControlPlane] = true
		}
	}
	for _, c := range []string{Docker, Consul, Nomad, Traefik, Postgres, ControlPlane} {
		if seen[c] {
			out = append(out, c)
		}
	}
	return out
}

func normalizeComponent(component string) string {
	component = strings.ToLower(strings.TrimSpace(component))
	switch component {
	case "", All:
		return All
	case "base", "packages":
		return BasePackages
	case "postgresql", "database":
		return Postgres
	case "control-plane":
		return ControlPlane
	default:
		return component
	}
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

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
