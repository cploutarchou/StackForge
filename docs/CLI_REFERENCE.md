# CLI Reference

The CLI binary is `stackforge`.

Global flags are available on all commands:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--config` | empty | YAML config file path. Required by install, validate, firewall, and some node flows. |
| `--state-dir` | `~/.stackforge/<cluster>` through config helper | Base state directory. The cluster name is appended. |
| `--cluster` | inferred from config or `stackforge-production` | Cluster name used for state lookup. |
| `--output` | `text` | Output format. Supports `text` and `json`. |
| `--dry-run` | `false` | Plan without live changes where supported. |
| `--yes` | `false` | Skip interactive confirmation prompts for dangerous/live operations. |
| `--verbose` | `false` | Registered but not widely used. |
| `--log-level` | `info` | Registered but not widely used by the CLI. |
| `--no-color` | `false` | Registered but not currently used for formatting. |
| `--allow-no-firewall` | `false` | Allow install/validation without managed UFW firewall. |
| `--allow-example-config` | `false` | Allow live commands to use example/demo values. |
| `--allow-public-ssh` | `false` | Allow `allowed_ssh_cidrs` to include `0.0.0.0/0` or `::/0`. |
| `--confirm-production` | `false` | Required for live actions against production configs. |

## `stackforge version`

Prints the compiled version string. The default development value is `dev`.

Example:

```bash
stackforge version
stackforge version --output json
```

Expected behavior:

- Text output prints only the version.
- JSON output prints `{"version":"..."}`.

## `stackforge install`

Installs or resumes StackForge infrastructure.

Flags:

- `--resume`: skip steps already completed in the previous local install report.
- `--ssh-key`: override `ssh.private_key_path`.
- `--ssh-user`: override `ssh.user`.

Required inputs:

- `--config` is required. The interactive install wizard is not implemented for `stackforge install`.
- For live production installs, pass `--confirm-production`.
- For non-interactive live installs, pass `--yes` after reviewing the plan.

Example dry run:

```bash
stackforge install --dry-run --config examples/stackforge.single-node.yaml
```

Example live install:

```bash
stackforge install \
  --config stackforge.yaml \
  --confirm-production
```

Expected behavior:

- Loads and validates YAML config.
- Runs safety checks for live installs.
- Generates or loads secrets.
- Writes inventory and reports under the state directory.
- Runs remote install steps over SSH unless `--dry-run`.

Safety notes:

- Refuses production live install without `--confirm-production`.
- Refuses example/demo values unless `--allow-example-config`.
- Requires typed confirmation unless `--yes`.

### `stackforge install report`

Prints the saved install report from `<state-dir>/<cluster>/install-report.json`.

Example:

```bash
stackforge install report --cluster stackforge-production
```

### `stackforge install resume-status`

Shows failed steps and suggested recovery from the saved install report.

Example:

```bash
stackforge install resume-status --cluster stackforge-production
```

## `stackforge status`

Shows cluster status from inventory. If `--config` is provided and the command is not a dry run, it refreshes inventory over SSH first.

Example:

```bash
stackforge status --cluster stackforge-production
stackforge status --config stackforge.yaml --output json
```

Required inputs:

- Existing `inventory.yaml` in the state directory.

## `stackforge inventory show`

Prints the saved inventory.

Example:

```bash
stackforge inventory show --cluster stackforge-production
```

## `stackforge inventory refresh`

Refreshes observed inventory from live nodes when an executor can be built from `--config`.

Example:

```bash
stackforge inventory refresh --config stackforge.yaml
```

Expected behavior:

- Runs a remote observation command over SSH.
- Updates OS, kernel, IPs, firewall, listening ports, versions, services, leaders, and health fields.
- If no executor is configured, records a warning and marks health as inventory-only.

## `stackforge nodes bootstrap`

Bootstraps passwordless SSH access.

Flags:

- `--node name=public-ip-or-hostname`: repeatable non-interactive node spec.
- `--ssh-user`: SSH username. Default: `root`.
- `--ssh-port`: SSH port. Default: `22`.
- `--public-key`: local public key path. Default: `~/.ssh/id_ed25519.pub`.
- `--auth`: `password` or `private-key`. Default: `private-key`.

Examples:

```bash
stackforge nodes bootstrap \
  --node node-1=203.0.113.10 \
  --ssh-user root \
  --ssh-port 22 \
  --public-key ~/.ssh/id_ed25519.pub \
  --auth private-key
```

```bash
stackforge nodes bootstrap \
  --node node-1=203.0.113.10 \
  --auth password
