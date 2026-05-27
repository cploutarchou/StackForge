package safety

import (
	"errors"
	"net"
	"strings"

	"stackforge/internal/stackforge/config"
)

type Options struct {
	Live               bool
	Production         bool
	ConfirmProduction  bool
	AllowExampleConfig bool
	AllowPublicSSH     bool
}

type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type Report struct {
	Safe     bool      `json:"safe"`
	Findings []Finding `json:"findings"`
}

func Check(cfg *config.Config, opts Options) Report {
	report := Report{Safe: true}
	add := func(severity, code, msg string) {
		report.Findings = append(report.Findings, Finding{Severity: severity, Code: code, Message: msg})
		if severity == "error" {
			report.Safe = false
		}
	}
	if cfg == nil {
		add("error", "config-nil", "config is required")
		return report
	}
	production := opts.Production || strings.EqualFold(cfg.Cluster.Environment, "production")
	if production && !opts.ConfirmProduction {
		add("error", "production-confirmation-required", "production environment requires --confirm-production")
	}
	if strings.EqualFold(cfg.SSH.User, "root") {
		add("warning", "ssh-root", "SSH user is root; prefer a named sudo-capable user for staging and production")
	}
	if !opts.AllowExampleConfig && isExampleClusterName(cfg.Cluster.Name) {
		add("error", "example-cluster-name", "cluster.name must not be example/demo for live installation")
	}
	if opts.Live && !opts.AllowExampleConfig {
		if v := exampleConfigValue(cfg); v != "" {
			add("error", "example-config", "live installation refuses example address/domain value "+v)
		}
	}
	if containsCIDR(cfg.Network.AllowedAdminCIDRs, "0.0.0.0/0") || containsCIDR(cfg.Network.AllowedAdminCIDRs, "::/0") {
		add("error", "public-admin-cidr", "allowed_admin_cidrs must not include 0.0.0.0/0 or ::/0")
	}
	if (containsCIDR(cfg.Network.AllowedSSHCIDRs, "0.0.0.0/0") || containsCIDR(cfg.Network.AllowedSSHCIDRs, "::/0")) && !opts.AllowPublicSSH {
		add("error", "public-ssh-cidr", "allowed_ssh_cidrs must not include 0.0.0.0/0 or ::/0 unless --allow-public-ssh is passed")
	}
	if cfg.Traefik.DashboardEnabled && !cfg.Traefik.DashboardBasicAuth {
		add("error", "traefik-dashboard-auth", "Traefik dashboard is enabled without auth")
	}
	if databasePublic(cfg) {
		add("error", "public-database", "database role node must not use a public address for database traffic")
	}
	if internalAPIPublic(cfg) {
		add("error", "public-internal-api", "Consul/Nomad APIs must be restricted to admin CIDRs and private node traffic")
	}
	if cloudflareEnabled(cfg) && strings.TrimSpace(cfg.Cloudflare.APITokenEnv) == "" {
		add("error", "cloudflare-token-env", "cloudflare.api_token_env is required when Cloudflare is enabled")
	}
	return report
}

func (r Report) Error() error {
	if r.Safe {
		return nil
	}
	var parts []string
	for _, f := range r.Findings {
		if f.Severity == "error" {
			parts = append(parts, f.Code+": "+f.Message)
		}
	}
	return errors.New(strings.Join(parts, "; "))
}

func Errors(cfg *config.Config, opts Options) error {
	return Check(cfg, opts).Error()
}

func isExampleClusterName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "example" || n == "demo" || strings.Contains(n, "example") || strings.Contains(n, "demo")
}

func exampleConfigValue(cfg *config.Config) string {
	for _, n := range cfg.Nodes {
		for _, value := range []string{n.Address, n.PublicAddress} {
			if isExampleIP(value) {
				return value
			}
		}
	}
	for _, value := range []string{cfg.ControlPlane.Domain, cfg.Traefik.DashboardDomain, cfg.Traefik.Email} {
		if isExampleDomain(value) {
			return value
		}
	}
	for _, key := range cfg.ControlPlane.AdminAPIKeys {
		if isPlaceholderSecret(key) {
			return "control_plane.admin_api_keys"
		}
	}
	return ""
}

func isExampleIP(value string) bool {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return false
	}
	for _, cidr := range []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24"} {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

func isExampleDomain(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.TrimPrefix(v, "http://")
	v = strings.TrimPrefix(v, "https://")
	host := strings.Split(v, "/")[0]
	host = strings.Split(host, ":")[0]
	return host == "example.com" || strings.HasSuffix(host, ".example.com") || host == "example.org" || strings.HasSuffix(host, ".example.org") || host == "example.net" || strings.HasSuffix(host, ".example.net") || strings.Contains(host, "change-me")
}

func isPlaceholderSecret(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	return v == "" || v == "change-me" || strings.Contains(v, "example") || strings.Contains(v, "demo")
}

func containsCIDR(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func databasePublic(cfg *config.Config) bool {
	for _, n := range cfg.Nodes {
		if !hasRole(n, "database") {
			continue
		}
		if n.PublicAddress != "" && n.PublicAddress == n.Address {
			return true
		}
		if cfg.Network.AllowPublicInternalCommunication && n.PublicAddress != "" {
			return true
		}
	}
	return false
}

func internalAPIPublic(cfg *config.Config) bool {
	if containsCIDR(cfg.Network.AllowedAdminCIDRs, "0.0.0.0/0") || containsCIDR(cfg.Network.AllowedAdminCIDRs, "::/0") {
		return true
	}
	if cfg.Network.AllowPublicInternalCommunication {
		for _, n := range cfg.Nodes {
			if (hasRole(n, "consul-server") || hasRole(n, "nomad-server")) && n.PublicAddress != "" {
				return true
			}
		}
	}
	return false
}

func cloudflareEnabled(cfg *config.Config) bool {
	return strings.TrimSpace(cfg.Cloudflare.DefaultZoneID) != "" && strings.TrimSpace(cfg.Cloudflare.DefaultZoneID) != "optional"
}

func hasRole(n config.NodeConfig, role string) bool {
	for _, r := range n.Roles {
		if r == role {
			return true
		}
	}
	return false
}
