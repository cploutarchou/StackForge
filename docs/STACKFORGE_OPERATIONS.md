# StackForge Operations

## Backups

Run:

```bash
stackforge backup run
stackforge backup list
```

Backups are stored under `~/.stackforge/{cluster}/backups/{backup-id}` with `backup-manifest.json` and checksums. With `--config`, StackForge uses SSH inventory context to run `pg_dump`, `consul snapshot save`, Nomad job export, and config archive commands on the relevant remote nodes. Without live context or with `--dry-run`, it records planned commands and warnings instead of pretending a live backup happened.

## Restore

Run:

```bash
stackforge backup restore BACKUP_ID --yes
```

Restore validates the manifest and checksums before updating local inventory.
Component restore requires `--yes`. PostgreSQL, Consul, Traefik config, and non-secret StackForge config restore through SSH when a config is provided. Nomad job restore is refused for automatic apply and reported as a partial restore because exported job specs require operator review before `nomad job run`.

## Inventory

Run:

```bash
stackforge inventory refresh --config examples/stackforge.single-node.yaml
stackforge inventory show
```

Inventory refresh harvests observed OS, kernel, IPs, Consul/Nomad/Traefik/PostgreSQL/StackForge status and versions, firewall backend, listening ports, health timestamp, and warnings.

## Rollback

Run:

```bash
stackforge install resume-status
stackforge install report
stackforge rollback list
stackforge rollback apply ROLLBACK_ID --yes --config examples/stackforge.single-node.yaml
```

Risky install steps create timestamped config backups and local rollback records before changes. Firewall and database rollback records refuse automatic apply when console or data-loss review is safer, and include manual recovery instructions.

## Upgrade

Run dry-run first:

```bash
stackforge upgrade --dry-run
```

Live upgrade requires target versions and reachable inventory. A backup is created unless `--skip-backup` is set.

## Monitoring

Monitor control-plane HTTP status, Consul leader, Nomad leader, Traefik route health, database availability, backup age, and reconciliation failures.

## Logs

Installer logs and reports are under `~/.stackforge/{cluster}`. Runtime services should log through systemd journald.

## Production Checklist

- Admin API keys configured.
- Firewall rules restricted.
- Database private only.
- Consul/Nomad ACLs enabled.
- Cloudflare token least-privilege.
- Backups tested.
- Restore tested.
- Inventory refresh reviewed after install/backup/restore.
- Rollback records reviewed after failed install/upgrade.
- TLS enabled.
