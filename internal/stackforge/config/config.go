package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Cluster      ClusterConfig      `yaml:"cluster" json:"cluster"`
	SSH          SSHConfig          `yaml:"ssh" json:"ssh"`
	Nodes        []NodeConfig       `yaml:"nodes" json:"nodes"`
	Network      NetworkConfig      `yaml:"network" json:"network"`
	Consul       ComponentConfig    `yaml:"consul" json:"consul"`
	Nomad        ComponentConfig    `yaml:"nomad" json:"nomad"`
	Traefik      TraefikConfig      `yaml:"traefik" json:"traefik"`
	Database     DatabaseConfig     `yaml:"database" json:"database"`
	ControlPlane ControlPlaneConfig `yaml:"control_plane" json:"control_plane"`
	Cloudflare   CloudflareConfig   `yaml:"cloudflare" json:"cloudflare"`
}

type ClusterConfig struct {
	Name        string `yaml:"name" json:"name"`
	Environment string `yaml:"environment" json:"environment"`
	Datacenter  string `yaml:"datacenter" json:"datacenter"`
}

type SSHConfig struct {
	User           string `yaml:"user" json:"user"`
	Port           int    `yaml:"port" json:"port"`
	PrivateKeyPath string `yaml:"private_key_path" json:"private_key_path"`
	CopyPublicKey  bool   `yaml:"copy_public_key" json:"copy_public_key"`
}

type NodeConfig struct {
	Name          string   `yaml:"name" json:"name"`
	Address       string   `yaml:"address" json:"address"`
	PublicAddress string   `yaml:"public_address" json:"public_address"`
	Roles         []string `yaml:"roles" json:"roles"`
}

type NetworkConfig struct {
	PrivateInterface                 string   `yaml:"private_interface" json:"private_interface"`
	PublicInterface                  string   `yaml:"public_interface" json:"public_interface"`
	AllowedAdminCIDRs                []string `yaml:"allowed_admin_cidrs" json:"allowed_admin_cidrs"`
	AllowedSSHCIDRs                  []string `yaml:"allowed_ssh_cidrs" json:"allowed_ssh_cidrs"`
	AllowPublicInternalCommunication bool     `yaml:"allow_public_internal_communication" json:"allow_public_internal_communication"`
}

type ComponentConfig struct {
	Version       string `yaml:"version" json:"version"`
	ACLEnabled    bool   `yaml:"acl_enabled" json:"acl_enabled"`
	EncryptGossip bool   `yaml:"encrypt_gossip" json:"encrypt_gossip"`
	UIEnabled     bool   `yaml:"ui_enabled" json:"ui_enabled"`
	ClientEnabled bool   `yaml:"client_enabled" json:"client_enabled"`
}

type TraefikConfig struct {
	Version            string         `yaml:"version" json:"version"`
	Entrypoints        map[string]int `yaml:"entrypoints" json:"entrypoints"`
	CertResolver       string         `yaml:"cert_resolver" json:"cert_resolver"`
	Email              string         `yaml:"email" json:"email"`
	DashboardEnabled   bool           `yaml:"dashboard_enabled" json:"dashboard_enabled"`
	DashboardDomain    string         `yaml:"dashboard_domain" json:"dashboard_domain"`
	DashboardBasicAuth bool           `yaml:"dashboard_basic_auth" json:"dashboard_basic_auth"`
}

type DatabaseConfig struct {
	Engine         string `yaml:"engine" json:"engine"`
	Mode           string `yaml:"mode" json:"mode"`
	BackupEnabled  bool   `yaml:"backup_enabled" json:"backup_enabled"`
	BackupSchedule string `yaml:"backup_schedule" json:"backup_schedule"`
}

type ControlPlaneConfig struct {
	Domain                    string   `yaml:"domain" json:"domain"`
	APIPort                   int      `yaml:"api_port" json:"api_port"`
	AdminAPIKeys              []string `yaml:"admin_api_keys" json:"admin_api_keys"`
	ReconcilerEnabled         bool     `yaml:"reconciler_enabled" json:"reconciler_enabled"`
	ReconcilerIntervalSeconds int      `yaml:"reconciler_interval_seconds" json:"reconciler_interval_seconds"`
}

type CloudflareConfig struct {
	APITokenEnv   string `yaml:"api_token_env" json:"api_token_env"`
	DefaultZoneID string `yaml:"default_zone_id" json:"default_zone_id"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(expandHome(path))
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Cluster.Environment == "" {
		cfg.Cluster.Environment = "production"
	}
	if cfg.Cluster.Datacenter == "" {
		cfg.Cluster.Datacenter = "dc1"
	}
	if cfg.SSH.User == "" {
		cfg.SSH.User = "root"
	}
	if cfg.SSH.Port == 0 {
		cfg.SSH.Port = 22
	}
	if cfg.ControlPlane.APIPort == 0 {
		cfg.ControlPlane.APIPort = 8080
	}
	if cfg.Database.Engine == "" {
		cfg.Database.Engine = "postgres"
	}
	if cfg.Consul.Version == "" {
		cfg.Consul.Version = "latest-stable"
	}
	if cfg.Nomad.Version == "" {
		cfg.Nomad.Version = "latest-stable"
	}
	if cfg.Traefik.Version == "" {
		cfg.Traefik.Version = "latest-stable"
	}
}

func Validate(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if cfg.Cluster.Name == "" {
		return errors.New("cluster.name is required")
	}
	if len(cfg.Nodes) == 0 {
		return errors.New("at least one node is required")
	}
	if cfg.ControlPlane.Domain == "" {
		return errors.New("control_plane.domain is required")
	}
	if len(cfg.ControlPlane.AdminAPIKeys) == 0 {
		return errors.New("control_plane.admin_api_keys must contain at least one key or be generated by install")
	}
	if cfg.Database.Engine != "postgres" && cfg.Database.Engine != "mysql" && cfg.Database.Engine != "sqlite" {
		return fmt.Errorf("database.engine must be postgres, mysql, or sqlite, got %q", cfg.Database.Engine)
	}
	roles := map[string]int{}
	for _, n := range cfg.Nodes {
		if n.Name == "" || n.Address == "" {
			return errors.New("each node requires name and address")
		}
		for _, r := range n.Roles {
			roles[r]++
		}
		if !cfg.Network.AllowPublicInternalCommunication && n.PublicAddress != "" && n.PublicAddress == n.Address {
			return fmt.Errorf("node %s uses public address for internal communication; set allow_public_internal_communication only when intentional", n.Name)
		}
	}
	if cfg.Database.Engine != "sqlite" && roles["database"] == 0 {
		return errors.New("at least one database node is required")
	}
	if roles["control-plane"] == 0 {
		return errors.New("at least one control-plane node is required")
	}
	if roles["consul-server"]%2 == 0 {
		return fmt.Errorf("consul-server count should be odd for quorum, got %d", roles["consul-server"])
	}
	if roles["nomad-server"]%2 == 0 {
		return fmt.Errorf("nomad-server count should be odd for quorum, got %d", roles["nomad-server"])
	}
	if cfg.Traefik.DashboardEnabled && !cfg.Traefik.DashboardBasicAuth {
		return errors.New("traefik dashboard requires basic auth when enabled")
	}
	for _, cidr := range append(cfg.Network.AllowedAdminCIDRs, cfg.Network.AllowedSSHCIDRs...) {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid CIDR %q: %w", cidr, err)
		}
	}
	return nil
}

func StateDir(base, cluster string) string {
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".stackforge")
	}
	return filepath.Join(expandHome(base), cluster)
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
