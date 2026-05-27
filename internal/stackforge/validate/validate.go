package validate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"stackforge/internal/stackforge/config"
	"stackforge/internal/stackforge/firewall"
	"stackforge/internal/stackforge/remoteexec"
	"stackforge/internal/stackforge/safety"
)

type Check struct {
	Node    string `json:"node"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type Report struct {
	Cluster        string        `json:"cluster"`
	Checks         []Check       `json:"checks"`
	Errors         []string      `json:"errors"`
	Warnings       []string      `json:"warnings,omitempty"`
	SafetyFindings safety.Report `json:"safety"`
	Safe           bool          `json:"safe"`
	DryRun         bool          `json:"dry_run"`
	Live           bool          `json:"live"`
	Production     bool          `json:"production"`
}

type Options struct {
	DryRun             bool
	Live               bool
	Production         bool
	AllowNoFirewall    bool
	AllowExampleConfig bool
	AllowPublicSSH     bool
	ConfirmProduction  bool
}

func Run(ctx context.Context, cfg *config.Config, exec remoteexec.Executor, dryRun bool, allowNoFirewall bool) Report {
	return RunWithOptions(ctx, cfg, exec, Options{DryRun: dryRun, AllowNoFirewall: allowNoFirewall})
}

func RunWithOptions(ctx context.Context, cfg *config.Config, exec remoteexec.Executor, opts Options) Report {
	cluster := ""
	if cfg != nil {
		cluster = cfg.Cluster.Name
	}
	report := Report{Cluster: cluster, DryRun: opts.DryRun, Live: opts.Live, Production: opts.Production, Safe: true}
	add := func(node, name, status, message string) {
		report.Checks = append(report.Checks, Check{Node: node, Name: name, Status: status, Message: message})
		if status == "fail" {
			report.Safe = false
			report.Errors = append(report.Errors, strings.TrimSpace(node+" "+name+": "+message))
		} else if status == "warn" {
			report.Warnings = append(report.Warnings, strings.TrimSpace(node+" "+name+": "+message))
		}
	}
	if err := config.Validate(cfg); err != nil {
		add("local", "config", "fail", err.Error())
		return report
	}
	safetyReport := safety.Check(cfg, safety.Options{Live: opts.Live, Production: opts.Production, ConfirmProduction: opts.ConfirmProduction, AllowExampleConfig: opts.AllowExampleConfig, AllowPublicSSH: opts.AllowPublicSSH})
	report.SafetyFindings = safetyReport
	for _, f := range safetyReport.Findings {
		status := "warn"
		if f.Severity == "error" {
			status = "fail"
		}
		add("local", "safety:"+f.Code, status, f.Message)
	}
	if !domainLooksReal(cfg.ControlPlane.Domain) {
		add("local", "dns-domain", "fail", "control_plane.domain must be a real non-placeholder domain")
	}
	if cfg.Traefik.DashboardEnabled && !domainLooksReal(cfg.Traefik.DashboardDomain) {
		add("local", "traefik-dashboard-domain", "fail", "traefik.dashboard_domain must be a real non-placeholder domain")
	}
	if cloudflareEnabled(cfg) {
		if os.Getenv(cfg.Cloudflare.APITokenEnv) == "" {
			add("local", "cloudflare-token", "fail", cfg.Cloudflare.APITokenEnv+" is required when Cloudflare is enabled")
		} else {
			add("local", "cloudflare-token", "ok", cfg.Cloudflare.APITokenEnv+" is present")
		}
	}
	if _, err := firewall.BuildPlan(cfg); err != nil && !opts.AllowNoFirewall {
		add("local", "firewall-plan", "fail", err.Error())
	} else {
		add("local", "firewall-plan", "ok", "UFW-only plan rejects public internal service exposure")
	}
	for _, n := range cfg.Nodes {
		if opts.DryRun || exec == nil {
			add(n.Name, "ssh", "planned", "would connect to "+n.Address)
			add(n.Name, "os", "planned", "would verify Debian 12+ or Ubuntu 22.04/24.04")
			add(n.Name, "sudo", "planned", "would verify non-interactive sudo/root")
			add(n.Name, "systemd", "planned", "would verify systemd/systemctl")
			add(n.Name, "package-manager", "planned", "would verify apt-get")
			add(n.Name, "base-packages", "planned", "would verify required packages: curl jq ufw openssl iproute2 tar gzip")
			add(n.Name, "docker", "planned", "would detect Docker engine and Compose plugin")
			add(n.Name, "disk", "planned", "would verify at least 20 GiB free on /")
			add(n.Name, "ram", "planned", "would verify at least 2 GiB RAM")
			add(n.Name, "ports", "planned", "would verify required ports are free")
			add(n.Name, "firewall", "planned", "would require UFW and report whether it is active")
			if cfg.Network.PrivateInterface != "" {
				add(n.Name, "private-interface", "planned", "would verify "+cfg.Network.PrivateInterface)
			}
			if cfg.Network.PublicInterface != "" {
				add(n.Name, "public-interface", "planned", "would verify "+cfg.Network.PublicInterface)
			}
			continue
		}
		res, err := exec.Run(ctx, n.Address, remoteexec.Command{Command: PreflightCommand(cfg.Network.PrivateInterface, cfg.Network.PublicInterface), Sudo: false, Timeout: 45 * time.Second})
		if err != nil {
			add(n.Name, "ssh", "fail", err.Error())
			continue
		}
		add(n.Name, "ssh", "ok", "SSH command executed")
		parsePreflight(n.Name, res.Stdout, opts.AllowNoFirewall, add)
	}
	return report
}

func PreflightCommand(privateInterface, publicInterface string) string {
	return fmt.Sprintf(`set +e
echo "os=$(. /etc/os-release 2>/dev/null; echo ${ID:-unknown}:${VERSION_ID:-unknown})"
echo "sudo=$(if [ "$(id -u)" = 0 ] || sudo -n true >/dev/null 2>&1; then echo ok; else echo fail; fi)"
echo "apt=$(if command -v apt-get >/dev/null 2>&1; then echo ok; else echo fail; fi)"
echo "systemd=$(if command -v systemctl >/dev/null 2>&1 && test -d /run/systemd/system; then echo ok; else echo fail; fi)"
echo "base_packages=$(missing=''; for p in curl jq ufw openssl iproute2 tar gzip ca-certificates gnupg lsb-release; do dpkg -s "$p" >/dev/null 2>&1 || missing="$missing,$p"; done; echo "${missing#,}")"
echo "docker=$(if command -v docker >/dev/null 2>&1 && docker version --format '{{.Server.Version}}' >/dev/null 2>&1; then echo ok; else echo missing; fi)"
echo "docker_compose=$(if docker compose version >/dev/null 2>&1; then echo ok; else echo missing; fi)"
echo "disk_mb=$(df -Pm / 2>/dev/null | awk 'NR==2 {print $4}')"
echo "ram_mb=$(awk '/MemTotal/ {printf "%%d", $2/1024}' /proc/meminfo 2>/dev/null)"
echo "firewall=$(if command -v ufw >/dev/null 2>&1; then echo ufw; elif command -v nft >/dev/null 2>&1; then echo nftables; else echo none; fi)"
echo "firewall_active=$(if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q active; then echo yes; else echo no; fi)"
echo "ports=$(ss -ltnH 2>/dev/null | awk '{print $4}' | tr '\n' ',' | sed 's/,$//')"
echo "private_ip=$(hostname -I 2>/dev/null | awk '{print $1}')"
echo "private_interface=$(if [ -z %s ] || ip link show dev %s >/dev/null 2>&1; then echo ok; else echo fail; fi)"
echo "public_interface=$(if [ -z %s ] || ip link show dev %s >/dev/null 2>&1; then echo ok; else echo fail; fi)"`, shellEscape(privateInterface), shellEscape(privateInterface), shellEscape(publicInterface), shellEscape(publicInterface))
}

func parsePreflight(node, stdout string, allowNoFirewall bool, add func(string, string, string, string)) {
	data := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			data[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	switch osID := data["os"]; {
	case strings.HasPrefix(osID, "debian:12") || strings.HasPrefix(osID, "debian:13") || strings.HasPrefix(osID, "ubuntu:22.04") || strings.HasPrefix(osID, "ubuntu:24.04") || strings.HasPrefix(osID, "ubuntu:26.04"):
		add(node, "os", "ok", osID)
	default:
		add(node, "os", "fail", "unsupported OS "+osID)
	}
	if data["sudo"] == "ok" {
		add(node, "sudo", "ok", "non-interactive sudo/root available")
	} else {
		add(node, "sudo", "fail", "non-interactive sudo/root is required")
	}
	if data["apt"] == "ok" {
		add(node, "package-manager", "ok", "apt-get available")
	} else {
		add(node, "package-manager", "fail", "apt-get is required")
	}
	if data["systemd"] == "ok" {
		add(node, "systemd", "ok", "systemd available")
	} else {
		add(node, "systemd", "fail", "systemd/systemctl is required")
	}
	if data["base_packages"] == "" {
		add(node, "base-packages", "ok", "required packages installed")
	} else {
		add(node, "base-packages", "warn", "missing packages can be installed by onboarding/install: "+data["base_packages"])
	}
	if data["docker"] == "ok" {
		add(node, "docker", "ok", "Docker engine available")
	} else {
		add(node, "docker", "warn", "Docker engine missing; onboarding can install it")
	}
	if data["docker_compose"] == "ok" {
		add(node, "docker-compose", "ok", "Docker Compose plugin available")
	} else {
		add(node, "docker-compose", "warn", "Docker Compose plugin missing; Docker preparation can install it")
	}
	if parseInt(data["disk_mb"]) >= 20480 {
		add(node, "disk", "ok", data["disk_mb"]+" MiB free on /")
	} else {
		add(node, "disk", "fail", "at least 20480 MiB free on / is required")
	}
	if parseInt(data["ram_mb"]) >= 2048 {
		add(node, "ram", "ok", data["ram_mb"]+" MiB RAM")
	} else {
		add(node, "ram", "fail", "at least 2048 MiB RAM is required")
	}
	switch data["firewall"] {
	case "ufw":
		if data["firewall_active"] == "yes" {
			add(node, "firewall", "ok", "ufw active")
		} else {
			add(node, "firewall", "warn", "ufw is installed but inactive; install/firewall apply will enable it")
		}
	case "nftables":
		if allowNoFirewall {
			add(node, "firewall", "warn", "nftables-only detected; StackForge firewall management bypassed")
		} else {
			add(node, "firewall", "fail", "nftables-only detected; install ufw or pass --allow-no-firewall")
		}
	default:
		if allowNoFirewall {
			add(node, "firewall", "warn", "no supported firewall detected; bypass requested")
		} else {
			add(node, "firewall", "fail", "ufw is required unless --allow-no-firewall is set")
		}
	}
	for _, port := range []string{":80", ":443", ":5432", ":8500", ":4646", ":8080"} {
		if strings.Contains(data["ports"], port) {
			add(node, "port "+port, "fail", fmt.Sprintf("required port %s is already listening", port))
		}
	}
	if data["private_ip"] == "" {
		add(node, "private-networking", "fail", "could not observe private IP")
	} else {
		add(node, "private-networking", "ok", data["private_ip"])
	}
	if data["private_interface"] == "fail" {
		add(node, "private-interface", "fail", "configured private interface not found")
	}
	if data["public_interface"] == "fail" {
		add(node, "public-interface", "fail", "configured public interface not found")
	}
}

func domainLooksReal(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" || v == "localhost" || v == "change-me" || strings.Contains(v, "example.com") || strings.Contains(v, "invalid") {
		return false
	}
	return strings.Contains(v, ".")
}

func cloudflareEnabled(cfg *config.Config) bool {
	return strings.TrimSpace(cfg.Cloudflare.DefaultZoneID) != "" && strings.TrimSpace(cfg.Cloudflare.DefaultZoneID) != "optional"
}

func parseInt(value string) int {
	var n int
	_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &n)
	return n
}

func shellEscape(value string) string {
	value = strings.ReplaceAll(value, `'`, `'\''`)
	return "'" + value + "'"
}
