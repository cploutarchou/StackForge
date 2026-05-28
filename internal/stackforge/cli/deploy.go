package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"stackforge/internal/controlplane/dns/cloudflare"
	"stackforge/internal/stackforge/config"
	"stackforge/internal/stackforge/domainpool"
	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/remoteexec"
)

type deployManifest struct {
	Name     string         `yaml:"name"`
	Services map[string]any `yaml:"services"`
	AutoDNS  deployAutoDNS  `yaml:"auto_dns"`
}

type deployAutoDNS struct {
	Enabled   *bool    `yaml:"enabled"`
	AppDomain string   `yaml:"app_domain"`
	APIDomain string   `yaml:"api_domain"`
	Domains   []string `yaml:"domains"`
	ZoneID    string   `yaml:"zone_id"`
	Proxied   *bool    `yaml:"proxied"`
}

func deployCmd() *cobra.Command {
	var filePath string
	var envFile string
	var nodeName string
	var appDomain string
	var apiDomain string
	var dnsZoneID string
	var dnsProxied bool
	var autoDNS bool
	var noBuild bool
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy an application manifest to a StackForge node",
		RunE: func(cmd *cobra.Command, args []string) error {
			manifestPath := strings.TrimSpace(filePath)
			envPath := strings.TrimSpace(envFile)
			nodeOverride := strings.TrimSpace(nodeName)
			if manifestPath == "" {
				return fmt.Errorf("--file is required")
			}
			if envPath == "" {
				envPath = ".env.stackforge"
			}

			manifestBytes, manifest, err := loadDeployManifest(manifestPath)
			if err != nil {
				return err
			}

			var envBytes []byte
			envVars := map[string]string{}
			if envPath != "" {
				envBytes, err = os.ReadFile(envPath)
				if err != nil {
					return fmt.Errorf("read env file %s: %w", envPath, err)
				}
				envVars = parseEnvFile(envBytes)
			}

			inv, err := inventoryForDeploy()
			if err != nil {
				return err
			}

			target, err := selectDeployNode(inv, nodeOverride)
			if err != nil {
				return err
			}

			deployName := sanitizeDeployName(manifest.Name)
			if deployName == "" {
				deployName = sanitizeDeployName(strings.TrimSuffix(filepath.Base(manifestPath), filepath.Ext(manifestPath)))
			}
			if deployName == "" {
				return fmt.Errorf("unable to derive deploy name from manifest")
			}

			remoteDir := "/opt/stackforge/deployments/" + deployName
			remoteManifestPath := filepath.ToSlash(filepath.Join(remoteDir, "stackforge-deployment.yaml"))
			remoteEnvPath := filepath.ToSlash(filepath.Join(remoteDir, ".env.stackforge"))
			registryLogins := deployRegistryLogins(envVars)
			dnsConfig := resolveDeployDNSConfig(cmd, manifest, envVars, strings.TrimSpace(appDomain), strings.TrimSpace(apiDomain), strings.TrimSpace(dnsZoneID), autoDNS, dnsProxied)
			domains := dnsConfig.Domains
			dnsTarget := preferredPublicAddress(target)
			if dnsTarget == "" {
				dnsTarget = targetAddress(target)
			}
			zoneID := dnsConfig.ZoneID

			plan := map[string]any{
				"deploy_name":    deployName,
				"manifest":       manifestPath,
				"target_node":    target.Name,
				"target_address": targetAddress(target),
				"remote_dir":     remoteDir,
				"service_count":  len(manifest.Services),
				"env_file":       envPath,
				"registry_auth": map[string]any{
					"configured":           len(registryLogins) > 0,
					"registries":           registryLoginNames(registryLogins),
					"github_api_token_set": strings.TrimSpace(envVars["GITHUB_API_TOKEN"]) != "",
				},
				"auto_dns": map[string]any{
					"enabled":    dnsConfig.Enabled,
					"token_set":  strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")) != "",
					"domains":    domains,
					"target":     dnsTarget,
					"zone_id":    zoneID,
					"proxied":    dnsConfig.Proxied,
					"pool_path":  domainPoolPath(),
					"audit_path": domainPoolAuditPath(),
				},
				"dry_run": rootOpts.dryRun,
			}
			if rootOpts.dryRun {
				return output(plan)
			}

			if !rootOpts.yes {
				if err := confirmText("deploy " + deployName); err != nil {
					return err
				}
			}

			exec := executorForInventory(inv)
			if exec == nil {
				return fmt.Errorf("unable to resolve SSH executor; pass --config with valid ssh settings or ensure inventory has SSH credentials")
			}
			ctx := context.Background()
			targetAddr := targetAddress(target)
			if strings.TrimSpace(targetAddr) == "" {
				return fmt.Errorf("node %s has no reachable address", target.Name)
			}

			if _, err := exec.Run(ctx, targetAddr, remoteexec.Command{Command: "docker compose version", Sudo: true, Timeout: 30 * time.Second}); err != nil {
				return fmt.Errorf("target %s is missing docker compose: %w", target.Name, err)
			}

			if err := writeRemoteFile(ctx, exec, targetAddr, remoteManifestPath, manifestBytes); err != nil {
				return err
			}

			if envPath != "" {
				if err := writeRemoteFile(ctx, exec, targetAddr, remoteEnvPath, envBytes); err != nil {
					return err
				}
			}

			if err := runRegistryLogins(ctx, exec, targetAddr, registryLogins); err != nil {
				return err
			}

			composeCmd := fmt.Sprintf("cd %s && docker compose -f %s", shellQuote(remoteDir), shellQuote(remoteManifestPath))
			if envPath != "" {
				composeCmd += fmt.Sprintf(" --env-file %s", shellQuote(remoteEnvPath))
			}
			composeCmd += " up -d"
			if !noBuild {
				composeCmd += " --build"
			}

			res, err := exec.Run(ctx, targetAddr, remoteexec.Command{Command: composeCmd, Sudo: true, Timeout: 30 * time.Minute})
			result := map[string]any{
				"deploy_name":    deployName,
				"target_node":    target.Name,
				"target_address": targetAddr,
				"stdout":         strings.TrimSpace(res.Stdout),
				"stderr":         strings.TrimSpace(res.Stderr),
			}
			if err != nil {
				if rootOpts.output == "json" {
					_ = output(result)
				}
				return fmt.Errorf("deploy failed on %s: %w", target.Name, err)
			}
			dnsResult, dnsErr := configureDeployDNS(ctx, deployDNSOptions{
				Enabled: dnsConfig.Enabled,
				Token:   strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")),
				Domains: domains,
				Target:  dnsTarget,
				ZoneID:  zoneID,
				Proxied: dnsConfig.Proxied,
			})
			result["dns"] = dnsResult
			if dnsErr != nil {
				if rootOpts.output == "json" {
					_ = output(result)
				}
				return dnsErr
			}
			return output(result)
		},
	}
	cmd.Flags().StringVar(&filePath, "file", "", "path to stackforge deployment manifest")
	cmd.Flags().StringVar(&envFile, "env-file", ".env.stackforge", "env file to use for deploy (defaults to .env.stackforge)")
	cmd.Flags().StringVar(&nodeName, "node", "", "optional node name override for target deployment host")
	cmd.Flags().StringVar(&appDomain, "app-domain", "", "optional app domain for auto DNS (falls back to APP_DOMAIN/VITE_CLIENT_HOST in env file)")
	cmd.Flags().StringVar(&apiDomain, "api-domain", "", "optional API domain for auto DNS (falls back to API_DOMAIN/VITE_API_URL in env file)")
	cmd.Flags().StringVar(&dnsZoneID, "dns-zone-id", "", "optional Cloudflare zone id override for auto DNS")
	cmd.Flags().BoolVar(&dnsProxied, "dns-proxied", false, "enable Cloudflare proxy for auto-created DNS records")
	cmd.Flags().BoolVar(&autoDNS, "auto-dns", true, "auto-configure Cloudflare DNS when token and domains are available")
	cmd.Flags().BoolVar(&noBuild, "no-build", false, "skip image build step and only start containers")
	cmd.AddCommand(deployInitCmd())
	return cmd
}

