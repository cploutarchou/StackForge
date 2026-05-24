# Configuration

StackForge uses two kinds of configuration:

- YAML cluster config for CLI install, validation, firewall, and onboarding.
- Environment variables for the control-plane API and some CLI API/DNS commands.

It also writes local state files under the cluster state directory.

## YAML Cluster Config

Example files live in `examples/`.

Main examples:

- `examples/stackforge.single-node.yaml`
- `examples/stackforge.three-node.yaml`
- `examples/stackforge.five-node.yaml`
- `examples/stackforge.custom.yaml`

Use examples as templates only. Replace example IPs, domains, CIDRs, and credentials before live use.

## Config File Shape

```yaml
cluster:
  name: stackforge-production
  environment: production
  datacenter: dc1

ssh:
  user: root
  port: 22
  private_key_path: ~/.ssh/id_ed25519
  copy_public_key: true

nodes:
  - name: node-1
    address: 10.0.0.11
    public_address: 203.0.113.11
    roles: [consul-server, nomad-server, nomad-client, traefik, database, control-plane]

network:
  private_interface: eth1
  public_interface: eth0
  allowed_admin_cidrs: [1.2.3.4/32]
  allowed_ssh_cidrs: [1.2.3.4/32]
  allow_public_internal_communication: false

consul:
  version: latest-stable
  acl_enabled: true
  encrypt_gossip: true
  ui_enabled: true

nomad:
  version: latest-stable
  acl_enabled: true
  ui_enabled: true
  client_enabled: true

traefik:
  version: latest-stable
  entrypoints: {web: 80, websecure: 443}
  cert_resolver: letsencrypt
  email: admin@example.com
  dashboard_enabled: true
  dashboard_domain: traefik.example.com
  dashboard_basic_auth: true

database:
  engine: postgres
  mode: single
  backup_enabled: true
  backup_schedule: "0 3 * * *"

control_plane:
  domain: control.example.com
  api_port: 8080
  admin_api_keys: [change-me]
  reconciler_enabled: true
  reconciler_interval_seconds: 300

cloudflare:
  api_token_env: CLOUDFLARE_API_TOKEN
  default_zone_id: optional
```

## Defaults

Defaults are applied in `internal/stackforge/config/config.go`.

| Field | Default |
| --- | --- |
| `cluster.environment` | `production` |
| `cluster.datacenter` | `dc1` |
| `ssh.user` | `root` |
| `ssh.port` | `22` |
| `control_plane.api_port` | `8080` |
| `database.engine` | `postgres` |
| `consul.version` | `latest-stable` |
| `nomad.version` | `latest-stable` |
| `traefik.version` | `latest-stable` |

## Required YAML Fields

The config validator requires:

- `cluster.name`
- at least one item in `nodes`
- `control_plane.domain`
- at least one value in `control_plane.admin_api_keys`
- each node must have `name` and `address`
- at least one `database` role
- at least one `control-plane` role
- odd number of `consul-server` roles
- odd number of `nomad-server` roles
- valid CIDRs in `network.allowed_admin_cidrs` and `network.allowed_ssh_cidrs`

Firewall planning also requires:

- `network.allowed_admin_cidrs`
- `network.allowed_ssh_cidrs`

## Cluster Fields

| Field | Required | Notes |
| --- | --- | --- |
| `cluster.name` | yes | Used for local state path. Example/demo names are blocked for live install unless explicitly allowed. |
| `cluster.environment` | no | Defaults to `production`. Production requires `--confirm-production` for live actions. |
| `cluster.datacenter` | no | Defaults to `dc1`. Used in Consul and Nomad config. |

## SSH Fields

| Field | Required | Default | Notes |
| --- | --- | --- | --- |
| `ssh.user` | no | `root` | Used by the SSH executor. Non-root users require passwordless sudo for sudo commands. |
| `ssh.port` | no | `22` | SSH port. |
| `ssh.private_key_path` | live SSH requires an auth method | empty | Path to private key. `~` is expanded. |
| `ssh.copy_public_key` | no | `false` | Used by onboarding to decide whether to bootstrap public key access. |

Passwords are never stored in config, inventory, reports, or environment variables.

## Node Fields

| Field | Required | Notes |
| --- | --- | --- |
| `name` | yes | Unique node name used in inventory and reports. |
| `address` | yes | Private/internal address used for install and component traffic. |
| `public_address` | no | Public IP/hostname used for public endpoints and bootstrap fallback. |
| `roles` | yes for useful install | Determines which components are installed. |

Known roles:

- `consul-server`
- `nomad-server`
- `nomad-client`
- `traefik`
- `database`
- `control-plane`
- `docker-host`