```

Expected behavior:

- Validates the public key file.
- Prints a plan and asks for confirmation unless `--dry-run` or `--yes`.
- For password auth, securely prompts for the password.
- Appends the public key to `~/.ssh/authorized_keys` idempotently.
- Verifies key-based SSH access.
- Updates inventory without storing the password.

Safety notes:

- Password auth is only for initial bootstrap.
- Passwords are not read from flags or environment variables.
- Passwords are passed as secrets for redaction.

## `stackforge nodes onboard`

Runs the higher-level onboarding flow.

Examples:

```bash
stackforge nodes onboard --dry-run --config stackforge.yaml
```

```bash
stackforge nodes onboard --config stackforge.yaml --confirm-production
```

Expected behavior with `--config`:

- Loads config.
- Creates bootstrap nodes from config.
- Plans SSH key copy, Docker install, firewall behavior, and full component install.
- Runs `bootstrap.Run`, Docker install through `components.RunInstall`, main install through `install.Run`, then `verify.Run`.

Expected behavior without `--config`:

- Requires an interactive TTY.
- Prompts for cluster, environment, admin CIDRs, SSH CIDRs, control-plane domain, server details, roles, SSH key path, and install choices.

Safety notes:

- Live execution asks for confirmation unless `--yes`.
- The main install still uses safety checks.

## `stackforge nodes list`

Prints nodes from inventory.

Example:

```bash
stackforge nodes list --cluster stackforge-production
```

## `stackforge nodes add`

Adds nodes from `--config` into existing inventory only.

Example:

```bash
stackforge nodes add --config stackforge.yaml
```

Expected behavior:

- Compares config nodes with inventory nodes.
- Adds missing nodes with `pending-install` health.
- Prints the next command: `stackforge install --resume --config <path>`.

Safety notes:

- Does not perform live installation.
- Does not drain Nomad, adjust Consul quorum, or modify remote nodes.

## `stackforge nodes remove NODE`

Removes a node from local inventory only.

Example:

```bash
stackforge nodes remove node-2 --cluster stackforge-production
```

Safety notes:

- Does not perform live Nomad/Consul drain.
- Does not remove remote services.

## `stackforge components install COMPONENT`

Installs or checks a component across matching inventory nodes.

Parent flag:

- `--node`: limit to one node name.

Supported component names from code:

- `base-packages`
- `docker`
- `consul`
- `nomad`
- `traefik`
- `postgres`
- `stackforge-control-plane`
- `all`

Examples:

```bash
stackforge components install docker --config stackforge.yaml --dry-run
stackforge components --node node-1 install docker --config stackforge.yaml
```

Expected behavior:

- `base-packages` and `docker` have direct install commands.
- Consul, Nomad, Traefik, PostgreSQL, and control-plane component commands mostly verify existing service state or refuse with a message telling the operator to use `stackforge install` or `nodes onboard`.

Safety notes:

- Live install requires confirmation unless `--yes`.
- Full role component install is owned by the main install flow.

## `stackforge components status`

Reads component status from live nodes or inventory.

Example:

```bash
stackforge components status --config stackforge.yaml
stackforge components --node node-1 status --cluster stackforge-production
```

Expected behavior:

- With live SSH, checks Docker, Consul, Nomad, Traefik, PostgreSQL, control-plane service, and listening ports.
- Without live SSH, prints status from inventory versions/services.

## `stackforge firewall plan`

Builds and prints the UFW plan from config.

Example:

```bash
stackforge firewall plan --config stackforge.yaml
```

Required inputs:

- `--config`
- `network.allowed_admin_cidrs`
- `network.allowed_ssh_cidrs`

Safety notes:

- Rejects public admin API exposure.
- Rejects public SSH CIDRs unless `--allow-public-ssh`.

## `stackforge firewall apply`

Applies the UFW plan on every configured node.

Example:

```bash
stackforge firewall apply --config stackforge.yaml
```

Expected behavior:

- Prints planned rules.
- In `--dry-run`, stops before remote changes.
- Otherwise asks for confirmation unless `--yes`.
- Runs UFW backup and UFW commands through SSH.

Safety notes:

- Can affect SSH reachability.
- Review the plan before applying.

## `stackforge domains add DOMAIN`

Creates a domain through the control-plane API.

Flags:

- `--tenant`: tenant id.
- `--service`: target service name.
- `--port`: target service port.

Environment:

- `STACKFORGE_API_URL`: API base URL. Default: `http://127.0.0.1:8080`.
- `STACKFORGE_ADMIN_API_KEY` or `STACKFORGE_ADMIN_API_KEYS`: bearer token source.

Example:

```bash
STACKFORGE_ADMIN_API_KEY=key \
stackforge domains add app.example.com \
  --tenant tenant-1 \
  --service app \
  --port 8080
```

## `stackforge domains list`

Lists domains through the control-plane API.

```bash
STACKFORGE_ADMIN_API_KEY=key stackforge domains list
```

## `stackforge domains verify DOMAIN_OR_ID`

Marks a domain verified through the API.

Environment:

- `STACKFORGE_DOMAIN_VERIFICATION_TOKEN`: token sent in the request body.

```bash
STACKFORGE_ADMIN_API_KEY=key \
STACKFORGE_DOMAIN_VERIFICATION_TOKEN=token \
stackforge domains verify domain-id
```

## API Domain Action Commands

These commands call API endpoints:

- `stackforge domains apply-dns DOMAIN_OR_ID`
- `stackforge domains apply-routing DOMAIN_OR_ID`
- `stackforge domains reconcile DOMAIN_OR_ID`
- `stackforge domains disable DOMAIN_OR_ID`
- `stackforge domains delete DOMAIN_OR_ID`

Safety and behavior:

- They require API auth environment variables.
- API `apply-dns` and `apply-routing` return accepted only after ownership is verified.
- API reconciliation currently refuses real reconciliation because external clients are not wired in the HTTP server.
- `disable` deletes routing through the API endpoint.
- `delete` soft-deletes the domain record.

## `stackforge deploy`

Deploys a compose-style StackForge manifest to a selected cluster node over SSH using Docker Compose.

Flags:

- `--file`: path to deployment YAML manifest (required).
- `--env-file`: env file used for deploy. Default: `.env.stackforge`.
- `--node`: optional node name override. Default target order is `control-plane`, then `nomad-server`, then first inventory node.
- `--auto-dns`: auto-apply Cloudflare DNS after deploy when token and domains are present. Default: `true`.
- `--app-domain`: app domain override for auto DNS (fallbacks: `APP_DOMAIN`, `VITE_CLIENT_HOST`).
- `--api-domain`: API domain override for auto DNS (fallbacks: `API_DOMAIN`, `VITE_API_URL`, `VITE_API_BASE_URL`, `VITE_AUTH_BASE_URL`).
- `--dns-zone-id`: optional Cloudflare zone id override.
- `--dns-proxied`: enable Cloudflare proxy for auto-created records.
- `--no-build`: skip image build and run `docker compose up -d` without `--build`.

Examples:

```bash
stackforge deploy \
  --config stackforge.yaml \
  --file /home/chris/workspace/dydx-trading-bot/stackforge-deployment.yaml \
  --env-file /home/chris/workspace/dydx-trading-bot/.env.stackforge

stackforge deploy \
  --config stackforge.yaml \
  --file ./stackforge-deployment.yaml \
  --node nomad-cp-01 \
  --no-build
```

Expected behavior:

- Validates that the manifest includes a non-empty `services` map.
- Selects a deployment node from inventory/config.
- Copies manifest (and optional env file) to `/opt/stackforge/deployments/<manifest-name>/` on the target.
- Uses `--env-file` (default `.env.stackforge`) and passes it explicitly to Docker Compose (`--env-file`), avoiding implicit `.env` fallback.
- Runs `docker compose up -d [--build]` remotely via SSH.
- If private registry credentials are set in `.env.stackforge`, logs into registries on the target before compose pull/build:
  - GHCR via `GHCR_USERNAME` + `GHCR_TOKEN` (or `GITHUB_API_TOKEN` fallback)
  - Docker Hub via `DOCKERHUB_USERNAME` + `DOCKERHUB_TOKEN`
  - Custom registry via `DOCKER_REGISTRY`, `DOCKER_REGISTRY_USERNAME`, `DOCKER_REGISTRY_PASSWORD`
- If `CLOUDFLARE_API_TOKEN` is set and domains are available, automatically manages A records through Cloudflare and stores domain entries in the local domain pool.

Manifest-level DNS configuration (`stackforge-deployment.yaml`):

```yaml
name: dydx-trading-bot
services:
  # ...

auto_dns:
  enabled: true
  app_domain: app.example.com
  api_domain: api.example.com
  domains:
    - admin.example.com
  zone_id: your-cloudflare-zone-id
  proxied: true
```

Precedence order:

- CLI flags override manifest values.
- Manifest `auto_dns` values override env-file fallbacks.
- Env-file fallbacks are used when neither flags nor manifest provide domains/zone.

Safety notes:

