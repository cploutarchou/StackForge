# Operations

This guide covers common operator workflows.

## Build Or Install The CLI

Build from source:

```bash
go build -o bin/stackforge ./cmd/stackforge
```

Install a release:

```bash
curl -fsSL https://raw.githubusercontent.com/cploutarchou/StackForge/master/scripts/install-stackforge.sh | sh
```

## Prepare A Config

Start from an example:

```bash
cp examples/stackforge.single-node.yaml stackforge.yaml
```

Edit:

- node addresses
- public addresses
- cluster name
- domains
- SSH user/key
- admin and SSH CIDRs
- admin API keys
- Cloudflare settings if used

Do not use the example file unchanged for live install.

## Validate Flow

Local validation:

```bash
stackforge validate --config stackforge.yaml
```

Live validation:

```bash
stackforge validate --config stackforge.yaml --live --production
```

Expected output:

- `safe: true` when all checks pass.
- `planned` remote checks when not using `--live`.
- `fail` entries with messages when checks fail.

Common failures:

- example IPs/domains
- production confirmation missing
- public admin CIDR
- public SSH CIDR
- UFW missing
- required ports already listening
- unsupported OS
- no non-interactive sudo

## Install Flow

Dry-run first:

```bash
stackforge install --dry-run --config stackforge.yaml
```

Live install:

```bash
stackforge install --config stackforge.yaml --confirm-production
```

Non-interactive live install:

```bash
stackforge install --config stackforge.yaml --confirm-production --yes
```

If an install fails, inspect:

```text
~/.stackforge/<cluster>/install-report.json
~/.stackforge/<cluster>/inventory.yaml
STACKFORGE_INSTALL_REPORT.md
```

Then resume:

```bash
stackforge install --resume --config stackforge.yaml --confirm-production
```

## Add Server Flow

For SSH key setup only:

```bash
stackforge nodes bootstrap \
  --node node-2=203.0.113.12 \
  --ssh-user root \
  --public-key ~/.ssh/id_ed25519.pub
```

To add config nodes into inventory:

```bash
stackforge nodes add --config stackforge.yaml
```

Then run:

```bash
stackforge install --resume --config stackforge.yaml --confirm-production
```

Important:

`nodes add` updates inventory only. It does not handle live Nomad drain, Consul quorum changes, or remote install by itself.

## Onboard Server Flow

Dry-run from config:

```bash
stackforge nodes onboard --dry-run --config stackforge.yaml
```

Live:

```bash
stackforge nodes onboard --config stackforge.yaml --confirm-production
```

This flow can:

- copy SSH public keys
- install Docker
- run the main component install
- run verification

## Add Domain Through API

Start the API:

```bash
STACKFORGE_ADMIN_API_KEYS=your-admin-key \
DATABASE_URL="postgres://stackforge:password@127.0.0.1:5432/stackforge?sslmode=disable" \
stackforge serve
```

Add a domain:

```bash
STACKFORGE_API_URL=http://127.0.0.1:8080 \
STACKFORGE_ADMIN_API_KEY=your-admin-key \
stackforge domains add app.example.com \
  --tenant tenant-1 \
  --service app \
  --port 8080
```

Generate the verification token through the API directly. The CLI does not currently expose `verification-token` as a subcommand.

Verify:

```bash
STACKFORGE_ADMIN_API_KEY=your-admin-key \
STACKFORGE_DOMAIN_VERIFICATION_TOKEN=token \
stackforge domains verify domain-id
```

Apply DNS or routing:

```bash
STACKFORGE_ADMIN_API_KEY=your-admin-key \
stackforge domains apply-dns domain-id
```

Current limitation:

The API apply endpoints return accepted after ownership verification, but do not apply real Cloudflare/Consul/Nomad/Traefik changes in the current server wiring.

## Add Domain Through Local Domain Pool

Add:

```bash
stackforge domains pool add app.example.com \
  --target traefik \
  --target-value 203.0.113.10 \
  --record-type A \
  --zone-id zone-id
```