`docker-host` is used by component planning. The main install flow does not install Docker as a normal role step; `nodes onboard` can install Docker before the main component install.

## Network Fields

| Field | Required | Notes |
| --- | --- | --- |
| `private_interface` | no | Live validation checks it when set. |
| `public_interface` | no | Live validation checks it when set. |
| `allowed_admin_cidrs` | required for firewall | CIDRs allowed to access admin APIs and dashboards. Must not be public. |
| `allowed_ssh_cidrs` | required for firewall | CIDRs allowed to access SSH. Public values require `--allow-public-ssh`. |
| `allow_public_internal_communication` | no | Dangerous. Can trigger safety errors for database/internal API exposure. |

## Component Fields

### Consul

| Field | Default | Notes |
| --- | --- | --- |
| `version` | `latest-stable` | HashiCorp apt repo is used. The value is not pinned in the apt install command. |
| `acl_enabled` | `false` | Adds ACL config and token verification. |
| `encrypt_gossip` | `false` | Adds gossip encryption when generated secret is available. |
| `ui_enabled` | `false` | Controls Consul UI config. |

### Nomad

| Field | Default | Notes |
| --- | --- | --- |
| `version` | `latest-stable` | HashiCorp apt repo is used. The value is not pinned in the apt install command. |
| `acl_enabled` | `false` | Enables ACL stanza and bootstrap handling. |
| `ui_enabled` | `false` | Present in config, not used directly in generated Nomad config. |
| `client_enabled` | `false` | Enables client mode along with role checks. |

### Traefik

| Field | Default | Notes |
| --- | --- | --- |
| `version` | `latest-stable` | `latest-stable` maps to Traefik `v3.3.3` in current code. |
| `entrypoints` | none | Example uses web 80 and websecure 443. Install command writes fixed 80/443 entrypoints. |
| `cert_resolver` | none | Used to write ACME config when email is also set. |
| `email` | none | Used for ACME config. |
| `dashboard_enabled` | `false` | Requires `dashboard_basic_auth: true`. |
| `dashboard_domain` | none | Validated when dashboard is enabled. |
| `dashboard_basic_auth` | `false` | Must be true when dashboard is enabled. |

### Database

| Field | Default | Notes |
| --- | --- | --- |
| `engine` | `postgres` | Validator also accepts `mysql`, but live install supports PostgreSQL only. |
| `mode` | none | Example uses `single`. Not deeply used by install code. |
| `backup_enabled` | `false` | Present in config. Backup command is manual in current code. |
| `backup_schedule` | empty | Present in config. No scheduler is implemented in current code. |

### Control Plane

| Field | Default | Notes |
| --- | --- | --- |
| `domain` | required | Must be a real non-placeholder domain for validation. |
| `api_port` | `8080` | Used by install and firewall plan. |
| `admin_api_keys` | required | First non-empty non-`change-me` key is reused; otherwise generated secrets can create one during install. Config validation still requires a value. |
| `reconciler_enabled` | `false` | Present in config. The current HTTP server does not start a background reconciler loop. |
| `reconciler_interval_seconds` | `0` | Present in config. |

### Cloudflare

| Field | Required | Notes |
| --- | --- | --- |
| `api_token_env` | required when Cloudflare is enabled | Environment variable name that should contain the token. |
| `default_zone_id` | optional | Any non-empty value other than `optional` makes Cloudflare safety/token checks active. |

## Control-Plane Environment Variables

`stackforge serve` reads these with `internal/controlplane/config.FromEnv()`.

| Variable | Default | Required | Notes |
| --- | --- | --- | --- |
| `STACKFORGE_ENV` | `production` | no | Production mode requires `DATABASE_URL`. |
| `STACKFORGE_HTTP_ADDR` | `:8080` | no | HTTP listen address. |
| `STACKFORGE_STATE_DIR` | `/var/lib/stackforge` | no | State dir value in control-plane config. |
| `STACKFORGE_LOG_LEVEL` | `info` | no | Stored in config. |
| `STACKFORGE_ADMIN_API_KEYS` | empty | yes for `serve` | Comma-separated API bearer tokens. |
| `STACKFORGE_RECONCILER_ENABLED` | `true` | no | Config only; no background loop is currently started. |
| `STACKFORGE_RECONCILER_INTERVAL_SECONDS` | `300` | no | Config only in current server. |
| `STACKFORGE_READY_PROTECTED` | `true` | no | Protects `/ready` with auth when true. |
| `DATABASE_URL` | empty | yes in production | PostgreSQL connection string. |
| `ALLOW_WILDCARD_DOMAINS` | `false` | no | Allows wildcard domain creation through API. |
| `CLOUDFLARE_API_TOKEN` | empty | needed for Cloudflare clients/domain pool DNS | Bearer token for Cloudflare API. |
| `CLOUDFLARE_ACCOUNT_ID` | empty | no | Loaded but not used by current code. |
| `CLOUDFLARE_DEFAULT_ZONE_ID` | empty | no | Loaded but not wired into API reconciliation currently. |
| `NOMAD_ADDR` | empty | required by Nomad client methods | Not wired into HTTP reconciliation in current server. |
| `NOMAD_TOKEN` | empty | optional | Used by Nomad client when configured. |
| `CONSUL_HTTP_ADDR` | empty | required by Consul client methods | Not wired into HTTP reconciliation in current server. |
| `CONSUL_HTTP_TOKEN` | empty | optional | Used by Consul client when configured. |
| `TRAEFIK_CERT_RESOLVER` | `letsencrypt` | no | Used by Traefik tag helper config. |
| `TRAEFIK_ENTRYPOINT` | `websecure` | no | Used by Traefik tag helper config. |

