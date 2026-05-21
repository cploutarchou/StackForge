package traefik

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

var unsafe = regexp.MustCompile(`[^a-z0-9-]+`)

func Tags(tenant, domain, service string, port int, certResolver, entrypoint string) []string {
	name := safeName(tenant, domain)
	return []string{
		"traefik.enable=true",
		fmt.Sprintf("traefik.http.routers.%s.rule=Host(`%s`)", name, strings.ToLower(domain)),
		fmt.Sprintf("traefik.http.routers.%s.entrypoints=%s", name, entrypoint),
		fmt.Sprintf("traefik.http.routers.%s.tls=true", name),
		fmt.Sprintf("traefik.http.routers.%s.tls.certresolver=%s", name, certResolver),
		fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port=%d", safeName(tenant, service), port),
	}
}

func safeName(parts ...string) string {
	raw := strings.ToLower(strings.Join(parts, "-"))
	sum := sha1.Sum([]byte(raw))
	clean := strings.Trim(unsafe.ReplaceAllString(raw, "-"), "-")
	if len(clean) > 40 {
		clean = clean[:40]
	}
	return clean + "-" + hex.EncodeToString(sum[:])[:10]
}
