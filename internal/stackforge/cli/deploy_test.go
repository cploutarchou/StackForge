package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"stackforge/internal/stackforge/inventory"
)

func TestLoadDeployManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stackforge-deployment.yaml")
	content := []byte("name: test-app\nservices:\n  web:\n    image: nginx:alpine\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	gotBytes, manifest, err := loadDeployManifest(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if string(gotBytes) != string(content) {
		t.Fatalf("manifest bytes mismatch")
	}
	if manifest.Name != "test-app" {
		t.Fatalf("unexpected manifest name: %q", manifest.Name)
	}
	if len(manifest.Services) != 1 {
		t.Fatalf("expected one service, got %d", len(manifest.Services))
	}
}

func TestLoadDeployManifestRejectsNoServices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stackforge-deployment.yaml")
	if err := os.WriteFile(path, []byte("name: empty\nservices: {}\n"), 0600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, _, err := loadDeployManifest(path); err == nil {
		t.Fatal("expected error for empty services")
	}
}

func TestLoadDeployManifestWithAutoDNS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stackforge-deployment.yaml")
	content := []byte("name: test-app\nservices:\n  web:\n    image: nginx:alpine\nauto_dns:\n  enabled: true\n  app_domain: app.example.com\n  api_domain: api.example.com\n  domains:\n    - admin.example.com\n  zone_id: zone-123\n  proxied: true\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, manifest, err := loadDeployManifest(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if manifest.AutoDNS.Enabled == nil || !*manifest.AutoDNS.Enabled {
		t.Fatalf("expected auto_dns.enabled=true, got %+v", manifest.AutoDNS.Enabled)
	}
	if manifest.AutoDNS.Proxied == nil || !*manifest.AutoDNS.Proxied {
		t.Fatalf("expected auto_dns.proxied=true, got %+v", manifest.AutoDNS.Proxied)
	}
	if manifest.AutoDNS.ZoneID != "zone-123" {
		t.Fatalf("expected auto_dns.zone_id=zone-123, got %q", manifest.AutoDNS.ZoneID)
	}
}

func TestSelectDeployNode(t *testing.T) {
	inv := &inventory.Inventory{Nodes: []inventory.Node{
		{Name: "node-1", Roles: []string{"nomad-client"}, PrivateIP: "10.0.0.11"},
		{Name: "node-2", Roles: []string{"control-plane", "nomad-server"}, PrivateIP: "10.0.0.12"},
	}}
	picked, err := selectDeployNode(inv, "")
	if err != nil {
		t.Fatalf("select deploy node: %v", err)
	}
	if picked.Name != "node-2" {
		t.Fatalf("expected control-plane node, got %s", picked.Name)
	}

	override, err := selectDeployNode(inv, "node-1")
	if err != nil {
		t.Fatalf("select override node: %v", err)
	}
	if override.Name != "node-1" {
		t.Fatalf("expected node-1, got %s", override.Name)
	}
}

func TestSanitizeDeployName(t *testing.T) {
	if got := sanitizeDeployName("  dydx Trading Bot@Prod "); got != "dydx-trading-bot-prod" {
		t.Fatalf("unexpected sanitized name: %q", got)
	}
}

func TestParseEnvFile(t *testing.T) {
	env := []byte("# comment\nexport APP_DOMAIN=app.example.com\nAPI_DOMAIN=api.example.com\nQUOTED=\"abc\"\n")
	got := parseEnvFile(env)
	if got["APP_DOMAIN"] != "app.example.com" {
		t.Fatalf("unexpected APP_DOMAIN: %q", got["APP_DOMAIN"])
	}
	if got["API_DOMAIN"] != "api.example.com" {
		t.Fatalf("unexpected API_DOMAIN: %q", got["API_DOMAIN"])
	}
	if got["QUOTED"] != "abc" {
		t.Fatalf("unexpected QUOTED: %q", got["QUOTED"])
	}
}

func TestDeployDomainsFromFlagsAndEnv(t *testing.T) {
	env := map[string]string{
		"APP_DOMAIN":       "app.example.com",
		"API_DOMAIN":       "api.example.com",
		"VITE_API_URL":     "https://api.example.com",
		"VITE_CLIENT_HOST": "app.example.com",
	}
	got := deployDomains("portal.example.com", "", nil, env)
	want := []string{"portal.example.com", "app.example.com", "api.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected domains:\nwant=%v\n got=%v", want, got)
	}
}

