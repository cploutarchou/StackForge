package inventory

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Inventory struct {
	ClusterName           string            `yaml:"cluster_name" json:"cluster_name"`
	Environment           string            `yaml:"environment" json:"environment"`
	Datacenter            string            `yaml:"datacenter" json:"datacenter"`
	Nodes                 []Node            `yaml:"nodes" json:"nodes"`
	ComponentVersions     map[string]string `yaml:"component_versions" json:"component_versions"`
	ServiceStatus         map[string]string `yaml:"service_status" json:"service_status"`
	ConsulEndpoints       []string          `yaml:"consul_endpoints" json:"consul_endpoints"`
	NomadEndpoints        []string          `yaml:"nomad_endpoints" json:"nomad_endpoints"`
	TraefikEndpoints      []string          `yaml:"traefik_endpoints" json:"traefik_endpoints"`
	DatabaseEndpoint      string            `yaml:"database_endpoint" json:"database_endpoint"`
	ControlPlaneEndpoint  string            `yaml:"control_plane_endpoint" json:"control_plane_endpoint"`
	FirewallMode          string            `yaml:"firewall_mode" json:"firewall_mode"`
	InstallStatus         string            `yaml:"install_status" json:"install_status"`
	LastSuccessfulStep    string            `yaml:"last_successful_install_step" json:"last_successful_install_step"`
	FailedInstallStep     string            `yaml:"failed_install_step" json:"failed_install_step"`
	LastBackupID          string            `yaml:"last_backup_id" json:"last_backup_id"`
	LastRestoreID         string            `yaml:"last_restore_id" json:"last_restore_id"`
	LastHealthCheckStatus string            `yaml:"last_health_check_status" json:"last_health_check_status"`
	LastHealthCheckAt     time.Time         `yaml:"last_health_check_at" json:"last_health_check_at"`
	CreatedAt             time.Time         `yaml:"created_at" json:"created_at"`
	UpdatedAt             time.Time         `yaml:"updated_at" json:"updated_at"`
	Warnings              []string          `yaml:"warnings" json:"warnings"`
}

type Node struct {
	Name         string            `yaml:"name" json:"name"`
	Roles        []string          `yaml:"roles" json:"roles"`
	PrivateIP    string            `yaml:"private_ip" json:"private_ip"`
	PublicIP     string            `yaml:"public_ip" json:"public_ip"`
	SSH          SSHInfo           `yaml:"ssh" json:"ssh"`
	OSName       string            `yaml:"os_name" json:"os_name"`
	OSVersion    string            `yaml:"os_version" json:"os_version"`
	Kernel       string            `yaml:"kernel" json:"kernel"`
	ObservedIPs  []string          `yaml:"observed_ips" json:"observed_ips"`
	Components   map[string]string `yaml:"components" json:"components"`
	Services     map[string]string `yaml:"services" json:"services"`
	Leaders      map[string]string `yaml:"leaders" json:"leaders"`
	Versions     map[string]string `yaml:"versions" json:"versions"`
	Listening    []string          `yaml:"listening_ports" json:"listening_ports"`
	Firewall     string            `yaml:"firewall_backend" json:"firewall_backend"`
	LastObserved time.Time         `yaml:"last_observed_at" json:"last_observed_at"`
	Warnings     []string          `yaml:"warnings" json:"warnings"`
	Endpoints    []string          `yaml:"endpoints" json:"endpoints"`
	HealthStatus string            `yaml:"health_status" json:"health_status"`
}

type SSHInfo struct {
	User           string `yaml:"user" json:"user"`
	Port           int    `yaml:"port" json:"port"`
	PrivateKeyPath string `yaml:"private_key_path" json:"private_key_path"`
}

func Load(path string) (*Inventory, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var inv Inventory
	return &inv, yaml.Unmarshal(b, &inv)
}

func Save(path string, inv *Inventory) error {
	Normalize(inv)
	now := time.Now().UTC()
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = now
	}
	inv.UpdatedAt = now
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := yaml.Marshal(inv)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

func Normalize(inv *Inventory) {
	if inv == nil {
		return
	}
	if inv.ComponentVersions == nil {
		inv.ComponentVersions = map[string]string{}
	}
	if inv.ServiceStatus == nil {
		inv.ServiceStatus = map[string]string{}
	}
	for i := range inv.Nodes {
		if inv.Nodes[i].Components == nil {
			inv.Nodes[i].Components = map[string]string{}
		}
		if inv.Nodes[i].Services == nil {
			inv.Nodes[i].Services = map[string]string{}
		}
		if inv.Nodes[i].Leaders == nil {
			inv.Nodes[i].Leaders = map[string]string{}
		}
		if inv.Nodes[i].Versions == nil {
			inv.Nodes[i].Versions = map[string]string{}
		}
	}
}

func MarkStepSuccess(inv *Inventory, stepID string) {
	Normalize(inv)
	inv.LastSuccessfulStep = stepID
	inv.FailedInstallStep = ""
	inv.InstallStatus = "installing"
}

func MarkStepFailure(inv *Inventory, stepID string, warning string) {
	Normalize(inv)
	inv.FailedInstallStep = stepID
	inv.InstallStatus = "failed"
	if warning != "" {
		inv.Warnings = append(inv.Warnings, warning)
	}
}

func MarkBackup(inv *Inventory, backupID string, warnings []string) {
	Normalize(inv)
	inv.LastBackupID = backupID
	inv.Warnings = append(inv.Warnings, warnings...)
}

func MarkRestore(inv *Inventory, restoreID string, warnings []string) {
	Normalize(inv)
	inv.LastRestoreID = restoreID
	inv.Warnings = append(inv.Warnings, warnings...)
}

func MarkHealthCheck(inv *Inventory, status string, warnings []string) {
	Normalize(inv)
	inv.LastHealthCheckStatus = status
	inv.LastHealthCheckAt = time.Now().UTC()
	inv.Warnings = append(inv.Warnings, warnings...)
}