func deployInitCmd() *cobra.Command {
	var outputPath string
	var force bool
	var randomSecrets bool
	var printSecretsSummary bool
	var quiet bool
	cmd := &cobra.Command{Use: "init", Short: "Create an editable stackforge deployment template", RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := writeDeployScaffold(outputPath, force, randomSecrets)
		if err != nil {
			return err
		}
		printSecretsSummary = effectiveSecretsSummaryMode(quiet, printSecretsSummary)
		if quiet {
			fmt.Fprintln(os.Stdout, paths["deployment_file"])
			fmt.Fprintln(os.Stdout, paths["env_file"])
			return nil
		}
		result := map[string]any{
			"created":         true,
			"deployment_file": paths["deployment_file"],
			"env_file":        paths["env_file"],
			"next":            "edit domains/services/env values and secrets, then run stackforge deploy --file " + paths["deployment_file"] + " --env-file " + paths["env_file"],
		}
		if printSecretsSummary {
			summary, err := maskedSecretsSummary(paths["env_file"])
			if err != nil {
				return err
			}
			result["secrets_summary"] = summary
		}
		return output(result)
	}}
	cmd.Flags().StringVarP(&outputPath, "output", "o", "stackforge-deployment.yaml", "output path for generated deployment template")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing file")
	cmd.Flags().BoolVar(&randomSecrets, "random-secrets", false, "generate secure random values for .env.stackforge secrets/passwords")
	cmd.Flags().BoolVar(&printSecretsSummary, "print-secrets-summary", false, "print masked summary of generated secret fields")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "print only generated file paths (for scripting/CI)")
	return cmd
}

