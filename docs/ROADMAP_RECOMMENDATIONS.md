# Roadmap Recommendations

This roadmap is based on gaps found in the current codebase.

## P0: High Priority

### Make OS Support Consistent

Current state:

- Install shell checks accept Ubuntu 26.04.
- Live validation and `osdetect` accept Ubuntu 22.04 and 24.04.

Recommendation:

Use one shared OS support policy across install, validate, and osdetect.

### Fix Database Engine Contract

Current state:

- Config validation accepts `postgres` and `mysql`.
- Live install supports PostgreSQL only.

Recommendation:

Either remove `mysql` from validation for now or implement MySQL install, backup, firewall, validation, and verification.

### Add Host Key Verification Policy

Current state:

- SSH uses `ssh.InsecureIgnoreHostKey()`.

Recommendation:

Add known_hosts support or an explicit host key policy flag. Default should be safer than blindly trusting the first connection for production.

### Wire Real Control-Plane Reconciliation

Current state:

- API has domain apply/reconcile endpoints.
- HTTP server does not wire Cloudflare, Consul, Nomad, or Traefik clients into reconciliation.
- Reconcile refuses to pretend success.

Recommendation:

Create clients from `cpconfig.Config`, pass them to reconciliation, and persist resulting domain state changes and audit logs.

### Expose Verification Token Command

Current state:

- API has `POST /api/v1/domains/{id}/verification-token`.
- CLI has no direct command for it.

Recommendation:

Add:

```bash
stackforge domains verification-token DOMAIN_OR_ID
```

### Clarify Or Complete Uninstall

Current state:

- `uninstall` marks local inventory and writes a wipe marker.
- It does not remove remote services or packages.

Recommendation:

Either document it as local-only in command help or implement a full remote uninstall plan with strong confirmation and dry-run.

## P1: Medium Priority

### Implement Live Consul Commands

Current registered commands refuse:

- `consul status`
- `consul members`
- `consul kv get`
- `consul kv put`
- `consul snapshot save`
- `consul snapshot restore`

Recommendation:

Use inventory and generated secrets to configure the Consul client or SSH into a Consul server.

### Implement Live Nomad Commands

Current registered commands refuse:

- `nomad status`
- `nomad nodes`
- `nomad jobs`
- `nomad allocations`
- `nomad drain-node`

Recommendation:

Use inventory and generated secrets to configure the Nomad client. Add strong safety checks for drain operations.

### Implement Live Traefik Commands

Current registered commands refuse:

- `traefik status`
- `traefik routes`
- `traefik reload`
- `traefik logs`

Recommendation:

Use SSH/systemd and Traefik config files for status/logs. Make route changes flow through the domain/routing system, not ad hoc edits.

### Make Component Install Semantics Clearer

Current state:

- `components install docker` performs real Docker install.
- `components install consul/nomad/traefik/postgres/control-plane` mostly refuses and points to main install.

Recommendation:

Either make `components install` a real component installer with config/secrets support, or rename/scope it to `components check` plus Docker/base package helpers.

### Improve Control-Plane Audit Logs

Current state:

- Domain-pool writes JSONL audit logs.
- API audit endpoints return empty arrays.
- PostgreSQL schema has `audit_logs`, but handlers do not write logs.

Recommendation:

Add API audit persistence for domain create, verify, delete, DNS apply, routing apply, and reconcile.

### Add Scheduler For Backups

Current state:

- Config has `database.backup_enabled` and `backup_schedule`.
- Backups are manual CLI commands.

Recommendation:

Either implement scheduled backups or remove/mark schedule config as future.

### Improve Secret Lifecycle

Current state:

- Secrets are generated and persisted locally.
- Remote env file is deployed.

Recommendation:

Add rotation commands, secret age warnings, and clearer handling for externally provided admin keys.

## P2: Lower Priority

### Use `--verbose`, `--log-level`, And `--no-color`

Current state:

- Flags are registered but not broadly used.

Recommendation:

Add consistent logging and output formatting.

### Improve Interactive UX

Current state:

- `nodes onboard` has a simple prompt flow.

Recommendation:

Add validation during prompts, clearer summaries, and safer defaults for production.

### Add More Integration Tests

Current tests are mostly unit tests.

Recommended tests:

- CLI command execution tests with temp state dirs.
- Installer dry-run golden tests.
- Domain API tests with PostgreSQL test container or fixture.
- Firewall plan tests for multi-node role-specific exposure.
- SSH executor tests with a test SSH server if practical.

### Improve Release Documentation

Current state:

- Release workflow exists.
- Installer script exists.

Recommendation:

Document release process, versioning, checksum verification, and how `Version` is injected.

### Add Example Config Variants

Recommendation:

Add safe template files that use placeholder strings instead of documentation IPs where possible. Keep live install safety checks strict.

## Security Improvements

- Add SSH known_hosts policy.
- Add API audit logging.
- Add secret rotation.
- Add explicit confirmation for `--allow-example-config`, `--allow-public-ssh`, and `--allow-no-firewall` in production.
- Add static checks for shell command quoting where config values enter shell commands.
- Add least-privilege non-root systemd user for the control-plane service if feasible.
- Avoid running the control-plane service as root unless required.

## UX Improvements

- Add `domains verification-token`.
- Improve command help descriptions for stub/refusal commands.
- Print state file paths consistently after commands.
- Add `stackforge doctor` to summarize config, state, inventory, and live reachability.
- Add `stackforge init-config` to create a safe starter config.

## Testing Improvements

- Add CLI-level tests for command parsing and required flags.
- Add tests for all refusal commands so behavior is intentional.
- Add tests for generated shell commands that include config values.
- Add tests for control-plane PostgreSQL migrations.
- Add tests for docs examples where practical.

## CI/CD Improvements

- Add `go test -race ./...` where runtime permits.
- Add docs link check.
- Add shellcheck for `scripts/install-stackforge.sh`.
- Add artifact smoke test that runs `stackforge --help` and `stackforge version`.
- Add a release dry-run workflow for pull requests.
