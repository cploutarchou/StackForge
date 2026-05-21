package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"stackforge/internal/stackforge/remoteexec"

	"gopkg.in/yaml.v3"
)

type Secrets struct {
	ConsulGossipKey          string    `yaml:"consul_gossip_key" json:"consul_gossip_key"`
	ConsulBootstrapToken     string    `yaml:"consul_bootstrap_token" json:"consul_bootstrap_token"`
	NomadBootstrapToken      string    `yaml:"nomad_bootstrap_token" json:"nomad_bootstrap_token"`
	StackForgeAdminAPIKey    string    `yaml:"stackforge_admin_api_key" json:"stackforge_admin_api_key"`
	DatabasePassword         string    `yaml:"database_password" json:"database_password"`
	TraefikDashboardPassword string    `yaml:"traefik_dashboard_password,omitempty" json:"traefik_dashboard_password,omitempty"`
	InternalServiceToken     string    `yaml:"internal_service_token" json:"internal_service_token"`
	CreatedAt                time.Time `yaml:"created_at" json:"created_at"`
}

func LoadOrGenerate(path string, adminKeys []string, traefikDashboard bool) (*Secrets, bool, error) {
	if b, err := os.ReadFile(path); err == nil {
		var s Secrets
		if err := yaml.Unmarshal(b, &s); err != nil {
			return nil, false, err
		}
		return &s, false, nil
	}
	s := &Secrets{CreatedAt: time.Now().UTC()}
	var err error
	if s.ConsulGossipKey, err = token(32); err != nil {
		return nil, false, err
	}
	if s.ConsulBootstrapToken, err = token(32); err != nil {
		return nil, false, err
	}
	if s.NomadBootstrapToken, err = token(32); err != nil {
		return nil, false, err
	}
	if len(adminKeys) > 0 && strings.TrimSpace(adminKeys[0]) != "" && adminKeys[0] != "change-me" {
		s.StackForgeAdminAPIKey = adminKeys[0]
	} else if s.StackForgeAdminAPIKey, err = token(32); err != nil {
		return nil, false, err
	}
	if s.DatabasePassword, err = token(30); err != nil {
		return nil, false, err
	}
	if traefikDashboard {
		if s.TraefikDashboardPassword, err = token(24); err != nil {
			return nil, false, err
		}
	}
	if s.InternalServiceToken, err = token(32); err != nil {
		return nil, false, err
	}
	if err := Save(path, s); err != nil {
		return nil, false, err
	}
	return s, true, nil
}

func Save(path string, s *Secrets) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

func Redact(in string) string {
	if in == "" {
		return in
	}
	return "[REDACTED]"
}

func (s *Secrets) Values() []string {
	if s == nil {
		return nil
	}
	return []string{
		s.ConsulGossipKey,
		s.ConsulBootstrapToken,
		s.NomadBootstrapToken,
		s.StackForgeAdminAPIKey,
		s.DatabasePassword,
		s.TraefikDashboardPassword,
		s.InternalServiceToken,
	}
}

func (s *Secrets) Env(databaseURL string) string {
	if s == nil {
		return ""
	}
	lines := []string{
		"STACKFORGE_ENV=production",
		"STACKFORGE_ADMIN_API_KEYS=" + shellValue(s.StackForgeAdminAPIKey),
		"STACKFORGE_INTERNAL_SERVICE_TOKEN=" + shellValue(s.InternalServiceToken),
		"CONSUL_HTTP_TOKEN=" + shellValue(s.ConsulBootstrapToken),
		"NOMAD_TOKEN=" + shellValue(s.NomadBootstrapToken),
		"STACKFORGE_DATABASE_PASSWORD=" + shellValue(s.DatabasePassword),
	}
	if databaseURL != "" {
		lines = append(lines, "DATABASE_URL="+shellValue(databaseURL))
	}
	if s.TraefikDashboardPassword != "" {
		lines = append(lines, "TRAEFIK_DASHBOARD_PASSWORD="+shellValue(s.TraefikDashboardPassword))
	}
	return strings.Join(lines, "\n") + "\n"
}

func DeployEnvCommand(env string, secretValues []string) remoteexec.Command {
	encoded := base64.StdEncoding.EncodeToString([]byte(env))
	return remoteexec.Command{
		Command: fmt.Sprintf("install -d -m 0750 /etc/stackforge && printf %%s %s | base64 -d > /etc/stackforge/stackforge.env && chown root:root /etc/stackforge/stackforge.env && chmod 0600 /etc/stackforge/stackforge.env", shellValue(encoded)),
		Sudo:    true,
		Timeout: 30 * time.Second,
		Secrets: append([]string{encoded}, secretValues...),
	}
}

func VerifyRemoteEnvPermissionsCommand() remoteexec.Command {
	return remoteexec.Command{
		Command: "test -f /etc/stackforge/stackforge.env && stat -c '%U:%G %a' /etc/stackforge/stackforge.env | grep -Eq '^root:root 600$|^root:root 640$'",
		Sudo:    true,
		Timeout: 15 * time.Second,
	}
}

func RedactAll(in string, values []string) string {
	return remoteexec.Redact(in, values)
}

func shellValue(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func token(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
