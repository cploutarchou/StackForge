package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env                     string
	HTTPAddr                string
	StateDir                string
	LogLevel                string
	AdminAPIKeys            []string
	ReconcilerEnabled       bool
	ReconcilerInterval      time.Duration
	ReadyProtected          bool
	DatabaseURL             string
	AllowWildcardDomains    bool
	CloudflareAPIToken      string
	CloudflareAccountID     string
	CloudflareDefaultZoneID string
	NomadAddr               string
	NomadToken              string
	ConsulHTTPAddr          string
	ConsulHTTPToken         string
	TraefikCertResolver     string
	TraefikEntrypoint       string
}

func FromEnv() Config {
	return Config{
		Env:                     getenv("STACKFORGE_ENV", "production"),
		HTTPAddr:                getenv("STACKFORGE_HTTP_ADDR", ":8080"),
		StateDir:                getenv("STACKFORGE_STATE_DIR", "/var/lib/stackforge"),
		LogLevel:                getenv("STACKFORGE_LOG_LEVEL", "info"),
		AdminAPIKeys:            split(os.Getenv("STACKFORGE_ADMIN_API_KEYS")),
		ReconcilerEnabled:       boolenv("STACKFORGE_RECONCILER_ENABLED", true),
		ReconcilerInterval:      time.Duration(intenv("STACKFORGE_RECONCILER_INTERVAL_SECONDS", 300)) * time.Second,
		ReadyProtected:          boolenv("STACKFORGE_READY_PROTECTED", true),
		DatabaseURL:             os.Getenv("DATABASE_URL"),
		AllowWildcardDomains:    boolenv("ALLOW_WILDCARD_DOMAINS", false),
		CloudflareAPIToken:      os.Getenv("CLOUDFLARE_API_TOKEN"),
		CloudflareAccountID:     os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		CloudflareDefaultZoneID: os.Getenv("CLOUDFLARE_DEFAULT_ZONE_ID"),
		NomadAddr:               os.Getenv("NOMAD_ADDR"),
		NomadToken:              os.Getenv("NOMAD_TOKEN"),
		ConsulHTTPAddr:          os.Getenv("CONSUL_HTTP_ADDR"),
		ConsulHTTPToken:         os.Getenv("CONSUL_HTTP_TOKEN"),
		TraefikCertResolver:     getenv("TRAEFIK_CERT_RESOLVER", "letsencrypt"),
		TraefikEntrypoint:       getenv("TRAEFIK_ENTRYPOINT", "websecure"),
	}
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func split(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func boolenv(k string, d bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	return v == "true" || v == "1" || v == "yes"
}

func intenv(k string, d int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return d
}
