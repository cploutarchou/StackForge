# Infrastructure Components

This document describes the components found in the current codebase.

## Docker

Purpose:

Docker is installed for nodes that need container runtime support, especially Nomad client or `docker-host` use cases.

Installation flow:

1. Check whether `docker` exists and `docker version` can reach the daemon.
2. Install apt prerequisites.
3. Add Docker apt key under `/etc/apt/keyrings/docker.gpg`.
4. Add Docker apt source for the server OS.
5. Install:
   - `docker-ce`
   - `docker-ce-cli`
   - `containerd.io`
   - `docker-buildx-plugin`
   - `docker-compose-plugin`
6. Enable and start Docker.
7. Run `docker info`.

Configuration flow:

- No daemon config is written by StackForge in the current code.
- `DockerInstallCommand` can add a non-root SSH user to the `docker` group, but current calls pass `addUser=false`.

Validation checks:

- `components status` checks Docker binary, server version, and systemd status.

Failure scenarios:

- Docker apt repository cannot be reached.
- Unsupported OS ID for Docker repository URL.
- Docker daemon does not start.

Troubleshooting:

```bash
systemctl status docker
journalctl -u docker
docker info
```

## Consul

Purpose:

Consul provides service discovery and health/discovery APIs.

Installation flow:

1. Add HashiCorp apt repository.
2. Install `consul`.
3. Create `/etc/consul.d` and `/opt/consul`.
4. Back up existing `/etc/consul.d`.
5. Write `/etc/consul.d/stackforge.hcl`.
6. Set ownership for Consul paths.
7. Enable and restart Consul.
8. Wait for `http://127.0.0.1:8500/v1/status/leader`.
9. If ACLs are enabled, verify the bootstrap token.

Configuration flow:

Generated config includes:

- datacenter
- data dir
- server mode
- `bootstrap_expect`
- localhost client address
- private bind address through `{{ GetPrivateIP }}`
- optional gossip encryption
- UI setting
- ACL settings
- optional initial management token

Validation checks:

- `consul version`
- active `consul` systemd service
- leader endpoint
- peers endpoint
- ACL token self-check when ACLs are enabled

Failure scenarios:

- HashiCorp repo setup fails.
- Consul cannot elect or report a leader.
- ACL token mismatch or missing token.
- Wrong number of server nodes for quorum.

Troubleshooting:

```bash
systemctl status consul
journalctl -u consul
consul version
curl http://127.0.0.1:8500/v1/status/leader
curl http://127.0.0.1:8500/v1/status/peers
```

Operational gap:

`stackforge consul status`, `members`, `kv`, and `snapshot` commands are registered but intentionally refuse live behavior.

## Nomad

Purpose:

Nomad is the scheduler/orchestrator layer.

Installation flow:

1. Add HashiCorp apt repository.
2. Install `nomad`.
3. Create `/etc/nomad.d` and `/opt/nomad`.
4. Back up existing `/etc/nomad.d`.
5. Write `/etc/nomad.d/stackforge.hcl`.
6. Set ownership for Nomad paths.
7. Enable and restart Nomad.
8. Wait for `http://127.0.0.1:4646/v1/status/leader`.
9. If ACLs are enabled on a server, bootstrap or verify the token.

Configuration flow:

Generated config includes:

- datacenter
- data dir
- bind address `0.0.0.0`
- server enabled flag
- `bootstrap_expect`
- client enabled flag
- ACL enabled flag

Validation checks:

- `nomad version`
- active `nomad` systemd service
- leader endpoint
- nodes endpoint for client mode
- ACL token self-check when ACLs are enabled and a token is present

Failure scenarios:

- HashiCorp repo setup fails.
- Nomad cannot find a leader.
- ACL bootstrap already happened but the stored token is invalid or missing.
- Nomad server count is even.

Troubleshooting:

```bash
systemctl status nomad
journalctl -u nomad
nomad version
curl http://127.0.0.1:4646/v1/status/leader
curl http://127.0.0.1:4646/v1/nodes
```

Operational gap:

`stackforge nomad status`, `nodes`, `jobs`, `allocations`, and `drain-node` are registered but intentionally refuse live behavior.

The Nomad client package can query jobs and nodes, but job deployment templates are absent and the client refuses to submit jobs.

## Traefik

Purpose:

Traefik provides HTTP/HTTPS ingress.

Installation flow:

1. Install or reuse a Traefik binary at `/usr/local/bin/traefik`.
2. Create `/etc/traefik/dynamic`.
3. Create `/var/lib/traefik/acme.json` with mode `0600`.
4. Back up existing `/etc/traefik`.
5. Write `/etc/traefik/traefik.yaml`.
6. Write `/etc/traefik/dynamic/stackforge.yaml`.
7. Write `/etc/systemd/system/traefik.service`.
8. Reload systemd.
9. Enable and restart Traefik.

Configuration flow:

Generated config includes:

- `web` entrypoint on `:80`
- `websecure` entrypoint on `:443`
- file provider watching `/etc/traefik/dynamic`
- optional ACME resolver when `cert_resolver` and `email` are set
- dashboard enabled flag
- dashboard insecure flag set to false

Validation checks:

- active `traefik` systemd service
- ports `80` and `443` are listening
- dashboard auth guard when dashboard is enabled

Failure scenarios:

- Unsupported architecture for binary download.
- GitHub release download fails.
- Ports 80 or 443 are already in use.
- Dashboard enabled without basic auth.

Troubleshooting:

```bash
systemctl status traefik
journalctl -u traefik
traefik version
ss -ltn
```