`.env.example` lists these variables.

## CLI Environment Variables

Some CLI commands read environment variables directly.

| Variable | Used By | Notes |
| --- | --- | --- |
| `STACKFORGE_API_URL` | `domains` API commands | Defaults to `http://127.0.0.1:8080`. |
| `STACKFORGE_ADMIN_API_KEY` | `domains` API commands | Preferred bearer token variable. |
| `STACKFORGE_ADMIN_API_KEYS` | `domains` API commands and `serve` | CLI uses the first comma-separated key if `STACKFORGE_ADMIN_API_KEY` is empty. |
| `STACKFORGE_DOMAIN_VERIFICATION_TOKEN` | `domains verify` | Sent to `/api/v1/domains/{id}/verify`. |
| `CLOUDFLARE_API_TOKEN` | `domains pool apply-dns` | Used for Cloudflare DNS upsert. |

## Release Installer Environment Variables

`scripts/install-stackforge.sh` reads:

| Variable | Default | Notes |
| --- | --- | --- |
| `STACKFORGE_REPO` | `cploutarchou/StackForge` | GitHub repository. |
| `INSTALL_DIR` | `/usr/local/bin` | Install destination. |
| `BINARY_NAME` | `stackforge` | Installed binary name. |
| `VERSION` | `latest` | Release tag or latest release. |
| `VERIFY_CHECKSUM` | `true` | Set to `false` only after reviewing the release source. |

## Local State Files

Default state path:

```text
~/.stackforge/<cluster>/
```

Important files:

| Path | Purpose | Mode |
| --- | --- | --- |
| `inventory.yaml` | Desired and observed cluster state. | `0600` |
| `generated-secrets.yaml` | Local generated secrets. | `0600` |
| `install-report.json` | Detailed install report. | `0600` |
| `bootstrap-report.json` | SSH bootstrap report. | `0600` |
| `domain-pool.yaml` | Local domain pool. | `0600` |
| `domain-pool-audit.jsonl` | Domain-pool audit records. | `0600` |
| `backups/<id>/backup-manifest.json` | Backup manifest and checksums. | `0600` |
| `backups/<id>/restore-report.json` | Restore report. | `0600` |
| `rollback/<id>.json` | Rollback record. | `0600` |

The install flow also writes root-level generated reports:

- `stackforge-install-report.json`
- `STACKFORGE_INSTALL_REPORT.md`

## Generated Secrets

`generated-secrets.yaml` can contain:

- `consul_gossip_key`
- `consul_bootstrap_token`
- `nomad_bootstrap_token`
- `stackforge_admin_api_key`
- `database_password`
- `traefik_dashboard_password`
- `internal_service_token`
- `created_at`

Remote control-plane env is written to:

```text
/etc/stackforge/stackforge.env
```

It includes values such as:

- `STACKFORGE_ENV=production`
- `STACKFORGE_ADMIN_API_KEYS`
- `STACKFORGE_INTERNAL_SERVICE_TOKEN`
- `CONSUL_HTTP_TOKEN`
- `NOMAD_TOKEN`
- `STACKFORGE_DATABASE_PASSWORD`
- `DATABASE_URL`
- `TRAEFIK_DASHBOARD_PASSWORD` when needed

## Security Notes

- Do not commit config files containing real API keys or secrets.
- Do not commit `generated-secrets.yaml`.
- Keep the state directory readable only by trusted operators.
- Replace `change-me` before live use.
- Avoid `root` SSH for long-running production use when a sudo-capable user can be used.
- Do not use public `allowed_admin_cidrs`.
- Avoid public `allowed_ssh_cidrs`; if used, it requires explicit `--allow-public-ssh`.
- Treat `--allow-example-config`, `--allow-no-firewall`, and `--allow-public-ssh` as break-glass flags.