type deployDNSOptions struct {
	Enabled bool
	Token   string
	Domains []string
	Target  string
	ZoneID  string
	Proxied bool
}

type resolvedDeployDNSConfig struct {
	Enabled bool
	Domains []string
	ZoneID  string
	Proxied bool
}

func writeDeployScaffold(path string, force bool, randomSecrets bool) (map[string]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "stackforge-deployment.yaml"
	}
	envPath := filepath.Join(filepath.Dir(path), ".env.stackforge")
	for _, candidate := range []string{path, envPath} {
		if !force {
			if _, err := os.Stat(candidate); err == nil {
				return nil, fmt.Errorf("%s already exists; use --force to overwrite", candidate)
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(deployTemplateYAML()), 0644); err != nil {
		return nil, err
	}
	envContent, err := deployTemplateEnv(randomSecrets)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
		return nil, err
	}
	return map[string]string{"deployment_file": path, "env_file": envPath}, nil
}

func deployTemplateYAML() string {
	return strings.Join([]string{
		"name: my-stackforge-app",
		"",
		"auto_dns:",
		"  enabled: true",
		"  app_domain: app.example.com",
		"  api_domain: api.example.com",
		"  domains:",
		"    - admin.example.com",
		"  zone_id: \"\"",
		"  proxied: true",
		"",
		"services:",
		"  postgres:",
		"    image: postgres:16",
		"    restart: unless-stopped",
		"    environment:",
		"      POSTGRES_DB: ${APP_DB_NAME:-app}",
		"      POSTGRES_USER: ${APP_DB_USER:-app}",
		"      POSTGRES_PASSWORD: ${APP_DB_PASSWORD:-change-me}",
		"    volumes:",
		"      - postgres_data:/var/lib/postgresql/data",
		"    healthcheck:",
		"      test: [\"CMD-SHELL\", \"pg_isready -U ${APP_DB_USER:-app} -d ${APP_DB_NAME:-app}\"]",
		"      interval: 10s",
		"      timeout: 5s",
		"      retries: 10",
		"",
		"  redis:",
		"    image: redis:7-alpine",
		"    restart: unless-stopped",
		"    command: [\"redis-server\", \"--appendonly\", \"yes\"]",
		"    volumes:",
		"      - redis_data:/data",
		"    healthcheck:",
		"      test: [\"CMD\", \"redis-cli\", \"ping\"]",
		"      interval: 10s",
		"      timeout: 3s",
		"      retries: 10",
		"",
		"  api:",
		"    build:",
		"      context: .",
		"      dockerfile: docker/Dockerfile.api",
		"    restart: unless-stopped",
		"    depends_on:",
		"      postgres:",
		"        condition: service_healthy",
		"      redis:",
		"        condition: service_healthy",
		"    environment:",
		"      ENVIRONMENT: ${ENVIRONMENT:-production}",
		"      APP_CONFIG_ENV: ${APP_CONFIG_ENV:-production}",
		"      LOG_LEVEL: ${LOG_LEVEL:-INFO}",
		"      SECRET_KEY: ${SECRET_KEY:-replace-me-strong-secret}",
		"      JWT_SECRET_KEY: ${JWT_SECRET_KEY:-replace-me-strong-jwt-secret}",
		"      ENCRYPTION_KEY: ${ENCRYPTION_KEY:-replace-me-strong-encryption-key}",
		"      DB_HOST: postgres",
		"      DB_PORT: 5432",
		"      DB_NAME: ${APP_DB_NAME:-app}",
		"      DB_USER: ${APP_DB_USER:-app}",
		"      DB_PASSWORD: ${APP_DB_PASSWORD:-change-me}",
		"      REDIS_HOST: redis",
		"      REDIS_PORT: 6379",
		"    ports:",
		"      - \"8888:8888\"",
		"    healthcheck:",
		"      test: [\"CMD-SHELL\", \"wget -qO- http://127.0.0.1:8888/health >/dev/null 2>&1 || exit 1\"]",
		"      interval: 15s",
		"      timeout: 5s",
		"      retries: 10",
		"",
		"  frontend:",
		"    build:",
		"      context: .",
		"      dockerfile: docker/Dockerfile.frontend",
		"      args:",
		"        VITE_API_URL: ${VITE_API_URL:-https://api.example.com}",
		"        VITE_API_BASE_URL: ${VITE_API_BASE_URL:-https://api.example.com}",
		"        VITE_AUTH_BASE_URL: ${VITE_AUTH_BASE_URL:-https://api.example.com}",
		"        VITE_CLIENT_HOST: ${VITE_CLIENT_HOST:-app.example.com}",
		"    restart: unless-stopped",
		"    depends_on:",
		"      api:",
		"        condition: service_healthy",
		"    ports:",
		"      - \"5173:80\"",
		"",
		"volumes:",
		"  postgres_data:",
		"  redis_data:",
		"",
	}, "\n")
}