Apply DNS:

```bash
CLOUDFLARE_API_TOKEN=token \
stackforge domains pool apply-dns app.example.com --yes
```

Verify:

```bash
stackforge domains pool verify-dns app.example.com
```

List:

```bash
stackforge domains pool list --cluster stackforge-production
```

Remove from pool:

```bash
stackforge domains pool remove app.example.com --yes
```

Remove marks the entry disabled. It does not delete the Cloudflare record.

## Check Health

Cluster status:

```bash
stackforge status --config stackforge.yaml
```

Inventory:

```bash
stackforge inventory refresh --config stackforge.yaml
stackforge inventory show --cluster stackforge-production
```

Live verification:

```bash
stackforge verify --config stackforge.yaml
```

Control-plane health:

```bash
curl http://127.0.0.1:8080/health
```

## Backups

Dry-run backup:

```bash
stackforge backup run --cluster stackforge-production --dry-run
```

Live backup with config:

```bash
stackforge backup run --config stackforge.yaml
```

List backups:

```bash
stackforge backup list --cluster stackforge-production
```

Backups are stored under:

```text
~/.stackforge/<cluster>/backups/<backup-id>/
```

## Restore

Dry-run restore:

```bash
stackforge backup restore <backup-id> --dry-run --cluster stackforge-production
```

Live restore:

```bash
stackforge backup restore <backup-id> --yes --config stackforge.yaml
```

Restore verifies backup checksums first.

Nomad restore currently refuses automatic job run and requires operator review.

## Rollback

List rollback records:

```bash
stackforge rollback list --cluster stackforge-production
```

Apply safe rollback:

```bash
stackforge rollback apply <rollback-id> --yes --config stackforge.yaml
```

Unsafe rollback records, such as firewall and database, are refused for automatic apply.

## Troubleshooting

### SSH Fails

Check:

- node address
- SSH port
- SSH username
- private key path
- firewall rules
- server allows key auth

Run:

```bash
ssh -i ~/.ssh/id_ed25519 root@server
```

### Validation Fails On Example Values

Replace documentation IPs and `example.com` domains. Do not bypass this for production.

### Firewall Blocks Access

Use provider console access if SSH is blocked.

Check UFW:

```bash
ufw status verbose
ls -l /var/lib/stackforge/firewall
```

### Consul Or Nomad Has No Leader

Check services:

```bash
systemctl status consul
journalctl -u consul
systemctl status nomad
journalctl -u nomad
```

Check endpoints:

```bash
curl http://127.0.0.1:8500/v1/status/leader
curl http://127.0.0.1:4646/v1/status/leader
```

### Control Plane Does Not Start

Check:

```bash
systemctl status stackforge-control-plane
journalctl -u stackforge-control-plane
stat -c '%U:%G %a' /etc/stackforge/stackforge.env
```

In production, `DATABASE_URL` must be set.

### PostgreSQL Fails

Check:

```bash
systemctl status postgresql
journalctl -u postgresql
sudo -u postgres psql -d stackforge -c 'select 1'
```

### DNS Apply Fails

Check:

- `CLOUDFLARE_API_TOKEN`
- zone id
- root domain in Cloudflare
- record type and target value

Run:

```bash
stackforge domains pool list
stackforge domains pool verify-dns app.example.com
```

## Logs And Diagnostics

Local state:

```text
~/.stackforge/<cluster>/
```

Important files:

- `inventory.yaml`
- `install-report.json`
- `bootstrap-report.json`
- `generated-secrets.yaml`
- `domain-pool.yaml`
- `domain-pool-audit.jsonl`
- `backups/<id>/backup-manifest.json`
- `rollback/<id>.json`

Remote systemd logs:

```bash
journalctl -u consul
journalctl -u nomad
journalctl -u traefik
journalctl -u postgresql
journalctl -u stackforge-control-plane
```

Component status:

```bash
stackforge components status --config stackforge.yaml
```