func TestResolveDeployDNSConfigUsesManifestWhenFlagsNotChanged(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Bool("auto-dns", true, "")
	cmd.Flags().Bool("dns-proxied", false, "")

	enabled := false
	proxied := true
	manifest := deployManifest{AutoDNS: deployAutoDNS{Enabled: &enabled, Proxied: &proxied, AppDomain: "manifest-app.example.com", APIDomain: "manifest-api.example.com", Domains: []string{"admin.example.com"}, ZoneID: "zone-manifest"}}
	resolved := resolveDeployDNSConfig(cmd, manifest, map[string]string{"APP_DOMAIN": "env-app.example.com"}, "", "", "", true, false)

	if resolved.Enabled {
		t.Fatalf("expected manifest enabled=false to be applied")
	}
	if !resolved.Proxied {
		t.Fatalf("expected manifest proxied=true to be applied")
	}
	if resolved.ZoneID != "zone-manifest" {
		t.Fatalf("unexpected zone id: %q", resolved.ZoneID)
	}
	wantDomains := []string{"manifest-app.example.com", "manifest-api.example.com", "admin.example.com", "env-app.example.com"}
	if !reflect.DeepEqual(resolved.Domains, wantDomains) {
		t.Fatalf("unexpected domains:\nwant=%v\n got=%v", wantDomains, resolved.Domains)
	}
}

func TestWriteDeployScaffold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stackforge-deployment.yaml")

	created, err := writeDeployScaffold(path, false, false)
	if err != nil {
		t.Fatalf("write scaffold: %v", err)
	}
	if created["deployment_file"] != path {
		t.Fatalf("unexpected deployment path: %q", created["deployment_file"])
	}
	envPath := filepath.Join(dir, ".env.stackforge")
	if created["env_file"] != envPath {
		t.Fatalf("unexpected env path: %q", created["env_file"])
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	s := string(b)
	var parsed map[string]any
	if err := yaml.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("generated template is invalid yaml: %v", err)
	}
	for _, want := range []string{"auto_dns:", "enabled: true", "proxied: true", "services:", "api:", "frontend:"} {
		if !strings.Contains(s, want) {
			t.Fatalf("expected template to contain %q", want)
		}
	}

	envBytes, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env template: %v", err)
	}
	envText := string(envBytes)
	for _, want := range []string{"SECRET_KEY=", "APP_DOMAIN=app.example.com", "VITE_API_URL=https://${API_DOMAIN}"} {
		if !strings.Contains(envText, want) {
			t.Fatalf("expected env template to contain %q", want)
		}
	}

	if _, err := writeDeployScaffold(path, false, false); err == nil {
		t.Fatal("expected overwrite guard error")
	}
	if _, err := writeDeployScaffold(path, true, false); err != nil {
		t.Fatalf("force overwrite scaffold: %v", err)
	}
}

func TestDeployTemplateEnvRandomSecrets(t *testing.T) {
	envContent, err := deployTemplateEnv(true)
	if err != nil {
		t.Fatalf("deployTemplateEnv(true): %v", err)
	}
	parsed := parseEnvFile([]byte(envContent))
	for _, key := range []string{"SECRET_KEY", "JWT_SECRET_KEY", "ENCRYPTION_KEY", "APP_DB_PASSWORD"} {
		value := parsed[key]
		if value == "" {
			t.Fatalf("expected %s to be set", key)
		}
		if strings.Contains(value, "replace-") || strings.Contains(value, "change-me") {
			t.Fatalf("expected %s to be randomized, got %q", key, value)
		}
	}
	if len(parsed["SECRET_KEY"]) != 64 || len(parsed["JWT_SECRET_KEY"]) != 64 || len(parsed["ENCRYPTION_KEY"]) != 64 {
		t.Fatalf("expected 64-char hex secrets, got SECRET_KEY=%d JWT_SECRET_KEY=%d ENCRYPTION_KEY=%d", len(parsed["SECRET_KEY"]), len(parsed["JWT_SECRET_KEY"]), len(parsed["ENCRYPTION_KEY"]))
	}
	if len(parsed["APP_DB_PASSWORD"]) != 48 {
		t.Fatalf("expected 48-char hex APP_DB_PASSWORD, got %d", len(parsed["APP_DB_PASSWORD"]))
	}
}