func deployTemplateEnv(randomSecrets bool) (string, error) {
	secretKey := "replace-with-strong-secret-key-32plus"
	jwtSecretKey := "replace-with-strong-jwt-secret-32plus"
	encryptionKey := "replace-with-strong-encryption-key-32plus"
	dbPassword := "change-me-app-db-password"
	if randomSecrets {
		var err error
		if secretKey, err = randomHex(32); err != nil {
			return "", err
		}
		if jwtSecretKey, err = randomHex(32); err != nil {
			return "", err
		}
		if encryptionKey, err = randomHex(32); err != nil {
			return "", err
		}
		if dbPassword, err = randomHex(24); err != nil {
			return "", err
		}
	}

	return strings.Join([]string{
		"# StackForge deploy environment file",
		"# Used by: stackforge deploy --env-file .env.stackforge",
		"",
		"ENVIRONMENT=production",
		"APP_CONFIG_ENV=production",
		"LOG_LEVEL=INFO",
		"",
		"# Required app secrets (replace before live deploy)",
		"SECRET_KEY=" + secretKey,
		"JWT_SECRET_KEY=" + jwtSecretKey,
		"ENCRYPTION_KEY=" + encryptionKey,
		"",
		"# Database credentials",
		"APP_DB_NAME=app",
		"APP_DB_USER=app",
		"APP_DB_PASSWORD=" + dbPassword,
		"",
		"# Domains used by auto DNS and frontend build",
		"APP_DOMAIN=app.example.com",
		"API_DOMAIN=api.example.com",
		"",
		"# Optional GitHub token for API calls/build tooling",
		"GITHUB_API_TOKEN=",
		"",
		"# Optional private registry auth",
		"GHCR_USERNAME=",
		"GHCR_TOKEN=",
		"DOCKERHUB_USERNAME=",
		"DOCKERHUB_TOKEN=",
		"DOCKER_REGISTRY=",
		"DOCKER_REGISTRY_USERNAME=",
		"DOCKER_REGISTRY_PASSWORD=",
		"",
		"# Cloudflare defaults (optional)",
		"CLOUDFLARE_DEFAULT_ZONE_ID=",
		"",
		"# Frontend URL wiring",
		"VITE_API_URL=https://${API_DOMAIN}",
		"VITE_API_BASE_URL=https://${API_DOMAIN}",
		"VITE_AUTH_BASE_URL=https://${API_DOMAIN}",
		"VITE_CLIENT_HOST=${APP_DOMAIN}",
	}, "\n") + "\n", nil
}

