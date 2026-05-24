<p align="center">
  <img src="assets/stackforge-logo.svg" alt="StackForge logo" width="520">
</p>

# StackForge

StackForge is a Go CLI and small control-plane API for installing and operating a basic infrastructure stack on Debian or Ubuntu servers.

The CLI can validate server configuration, bootstrap SSH access, install core packages, configure UFW firewall rules, install Consul, Nomad, Traefik, PostgreSQL, and a StackForge control-plane systemd service. It also keeps local state such as inventory, generated secrets, install reports, backups, rollback records, and a domain pool.

The codebase is still in progress. Some commands are fully implemented, some are dry-run or planning helpers, and some live operational commands intentionally refuse to run until real client configuration is wired.

## Who It Is For

StackForge is for developers or operators who want a CLI-driven way to prepare small Debian/Ubuntu clusters for:

- HashiCorp Consul service discovery.
- HashiCorp Nomad scheduling.
- Traefik HTTP/HTTPS ingress.
- PostgreSQL for the StackForge control plane.
- Domain and DNS management through a local domain pool or the control-plane API.

Use it carefully. Live install commands modify remote servers over SSH.

## Main Features

- Cobra-based `stackforge` CLI.
- YAML cluster configuration.
- SSH key bootstrap with password auth only for the initial key copy.
- Live preflight validation for SSH, OS, sudo, apt, systemd, disk, RAM, ports, and firewall.
- Safety checks for production installs, example IPs/domains, public admin CIDRs, public SSH CIDRs, and public database exposure.
- UFW firewall plan and apply commands.
- Remote install flow for base packages, Consul, Nomad, Traefik, PostgreSQL, and the StackForge control-plane service.
- Local inventory at `~/.stackforge/<cluster>/inventory.yaml` by default.
- Generated secrets at `~/.stackforge/<cluster>/generated-secrets.yaml`.
- Backup, restore planning, rollback records, and install reports.
- Control-plane API for domain records.
- Local domain pool with Cloudflare DNS apply and DNS verification.

## Supported Servers

The install and validation code targets Debian/Ubuntu servers with:

- Debian 12 or newer, based on install checks.
- Ubuntu 22.04 or 24.04 in validation checks.
- Ubuntu 26.04 is accepted by the install OS shell check, but not by `internal/stackforge/osdetect` or live validation parsing yet.
- `systemd`.
- `apt-get`.
- SSH access as root or a sudo-capable user.
- UFW for managed firewall rules, unless `--allow-no-firewall` is explicitly used.
- At least 20 GiB free disk space on `/` for live validation.
- At least 2 GiB RAM for live validation.

Supported release installer architectures are Linux `amd64` and `arm64`.

## High-Level Architecture

```mermaid
flowchart LR
  CLI[stackforge CLI] --> Config[YAML config]
  CLI --> State[Local state dir]
  CLI --> SSH[SSH executor]
  SSH --> Nodes[Debian/Ubuntu nodes]
  Nodes --> Consul[Consul]
  Nodes --> Nomad[Nomad]
  Nodes --> Traefik[Traefik]
  Nodes --> Postgres[PostgreSQL]
  Nodes --> CP[StackForge control-plane service]
  CLI --> API[Control-plane API]
  API --> DomainStore[Memory or PostgreSQL domain store]
  CLI --> DomainPool[Local domain-pool.yaml]
  DomainPool --> Cloudflare[Cloudflare DNS API]
```

The main entry point is `cmd/stackforge/main.go`. It calls `internal/stackforge/cli.Execute()`, which registers all CLI commands.

Core implementation lives under:

- `internal/stackforge`: CLI-side install, validation, inventory, firewall, SSH, secrets, backup, rollback, domain pool, and safety packages.
- `internal/controlplane`: HTTP API, auth, domain storage, Cloudflare, Consul, Nomad, reconciliation, and Traefik tag helpers.
- `migrations`: SQL schema for the control-plane domain tables.
- `examples`: example YAML cluster configs.
- `scripts`: release installer script.

## Build

```bash
go build -o bin/stackforge ./cmd/stackforge
```

## Install a Release

Install the latest Linux release:

```bash
curl -fsSL https://raw.githubusercontent.com/cploutarchou/StackForge/master/scripts/install-stackforge.sh | sh
```

