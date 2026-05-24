# Production Safety

StackForge has several safeguards because live commands can modify remote servers.

This document describes safeguards found in code, not planned behavior.

## Production Confirmation

For live installs, production configs require:

```bash
--confirm-production
```

Production is detected when:

- `cluster.environment` is `production`, or
- validation is run with `--production`.

Without confirmation, safety check `production-confirmation-required` fails.

## Live Install Confirmation

`stackforge install` asks for typed confirmation unless `--yes` is passed.

Expected text:

```text
install <cluster-name>
```

Example:

```bash
stackforge install --config stackforge.yaml --confirm-production
```

In non-interactive environments, use:

```bash
stackforge install --config stackforge.yaml --confirm-production --yes
```

Only use `--yes` after reviewing dry-run and validation output.

## Example And Demo Value Prevention

Live install refuses example/demo values unless:

```bash
--allow-example-config
```

Blocked examples include:

- cluster names containing `example` or `demo`
- `10.0.0.11`
- `10.0.0.12`
- `10.0.0.13`
- `203.0.113.0/24` documentation IPs
- `example.com`
- subdomains of `example.com`

The example YAML files intentionally contain these values. They are templates, not live configs.

## Public Admin CIDR Prevention

`network.allowed_admin_cidrs` must not include:

- `0.0.0.0/0`
- `::/0`

This is enforced by both safety checks and firewall planning.

Admin CIDRs control access to:

- StackForge API
- Consul HTTP/UI
- Nomad HTTP/UI
- Traefik dashboard when enabled

## Public SSH CIDR Prevention

`network.allowed_ssh_cidrs` must not include:

- `0.0.0.0/0`
- `::/0`

unless:

```bash
--allow-public-ssh
```

Use this only after explicit review.

## Public Database Prevention

Safety checks reject database nodes when:

- a database node has the same public and private address, or
- `allow_public_internal_communication` is true and a database node has a public address.

The PostgreSQL install step also sets `listen_addresses = '127.0.0.1'`.

Verification checks that PostgreSQL is not listening on `0.0.0.0` or `::`.

## Internal API Exposure Prevention

Safety checks reject public internal API exposure when:

- admin CIDRs are public, or
- `allow_public_internal_communication` is true and Consul/Nomad server nodes have public addresses.

Firewall validation also rejects public exposure for:

- database ports
- Nomad ports
- Consul ports
- dashboard ports

Only ports 80 and 443 are allowed publicly by default.

## Traefik Dashboard Protection

If:

```yaml
traefik:
  dashboard_enabled: true
```

then:

```yaml
dashboard_basic_auth: true
```

is required.

The installer refuses a Traefik dashboard that would be public without protection.

## Firewall Safety

StackForge supports managed UFW firewall only.

Without `--allow-no-firewall`, install and validation expect UFW.

Firewall apply:

- backs up current UFW status under `/var/lib/stackforge/firewall`
- resets UFW
- applies StackForge rules
- enables UFW

Risk:

Firewall changes can cut off SSH if CIDRs or ports are wrong. Review `firewall plan` first.

## SSH Bootstrap Safety

Password auth is allowed only for initial key copy.

Rules enforced by code:

- no password flag
- no password environment variable
- secure prompt only
- password passed as a redaction secret
- password not stored in inventory
- key-based SSH verified after copy

## Secrets Safety

Generated secrets are stored at:

```text
~/.stackforge/<cluster>/generated-secrets.yaml
```

The file is written with mode `0600`.

Remote control-plane env is written to:

```text
/etc/stackforge/stackforge.env
```

The installer verifies root ownership and mode `600` or `640`.

Remote command output redacts known secret values when they are passed to the executor.

## Restore And Rollback Safety

Backup restore is destructive and requires:

```bash
--yes
```

unless `--dry-run` is used.

Rollback apply also requires:

```bash
--yes
```

Rollback refuses records marked unsafe for automatic apply. Firewall and database rollback records are treated as unsafe because they can break access or data.

## Upgrade Safety

Live upgrade is not fully implemented.

Current behavior:

- dry-run prints planned steps
- live mode runs a pre-upgrade backup unless `--skip-backup`
- then it returns an error explaining that live upgrade needs explicit target versions and reachable inventory

This is a safety refusal, not a successful upgrade.

## Uninstall Safety

`stackforge uninstall` requires:

```bash
--confirm-destroy
```

Without it, the command prints the uninstall plan and returns an error.

Current uninstall does not remove remote services or packages. It marks inventory as uninstalled. With `--wipe-data`, it writes a local marker that remote data wipe requires manual review.

## Recommended Production Checklist

Before live install:

- Copy an example config to a new file.
- Replace all documentation IPs and `example.com` domains.
- Set a real cluster name that does not contain `example` or `demo`.
- Use a real admin API key, not only `change-me`.
- Restrict `allowed_admin_cidrs`.
- Restrict `allowed_ssh_cidrs`.
- Confirm private node addresses are actually private.
- Confirm server role counts are odd for Consul and Nomad servers.
- Confirm at least one `database` role and one `control-plane` role.
- Run `stackforge validate --config stackforge.yaml`.
- Run `stackforge validate --config stackforge.yaml --live --production`.
- Run `stackforge firewall plan --config stackforge.yaml`.
- Run `stackforge install --dry-run --config stackforge.yaml`.
- Back up any existing server config manually if these are not fresh nodes.
- Run live install with `--confirm-production`.