- Requires typed confirmation unless `--yes`.
- `--dry-run` prints deployment plan only.
- The target node must have Docker Compose installed.
- Auto DNS requires a public target IP and valid Cloudflare API token permissions.
- Private registry login requires valid credentials for the target registry; token values are handled as secrets and not printed verbatim.
- Current implementation deploys to one selected host (not native multi-node Nomad job scheduling yet).

### `stackforge deploy init`

Generates an editable deploy scaffold: `stackforge-deployment.yaml` plus `.env.stackforge` with required credential placeholders.

Flags:

- `--output`, `-o`: output path for the generated YAML. Default: `stackforge-deployment.yaml`.
- `--force`: overwrite existing file.
- `--random-secrets`: generate secure random values in `.env.stackforge` for app secrets and DB password.
- `--print-secrets-summary`: print masked secret previews (for verification without exposing full values).
- `--quiet`: print only generated file paths (one per line) for scripting/CI.

Example:

```bash
stackforge deploy init

stackforge deploy init --output ./deploy/stackforge-deployment.yaml --force

stackforge deploy init --random-secrets

stackforge deploy init --random-secrets --print-secrets-summary

stackforge deploy init --random-secrets --quiet
```

Expected behavior:

- Writes a complete template including `auto_dns` (`enabled`, explicit domains, `proxied`) and starter `services`.
- Writes `.env.stackforge` in the same directory for deploy-time credentials and app/domain values.
- With `--random-secrets`, writes cryptographically random secret values instead of placeholders.
- With `--print-secrets-summary`, returns masked values (for example `abcd...wxyz`) for key generated fields.
- With `--quiet`, outputs only the deployment YAML path and `.env.stackforge` path.
- If both `--quiet` and `--print-secrets-summary` are set, `--quiet` wins and summary output is suppressed.
- Refuses to overwrite an existing file unless `--force` is set.

## `stackforge domains pool add DOMAIN`

Adds a domain to the local domain pool.

Flags:

- `--target`: `traefik`, `control-plane`, or `custom`. Default: `traefik`.
- `--target-value`: DNS target value.
- `--record-type`: `A` or `CNAME`. Default: `A`.
- `--zone-id`: Cloudflare zone id.
- `--proxied`: enable Cloudflare proxying.
- `--allow-internal`: allow private, loopback, or unspecified IP targets.
- `--allow-wildcard`: allow wildcard domains.

Example:

```bash
stackforge domains pool add app.example.com \
  --target traefik \
  --target-value 203.0.113.10 \
  --record-type A \
  --zone-id zone-id
```

State:

- Writes `<state-dir>/<cluster>/domain-pool.yaml`.

## `stackforge domains pool list`

Lists local domain-pool entries.

```bash
stackforge domains pool list --cluster stackforge-production
```

## `stackforge domains pool remove DOMAIN`

Disables a local domain-pool entry.

```bash
stackforge domains pool remove app.example.com --yes
```

Safety notes:

- Requires confirmation unless `--yes` or `--dry-run`.
- Marks the entry `disabled`; it does not delete Cloudflare DNS.

## `stackforge domains pool apply-dns DOMAIN`

Applies a local domain-pool DNS record through Cloudflare.

Environment:

- `CLOUDFLARE_API_TOKEN`

Example:

```bash
CLOUDFLARE_API_TOKEN=token \
stackforge domains pool apply-dns app.example.com
```

Safety notes:

- Requires confirmation unless `--yes` or `--dry-run`.
- Writes audit records to `domain-pool-audit.jsonl`.

## `stackforge domains pool verify-dns DOMAIN`

Verifies the DNS record for a local domain-pool entry.

```bash
stackforge domains pool verify-dns app.example.com
```

Expected behavior:

- For `A`, checks that the domain resolves to `target_value`.
- For `CNAME`, checks that the CNAME matches `target_value`.

## Consul Commands

Registered commands:

- `stackforge consul status`
- `stackforge consul members`
- `stackforge consul kv get KEY`
- `stackforge consul kv put KEY VALUE`
- `stackforge consul snapshot save PATH`
- `stackforge consul snapshot restore PATH`

Current behavior:

- All return an error: live component client configuration from inventory and secrets is required.
- They intentionally refuse to fake production behavior.

## Nomad Commands

Registered commands:

- `stackforge nomad status`
- `stackforge nomad nodes`
- `stackforge nomad jobs`
- `stackforge nomad allocations`
- `stackforge nomad drain-node`

Current behavior:

- All return a refusal error until live client configuration is implemented.

## Traefik Commands

Registered commands:

- `stackforge traefik status`
- `stackforge traefik routes`
- `stackforge traefik reload`
- `stackforge traefik logs`

Current behavior:

- All return a refusal error until live client configuration is implemented.