Install a specific release:

```bash
curl -fsSL https://raw.githubusercontent.com/cploutarchou/StackForge/master/scripts/install-stackforge.sh | VERSION=v0.1.1 sh
```

Installer environment variables:

- `INSTALL_DIR`: install directory. Default: `/usr/local/bin`.
- `BINARY_NAME`: installed binary name. Default: `stackforge`.
- `VERSION`: release version. Default: `latest`.
- `STACKFORGE_REPO`: GitHub repository. Default: `cploutarchou/StackForge`.
- `VERIFY_CHECKSUM`: verify release checksum. Default: `true`.

## Quick Start

Build the CLI:

```bash
go build -o bin/stackforge ./cmd/stackforge
```

Review an example config:

```bash
cp examples/stackforge.single-node.yaml stackforge.yaml
```

Replace the example IPs, domains, admin CIDRs, SSH settings, and admin API key before any live install.

Run a dry-run install:

```bash
bin/stackforge install --dry-run --config stackforge.yaml
```

Run validation without live SSH:

```bash
bin/stackforge validate --config stackforge.yaml
```

Run live validation:

```bash
bin/stackforge validate --config stackforge.yaml --live --production
```

For a live production install, StackForge requires explicit production confirmation:

```bash
bin/stackforge install \
  --config stackforge.yaml \
  --confirm-production
```

In a non-interactive shell, also pass `--yes` after reviewing the plan:

```bash
bin/stackforge install \
  --config stackforge.yaml \
  --confirm-production \
  --yes
```

## Basic Usage

Print the version:

```bash
bin/stackforge version
```

Bootstrap SSH key access:

```bash
bin/stackforge nodes bootstrap \
  --node node-1=203.0.113.10 \
  --ssh-user root \
  --ssh-port 22 \
  --public-key ~/.ssh/id_ed25519.pub \
  --auth private-key
```

Plan firewall rules:

```bash
bin/stackforge firewall plan --config stackforge.yaml
```

Show inventory:

```bash
bin/stackforge inventory show --cluster stackforge-production
```

Start the control-plane API:

```bash
STACKFORGE_ADMIN_API_KEYS="$(openssl rand -base64 32)" \
DATABASE_URL="postgres://stackforge:password@127.0.0.1:5432/stackforge?sslmode=disable" \
bin/stackforge serve
```

Add a domain through the API:

```bash
STACKFORGE_API_URL=http://127.0.0.1:8080 \
STACKFORGE_ADMIN_API_KEY=your-admin-key \
bin/stackforge domains add app.example.com \
  --tenant tenant-1 \
  --service app \
  --port 8080
```

Add a local domain-pool entry:

```bash
bin/stackforge domains pool add app.example.com \
  --target traefik \
  --target-value 203.0.113.10 \
  --record-type A \
  --zone-id your-cloudflare-zone-id
```

## Production Safety

Do not run live commands with the example configs unchanged.

The example configs contain documentation IP ranges and `example.com` domains. Live install refuses these values unless `--allow-example-config` is passed. Production installs also require `--confirm-production`.

Important safeguards:

- `allowed_admin_cidrs` cannot include `0.0.0.0/0` or `::/0`.
- `allowed_ssh_cidrs` cannot include public CIDRs unless `--allow-public-ssh` is passed.
- Traefik dashboard requires basic auth when enabled.
- Database nodes must not use public addresses for database traffic.
- UFW is required unless `--allow-no-firewall` is passed.
- Live-changing operations require confirmation text or `--yes`.

## Deeper Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [CLI reference](docs/CLI_REFERENCE.md)
- [Configuration](docs/CONFIGURATION.md)
- [Server onboarding](docs/SERVER_ONBOARDING.md)
- [Infrastructure components](docs/INFRA_COMPONENTS.md)
- [Production safety](docs/PRODUCTION_SAFETY.md)
- [Operations](docs/OPERATIONS.md)
- [Development](docs/DEVELOPMENT.md)
- [Roadmap recommendations](docs/ROADMAP_RECOMMENDATIONS.md)

## Tests

```bash
go test ./...
```

## License

StackForge is released under the [Apache License 2.0](LICENSE).