func randomHex(numBytes int) (string, error) {
	b := make([]byte, numBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func maskedSecretsSummary(envPath string) (map[string]string, error) {
	b, err := os.ReadFile(envPath)
	if err != nil {
		return nil, fmt.Errorf("read env file for summary: %w", err)
	}
	env := parseEnvFile(b)
	fields := []string{"SECRET_KEY", "JWT_SECRET_KEY", "ENCRYPTION_KEY", "APP_DB_PASSWORD"}
	out := map[string]string{}
	for _, field := range fields {
		out[field] = maskSecret(env[field])
	}
	return out, nil
}

type registryLogin struct {
	Name     string
	Registry string
	Username string
	Password string
}

func deployRegistryLogins(envVars map[string]string) []registryLogin {
	logins := []registryLogin{}
	ghcrUser := strings.TrimSpace(envVars["GHCR_USERNAME"])
	ghcrToken := strings.TrimSpace(firstNonEmpty(envVars["GHCR_TOKEN"], envVars["GITHUB_API_TOKEN"]))
	if ghcrUser != "" && ghcrToken != "" {
		logins = append(logins, registryLogin{Name: "ghcr", Registry: "ghcr.io", Username: ghcrUser, Password: ghcrToken})
	}
	dockerHubUser := strings.TrimSpace(envVars["DOCKERHUB_USERNAME"])
	dockerHubToken := strings.TrimSpace(envVars["DOCKERHUB_TOKEN"])
	if dockerHubUser != "" && dockerHubToken != "" {
		logins = append(logins, registryLogin{Name: "dockerhub", Registry: "", Username: dockerHubUser, Password: dockerHubToken})
	}
	customRegistry := strings.TrimSpace(envVars["DOCKER_REGISTRY"])
	customUser := strings.TrimSpace(envVars["DOCKER_REGISTRY_USERNAME"])
	customPass := strings.TrimSpace(envVars["DOCKER_REGISTRY_PASSWORD"])
	if customRegistry != "" && customUser != "" && customPass != "" {
		logins = append(logins, registryLogin{Name: "custom", Registry: customRegistry, Username: customUser, Password: customPass})
	}
	return logins
}

func registryLoginNames(logins []registryLogin) []string {
	out := make([]string, 0, len(logins))
	for _, login := range logins {
		out = append(out, login.Name)
	}
	return out
}

func runRegistryLogins(ctx context.Context, exec remoteexec.Executor, node string, logins []registryLogin) error {
	for _, login := range logins {
		cmd := buildDockerLoginCommand(login)
		if _, err := exec.Run(ctx, node, remoteexec.Command{Command: cmd, Sudo: true, Timeout: 2 * time.Minute, Secrets: []string{login.Password}}); err != nil {
			registry := login.Registry
			if registry == "" {
				registry = "docker.io"
			}
			return fmt.Errorf("docker login failed for %s: %w", registry, err)
		}
	}
	return nil
}

func buildDockerLoginCommand(login registryLogin) string {
	base := fmt.Sprintf("printf %%s %s | docker login -u %s --password-stdin", shellQuote(login.Password), shellQuote(login.Username))
	if strings.TrimSpace(login.Registry) == "" {
		return base
	}
	return base + " " + shellQuote(login.Registry)
}

func maskSecret(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if len(v) <= 8 {
		return "****"
	}
	return v[:4] + "..." + v[len(v)-4:]
}

func effectiveSecretsSummaryMode(quiet bool, printSecretsSummary bool) bool {
	if quiet {
		return false
	}
	return printSecretsSummary
}

func resolveDeployDNSConfig(cmd *cobra.Command, manifest deployManifest, envVars map[string]string, flagAppDomain, flagAPIDomain, flagZoneID string, flagAutoDNS, flagDNSProxied bool) resolvedDeployDNSConfig {
	enabled := flagAutoDNS
	if !cmd.Flags().Changed("auto-dns") && manifest.AutoDNS.Enabled != nil {
		enabled = *manifest.AutoDNS.Enabled
	}

	proxied := flagDNSProxied
	if !cmd.Flags().Changed("dns-proxied") && manifest.AutoDNS.Proxied != nil {
		proxied = *manifest.AutoDNS.Proxied
	}

	app := firstNonEmpty(flagAppDomain, strings.TrimSpace(manifest.AutoDNS.AppDomain))
	api := firstNonEmpty(flagAPIDomain, strings.TrimSpace(manifest.AutoDNS.APIDomain))
	zoneID := firstNonEmpty(flagZoneID, strings.TrimSpace(manifest.AutoDNS.ZoneID), strings.TrimSpace(envVars["CLOUDFLARE_DEFAULT_ZONE_ID"]))
	domains := deployDomains(app, api, manifest.AutoDNS.Domains, envVars)

	return resolvedDeployDNSConfig{Enabled: enabled, Domains: domains, ZoneID: zoneID, Proxied: proxied}
}

func configureDeployDNS(ctx context.Context, opts deployDNSOptions) (map[string]any, error) {
	result := map[string]any{
		"enabled": opts.Enabled,
		"domains": opts.Domains,
		"target":  opts.Target,
		"zone_id": opts.ZoneID,
		"proxied": opts.Proxied,
	}
	if !opts.Enabled {
		result["status"] = "disabled"
		return result, nil
	}
	if opts.Token == "" {
		result["status"] = "skipped"
		result["reason"] = "CLOUDFLARE_API_TOKEN is not set"
		return result, nil
	}
	if len(opts.Domains) == 0 {
		result["status"] = "skipped"
		result["reason"] = "no domains provided (use --app-domain/--api-domain or env file APP_DOMAIN/API_DOMAIN)"
		return result, nil
	}
	if ip := net.ParseIP(strings.TrimSpace(opts.Target)); ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() {
		return result, fmt.Errorf("auto DNS requires a public target IP; got %q", opts.Target)
	}
	client := &cloudflare.Client{Token: opts.Token}
	applied := make([]string, 0, len(opts.Domains))
	for _, d := range opts.Domains {
		domainName := strings.TrimSpace(strings.ToLower(d))
		if domainName == "" {
			continue
		}
		if _, _, _, err := domainpool.Find(domainPoolPath(), domainName); err != nil {
			if _, err := domainpool.Add(domainPoolPath(), domainpool.Entry{
				Domain:      domainName,
				TargetType:  "traefik",
				TargetValue: opts.Target,
				RecordType:  "A",
				ZoneID:      opts.ZoneID,
				Proxied:     opts.Proxied,
			}, false, false); err != nil {
				return result, fmt.Errorf("add domain %s to pool: %w", domainName, err)
			}
		}
		if _, err := domainpool.ApplyDNS(ctx, domainName, domainpool.ApplyOptions{Path: domainPoolPath(), AuditPath: domainPoolAuditPath(), Client: client}); err != nil {
			return result, fmt.Errorf("apply DNS for %s: %w", domainName, err)
		}
		applied = append(applied, domainName)
	}
	result["status"] = "applied"
	result["applied"] = applied
	return result, nil
}

func loadDeployManifest(path string) ([]byte, deployManifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, deployManifest{}, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var manifest deployManifest
	if err := yaml.Unmarshal(b, &manifest); err != nil {
		return nil, deployManifest{}, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	if len(manifest.Services) == 0 {
		return nil, deployManifest{}, fmt.Errorf("manifest %s has no services", path)
	}
	return b, manifest, nil
}

func inventoryForDeploy() (*inventory.Inventory, error) {
	inv, err := loadInventory()
	if err == nil {
		return inv, nil
	}
	if rootOpts.configPath == "" {
		return nil, err
	}
	cfg, cfgErr := config.Load(rootOpts.configPath)
	if cfgErr != nil {
		return nil, err
	}
	return inventoryFromConfig(cfg), nil
}

func selectDeployNode(inv *inventory.Inventory, requested string) (inventory.Node, error) {
	if inv == nil || len(inv.Nodes) == 0 {
		return inventory.Node{}, fmt.Errorf("inventory has no nodes")
	}
	if requested != "" {
		for _, n := range inv.Nodes {
			if n.Name == requested {
				return n, nil
			}
		}
		return inventory.Node{}, fmt.Errorf("node %s not found", requested)
	}
	for _, n := range inv.Nodes {
		if hasRole(n.Roles, "control-plane") {
			return n, nil
		}
	}
	for _, n := range inv.Nodes {
		if hasRole(n.Roles, "nomad-server") {
			return n, nil
		}
	}
	return inv.Nodes[0], nil
}

func hasRole(roles []string, role string) bool {
	for _, r := range roles {
		if strings.EqualFold(strings.TrimSpace(r), role) {
			return true
		}
	}
	return false
}

func targetAddress(n inventory.Node) string {
	if strings.TrimSpace(n.PrivateIP) != "" {
		return n.PrivateIP
	}
	return n.PublicIP
}

func writeRemoteFile(ctx context.Context, exec remoteexec.Executor, node, path string, content []byte) error {
	encoded := base64.StdEncoding.EncodeToString(content)
	command := fmt.Sprintf("mkdir -p %s && cat > %s <<'STACKFORGE_B64'\n%s\nSTACKFORGE_B64\nbase64 -d %s > %s && rm -f %s", shellQuote(filepath.ToSlash(filepath.Dir(path))), shellQuote(path+".b64"), encoded, shellQuote(path+".b64"), shellQuote(path), shellQuote(path+".b64"))
	_, err := exec.Run(ctx, node, remoteexec.Command{Command: command, Sudo: true, Timeout: 2 * time.Minute})
	if err != nil {
		return fmt.Errorf("write remote file %s on %s: %w", path, node, err)
	}
	return nil
}

func parseEnvFile(content []byte) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if key == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func deployDomains(appDomain, apiDomain string, manifestDomains []string, env map[string]string) []string {
	candidates := []string{
		strings.TrimSpace(appDomain),
		strings.TrimSpace(apiDomain),
	}
	for _, domain := range manifestDomains {
		candidates = append(candidates, strings.TrimSpace(domain))
	}
	candidates = append(candidates,
		strings.TrimSpace(env["APP_DOMAIN"]),
		strings.TrimSpace(env["API_DOMAIN"]),
		strings.TrimSpace(env["VITE_CLIENT_HOST"]),
		hostFromValue(env["VITE_API_URL"]),
		hostFromValue(env["VITE_API_BASE_URL"]),
		hostFromValue(env["VITE_AUTH_BASE_URL"]),
	)
	seen := map[string]bool{}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		c = normalizeDomainHost(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func hostFromValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.Contains(v, "://") {
		u, err := url.Parse(v)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(u.Hostname())
	}
	if h, _, err := net.SplitHostPort(v); err == nil {
		return strings.TrimSpace(h)
	}
	return v
}

func normalizeDomainHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	if host == "" || strings.Contains(host, "/") || strings.Contains(host, " ") {
		return ""
	}
	if strings.Contains(host, "${") || strings.Contains(host, "$") {
		return ""
	}
	if net.ParseIP(host) != nil {
		return ""
	}
	return host
}

func preferredPublicAddress(n inventory.Node) string {
	if ip := strings.TrimSpace(n.PublicIP); ip != "" {
		return ip
	}
	return ""
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

var deployNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func sanitizeDeployName(s string) string {
	s = strings.TrimSpace(s)
	s = deployNameSanitizer.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-._")
	return strings.ToLower(s)
}