Operational gap:

`stackforge traefik status`, `routes`, `reload`, and `logs` are registered but intentionally refuse live behavior.

## PostgreSQL

Purpose:

PostgreSQL stores StackForge control-plane domain data.

Installation flow:

1. Install `postgresql` and `postgresql-client`.
2. Enable and start PostgreSQL.
3. Create role `stackforge` if missing.
4. Create database `stackforge` if missing.
5. Back up `/etc/postgresql`.
6. Set `listen_addresses = '127.0.0.1'`.
7. Restart PostgreSQL.
8. Create `stackforge_schema_migrations` marker and insert migration version.

Configuration flow:

- Database password comes from generated secrets.
- Control-plane `DATABASE_URL` points to localhost PostgreSQL.
- Current live install supports PostgreSQL only.

Validation checks:

- active `postgresql` systemd service
- `SELECT 1` against `stackforge`
- migration table/domain schema check in verify
- port `5432` is not listening publicly

Failure scenarios:

- Apt install fails.
- Local PostgreSQL auth does not allow `sudo -u postgres`.
- PostgreSQL cannot restart.
- Port 5432 is bound publicly.

Troubleshooting:

```bash
systemctl status postgresql
journalctl -u postgresql
sudo -u postgres psql -d stackforge -c 'select 1'
ss -ltnp | grep 5432
```

Important gap:

`database.engine` validation accepts `mysql`, but live install returns an error unless the engine is `postgres`.

## StackForge Control Plane

Purpose:

The control-plane service serves the domain API.

Installation flow:

1. Back up `/etc/stackforge`.
2. Install the current `stackforge` binary to `/usr/local/bin/stackforge`.
3. Write `/etc/stackforge/stackforge.env`.
4. Write `/etc/systemd/system/stackforge-control-plane.service`.
5. Reload systemd.
6. Enable and restart service.
7. Verify env file permissions.

Runtime:

```bash
stackforge serve
```

Required environment:

- `STACKFORGE_ADMIN_API_KEYS`
- `DATABASE_URL` when `STACKFORGE_ENV=production`

Validation checks:

- active `stackforge-control-plane` service
- `GET /health` returns ok
- unauthenticated `/api/v1/domains` returns `401`
- remote env file mode is `600`

Failure scenarios:

- Binary not available for remote install.
- `DATABASE_URL` missing in production.
- PostgreSQL connection fails.
- Env file permissions fail.

Troubleshooting:

```bash
systemctl status stackforge-control-plane
journalctl -u stackforge-control-plane
curl http://127.0.0.1:8080/health
```

## Domain Routing

Purpose:

The control-plane domain model stores domain-to-service routing intent.

Current API flow:

1. Create domain with tenant, domain, service name, and port.
2. Generate verification token.
3. Verify ownership by submitting token.
4. Apply DNS or routing endpoints return accepted only after verification.

Current limitations:

- The HTTP server does not wire Cloudflare, Consul, Nomad, or Traefik clients into reconciliation.
- `domains reconcile` through the API returns an error about missing external clients.
- Traefik tag generation exists in `internal/controlplane/routing/traefik`, but no command deploys those tags to Nomad.

## DNS

Purpose:

DNS is handled in two places:

- Local domain pool through `stackforge domains pool`.
- Cloudflare client package under `internal/controlplane/dns/cloudflare`.

Local domain-pool flow:

1. Add a domain with target type, target value, record type, zone id, and proxy setting.
2. Apply DNS through Cloudflare.
3. Verify DNS using Go's resolver.
4. Write audit records.

Supported record types:

- `A`
- `CNAME`

Failure scenarios:

- `CLOUDFLARE_API_TOKEN` is missing.
- Zone lookup fails.
- Record target is invalid.
- DNS propagation is not complete during verification.

Troubleshooting:

```bash
stackforge domains pool list --cluster stackforge-production
stackforge domains pool apply-dns app.example.com --yes
stackforge domains pool verify-dns app.example.com
```

## Firewall

Purpose:

Firewall management limits exposure of SSH, admin APIs, internal cluster ports, and database ports.

Installation/configuration flow:

1. Build a UFW plan from config.
2. Back up current UFW status to `/var/lib/stackforge/firewall`.
3. Reset UFW.
4. Set default deny incoming and allow outgoing.
5. Apply allow rules.
6. Force-enable UFW.
7. Verify UFW is active and internal/admin services are not exposed to `Anywhere`.

Failure scenarios:

- UFW missing.
- nftables-only host without `--allow-no-firewall`.
- Admin CIDR is public.
- SSH CIDR is public without explicit override.
- Firewall blocks SSH after apply.

Troubleshooting:

Use console access if SSH is blocked.

```bash
ufw status verbose
ls -l /var/lib/stackforge/firewall
```

## Inventory

Purpose:

Inventory stores desired and observed cluster state.

It includes:

- cluster and environment
- nodes and roles
- SSH connection info
- observed OS/kernel/IPs/listening ports
- component versions
- service statuses
- endpoints
- install and health status
- backup/restore markers
- warnings

Refresh:

```bash
stackforge inventory refresh --config stackforge.yaml
```

## Backup And Rollback

Backups include:

- inventory
- redacted generated secrets
- database dump plan or output
- Consul snapshot plan or output
- Nomad job export plan or output
- Traefik config archive plan or output
- StackForge config archive plan or output
- checksums

Rollback records are saved before live component changes when the install step has rollback metadata.

Automatic rollback is refused for unsafe records such as firewall and database.
