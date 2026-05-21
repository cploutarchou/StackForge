# StackForge Security

## Authentication

All `/api/v1` endpoints require `Authorization: Bearer <key>`. Keys come from `STACKFORGE_ADMIN_API_KEYS` and are hashed before comparison.

## Secrets

Generated local secrets are stored in `~/.stackforge/{cluster}/generated-secrets.yaml` with mode `0600`. Secret values are not printed in normal command output.

## Production Limitation

Local secrets encryption at rest is not implemented. Protect operator workstations and use filesystem encryption or a secrets manager for production use.

## Firewall

SSH must be restricted to allowed SSH CIDRs. Consul UI/API, Nomad UI/API, Traefik dashboard, and the control-plane API must be restricted to admin CIDRs/private networks. The database must never be public.

## Cloudflare Token Scopes

Use least privilege: zone read plus DNS edit for only the managed zones.

## Consul and Nomad ACLs

Enable ACLs, bootstrap once, store bootstrap tokens in restricted env files, and rotate service tokens.

## TLS

Expose public routes only through HTTPS with Traefik and ACME. Do not expose admin UIs without CIDR restriction and authentication.

## SSH Hardening

Use key-only auth, disable root password login, restrict source CIDRs, and keep console access available during firewall changes.