func TestMaskSecret(t *testing.T) {
	if got := maskSecret(""); got != "" {
		t.Fatalf("expected empty mask for empty input, got %q", got)
	}
	if got := maskSecret("1234567"); got != "****" {
		t.Fatalf("expected short secret mask, got %q", got)
	}
	if got := maskSecret("1234567890abcdef"); got != "1234...cdef" {
		t.Fatalf("unexpected mask: %q", got)
	}
}

func TestMaskedSecretsSummary(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env.stackforge")
	env := "SECRET_KEY=abcdef0123456789\nJWT_SECRET_KEY=0011223344556677\nENCRYPTION_KEY=8899aabbccddeeff\nAPP_DB_PASSWORD=password1234\n"
	if err := os.WriteFile(envPath, []byte(env), 0600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	summary, err := maskedSecretsSummary(envPath)
	if err != nil {
		t.Fatalf("maskedSecretsSummary: %v", err)
	}
	if summary["SECRET_KEY"] != "abcd...6789" {
		t.Fatalf("unexpected SECRET_KEY summary: %q", summary["SECRET_KEY"])
	}
	if summary["APP_DB_PASSWORD"] != "pass...1234" {
		t.Fatalf("unexpected APP_DB_PASSWORD summary: %q", summary["APP_DB_PASSWORD"])
	}
}

func TestEffectiveSecretsSummaryMode(t *testing.T) {
	if got := effectiveSecretsSummaryMode(true, true); got {
		t.Fatal("quiet mode should disable secrets summary")
	}
	if got := effectiveSecretsSummaryMode(false, true); !got {
		t.Fatal("expected summary enabled when quiet=false and print-secrets-summary=true")
	}
	if got := effectiveSecretsSummaryMode(false, false); got {
		t.Fatal("expected summary disabled when flag is false")
	}
}

func TestDeployRegistryLogins(t *testing.T) {
	env := map[string]string{
		"GHCR_USERNAME":            "octocat",
		"GHCR_TOKEN":               "gh-token",
		"DOCKERHUB_USERNAME":       "dockeruser",
		"DOCKERHUB_TOKEN":          "docker-token",
		"DOCKER_REGISTRY":          "registry.example.com",
		"DOCKER_REGISTRY_USERNAME": "custom-user",
		"DOCKER_REGISTRY_PASSWORD": "custom-pass",
		"GITHUB_API_TOKEN":         "api-token-ignored-when-ghcr-token-present",
	}
	logins := deployRegistryLogins(env)
	if len(logins) != 3 {
		t.Fatalf("expected 3 registry logins, got %d", len(logins))
	}
	names := registryLoginNames(logins)
	wantNames := []string{"ghcr", "dockerhub", "custom"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("unexpected login names: want=%v got=%v", wantNames, names)
	}
}

func TestDeployRegistryLoginsFallsBackToGitHubAPIToken(t *testing.T) {
	env := map[string]string{
		"GHCR_USERNAME":    "octocat",
		"GITHUB_API_TOKEN": "api-token",
	}
	logins := deployRegistryLogins(env)
	if len(logins) != 1 {
		t.Fatalf("expected 1 login, got %d", len(logins))
	}
	if logins[0].Name != "ghcr" || logins[0].Password != "api-token" {
		t.Fatalf("unexpected GHCR fallback login: %+v", logins[0])
	}
}

func TestBuildDockerLoginCommand(t *testing.T) {
	cmd := buildDockerLoginCommand(registryLogin{Name: "dockerhub", Username: "user", Password: "pass"})
	if !strings.Contains(cmd, "docker login -u") || strings.Contains(cmd, "registry.example.com") {
		t.Fatalf("unexpected dockerhub login command: %s", cmd)
	}
	custom := buildDockerLoginCommand(registryLogin{Name: "custom", Registry: "registry.example.com", Username: "user", Password: "pass"})
	if !strings.Contains(custom, "registry.example.com") {
		t.Fatalf("expected custom registry in command, got: %s", custom)
	}
}