## Database Commands

### `stackforge db status`

Registered but refuses live behavior.

### `stackforge db migrate`

Registered but refuses live behavior.

### `stackforge db backup`

Alias for backup run behavior.

```bash
stackforge db backup --cluster stackforge-production
```

### `stackforge db restore BACKUP_ID`

Restores from a backup using the backup restore logic.

```bash
stackforge db restore 20260524T120000Z --yes
```

Safety notes:

- Restore is destructive and requires `--yes` unless `--dry-run`.

## Backup Commands

### `stackforge backup run`

Creates a backup manifest under `<state-dir>/<cluster>/backups/<id>`.

```bash
stackforge backup run --config stackforge.yaml
stackforge backup run --cluster stackforge-production --dry-run
```

Expected behavior:

- Copies inventory.
- Copies redacted generated secrets.
- Plans or runs exports for database, Consul, Nomad, Traefik, and StackForge config.
- Writes checksums.

### `stackforge backup list`

Lists backup manifests.

```bash
stackforge backup list --cluster stackforge-production
```

### `stackforge backup restore BACKUP_ID`

Restores local inventory and attempts live component restore when executor context is available.

```bash
stackforge backup restore 20260524T120000Z --dry-run
stackforge backup restore 20260524T120000Z --yes --config stackforge.yaml
```

Safety notes:

- Requires `--yes` for non-dry-run restore.
- Nomad restore currently requires operator review and refuses automatic job run.

## Rollback Commands

### `stackforge rollback list`

Lists rollback records under `<state-dir>/<cluster>/rollback`.

```bash
stackforge rollback list --cluster stackforge-production
```

### `stackforge rollback apply ROLLBACK_ID`

Applies a rollback record if it is marked safe for automatic apply.

```bash
stackforge rollback apply 20260524T120000Z-node-1-consul --yes --config stackforge.yaml
```

Safety notes:

- Requires `--yes`.
- Refuses unsafe rollback records such as firewall and database.
- Requires live SSH executor context.

## `stackforge validate`

Runs preflight validation.

Flags:

- `--live`: run SSH checks.
- `--production`: validate production safety rules.

Examples:

```bash
stackforge validate --config stackforge.yaml
stackforge validate --config stackforge.yaml --live --production
stackforge validate --config stackforge.yaml --output json
```

Expected behavior:

- Fails on invalid config or unsafe safety findings.
- In non-live mode, prints planned remote checks.
- In live mode, checks SSH, OS, sudo, apt, systemd, disk, RAM, firewall, ports, private IP, and configured interfaces.

## `stackforge verify`

Verifies observed live StackForge state from inventory and SSH.

```bash
stackforge verify --config stackforge.yaml
```

Expected behavior:

- Requires inventory and live SSH access.
- Checks generated secrets permissions.
- Checks services and endpoints according to node roles.
- Runs backup dry-run and uninstall dry-run plan checks.

## `stackforge upgrade`

Plans or starts upgrade safety flow.

Flag:

- `--skip-backup`: skip pre-upgrade backup.

Examples:

```bash
stackforge upgrade --dry-run --cluster stackforge-production
stackforge upgrade --cluster stackforge-production
```

Current behavior:

- Dry-run prints planned upgrade steps.
- Live mode runs a pre-upgrade backup unless skipped, then returns an error because live upgrade needs explicit component target versions and reachable inventory.

## `stackforge uninstall`

Builds or marks an uninstall operation.

Flags:

- `--confirm-destroy`: required for uninstall.
- `--preserve-data`: preserve remote and local data.
- `--wipe-data`: write a marker that remote data wipe requires manual review.

Examples:

```bash
stackforge uninstall --cluster stackforge-production
stackforge uninstall --cluster stackforge-production --confirm-destroy --preserve-data
```

Current behavior:

- Without `--confirm-destroy`, prints the plan and returns an error.
- With confirmation, marks inventory as `uninstalled`.
- It does not remove remote services or packages.

## `stackforge serve`

Runs the StackForge control-plane API.

Environment:

- `STACKFORGE_ADMIN_API_KEYS` is required.
- `DATABASE_URL` is required when `STACKFORGE_ENV=production`.

Example:

```bash
STACKFORGE_ADMIN_API_KEYS="$(openssl rand -base64 32)" \
DATABASE_URL="postgres://stackforge:password@127.0.0.1:5432/stackforge?sslmode=disable" \
stackforge serve
```

Expected behavior:

- Starts HTTP server on `STACKFORGE_HTTP_ADDR`, default `:8080`.
- Serves health endpoints.
- Protects `/api/v1` with bearer token auth.
