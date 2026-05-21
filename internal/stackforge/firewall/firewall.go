package firewall

import (
	"fmt"
	"strings"

	"stackforge/internal/stackforge/config"
)

type Rule struct {
	Port     int    `json:"port" yaml:"port"`
	Protocol string `json:"protocol" yaml:"protocol"`
	Source   string `json:"source" yaml:"source"`
	Purpose  string `json:"purpose" yaml:"purpose"`
	Node     string `json:"node" yaml:"node"`
	Exposure string `json:"exposure" yaml:"exposure"`
}

type Plan struct {
	Mode     string   `json:"mode" yaml:"mode"`
	Rules    []Rule   `json:"rules" yaml:"rules"`
	Warnings []string `json:"warnings" yaml:"warnings"`
}

func BuildPlan(cfg *config.Config) (Plan, error) {
	p := Plan{Mode: "ufw"}
	sshCIDRs := cfg.Network.AllowedSSHCIDRs
	if len(sshCIDRs) == 0 {
		return p, fmt.Errorf("allowed_ssh_cidrs is required; refusing to expose SSH broadly")
	}
	adminCIDRs := cfg.Network.AllowedAdminCIDRs
	if len(adminCIDRs) == 0 {
		return p, fmt.Errorf("allowed_admin_cidrs is required; refusing to expose admin APIs broadly")
	}
	nodes := []string{"all"}
	if len(cfg.Nodes) > 0 {
		nodes = nil
		for _, n := range cfg.Nodes {
			nodes = append(nodes, n.Name)
		}
	}
	for _, cidr := range sshCIDRs {
		for _, node := range nodes {
			p.Rules = append(p.Rules, Rule{Node: node, Port: cfg.SSH.Port, Protocol: "tcp", Source: cidr, Purpose: "SSH", Exposure: exposure(cidr)})
		}
	}
	for _, node := range nodes {
		p.Rules = append(p.Rules, Rule{Node: node, Port: 80, Protocol: "tcp", Source: "0.0.0.0/0", Purpose: "public HTTP", Exposure: "public"})
		p.Rules = append(p.Rules, Rule{Node: node, Port: 443, Protocol: "tcp", Source: "0.0.0.0/0", Purpose: "public HTTPS", Exposure: "public"})
	}
	for _, cidr := range adminCIDRs {
		for _, r := range []Rule{
			{Port: cfg.ControlPlane.APIPort, Protocol: "tcp", Source: cidr, Purpose: "StackForge API", Exposure: exposure(cidr)},
			{Port: 8500, Protocol: "tcp", Source: cidr, Purpose: "Consul HTTP/UI", Exposure: exposure(cidr)},
			{Port: 4646, Protocol: "tcp", Source: cidr, Purpose: "Nomad HTTP/UI", Exposure: exposure(cidr)},
		} {
			for _, node := range nodes {
				r.Node = node
				p.Rules = append(p.Rules, r)
			}
		}
	}
	private := privateSource(cfg)
	for _, r := range []Rule{
		{Port: 8300, Protocol: "tcp", Source: private, Purpose: "Consul RPC", Exposure: "private"},
		{Port: 8301, Protocol: "tcp/udp", Source: private, Purpose: "Consul LAN gossip", Exposure: "private"},
		{Port: 8302, Protocol: "tcp/udp", Source: private, Purpose: "Consul WAN gossip", Exposure: "private"},
		{Port: 8600, Protocol: "tcp/udp", Source: private, Purpose: "Consul DNS", Exposure: "private"},
		{Port: 4647, Protocol: "tcp", Source: private, Purpose: "Nomad RPC", Exposure: "private"},
		{Port: 4648, Protocol: "tcp/udp", Source: private, Purpose: "Nomad Serf", Exposure: "private"},
		{Port: databasePort(cfg.Database.Engine), Protocol: "tcp", Source: private, Purpose: cfg.Database.Engine + " database private only", Exposure: "private"},
	} {
		for _, node := range nodes {
			r.Node = node
			p.Rules = append(p.Rules, r)
		}
	}
	if cfg.Traefik.DashboardEnabled {
		for _, cidr := range adminCIDRs {
			for _, node := range nodes {
				p.Rules = append(p.Rules, Rule{Node: node, Port: 8080, Protocol: "tcp", Source: cidr, Purpose: "Traefik dashboard", Exposure: exposure(cidr)})
			}
		}
	}
	if err := Validate(p); err != nil {
		return p, err
	}
	return p, nil
}

func UFWCommands(plan Plan) []string {
	cmds := []string{"ufw --force reset", "ufw default deny incoming", "ufw default allow outgoing"}
	for _, r := range plan.Rules {
		protos := strings.Split(r.Protocol, "/")
		for _, proto := range protos {
			cmds = append(cmds, fmt.Sprintf("ufw allow from %s to any port %d proto %s comment 'StackForge %s'", r.Source, r.Port, proto, r.Purpose))
		}
	}
	cmds = append(cmds, "ufw --force enable")
	return cmds
}

func UFWDetectCommand() string {
	return "if command -v ufw >/dev/null 2>&1; then exit 0; fi; if command -v nft >/dev/null 2>&1; then echo 'nftables-only firewall detected; StackForge supports UFW live management only' >&2; exit 2; fi; echo 'ufw is required unless --allow-no-firewall is set' >&2; exit 1"
}

func BackupCommand() string {
	return "install -d -m 0700 /var/lib/stackforge/firewall && ufw status verbose > /var/lib/stackforge/firewall/ufw-before-$(date -u +%Y%m%dT%H%M%SZ).txt || true"
}

func VerifyUFWCommand() string {
	return "ufw status | grep -q active && ! ufw status numbered | grep -E '5432|8500|4646|8080' | grep -q 'Anywhere'"
}

func DetectModeCommand() string {
	return "if command -v ufw >/dev/null 2>&1; then echo ufw; elif command -v nft >/dev/null 2>&1; then echo nftables; else echo none; fi"
}

func Validate(plan Plan) error {
	for _, r := range plan.Rules {
		if r.Source == "0.0.0.0/0" || r.Source == "::/0" {
			switch r.Port {
			case 80, 443:
				continue
			}
			purpose := strings.ToLower(r.Purpose)
			if strings.Contains(purpose, "database") || strings.Contains(purpose, "nomad") || strings.Contains(purpose, "consul") || strings.Contains(purpose, "dashboard") {
				return fmt.Errorf("refusing public exposure for %s on port %d", r.Purpose, r.Port)
			}
		}
	}
	return nil
}

func databasePort(engine string) int {
	if engine == "mysql" {
		return 3306
	}
	return 5432
}

func privateSource(cfg *config.Config) string {
	if len(cfg.Nodes) == 1 {
		return cfg.Nodes[0].Address + "/32"
	}
	return "10.0.0.0/8"
}

func exposure(cidr string) string {
	if cidr == "0.0.0.0/0" || cidr == "::/0" {
		return "public"
	}
	if strings.HasPrefix(cidr, "10.") || strings.HasPrefix(cidr, "192.168.") || strings.HasPrefix(cidr, "172.16.") || strings.HasPrefix(cidr, "172.17.") || strings.HasPrefix(cidr, "172.18.") || strings.HasPrefix(cidr, "172.19.") || strings.HasPrefix(cidr, "172.2") || strings.HasPrefix(cidr, "172.30.") || strings.HasPrefix(cidr, "172.31.") {
		return "private"
	}
	return "restricted"
}
