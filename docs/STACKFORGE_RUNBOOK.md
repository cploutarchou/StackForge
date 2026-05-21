# StackForge Runbook

## Domains

Add a domain through `POST /api/v1/domains` or the `stackforge domains` command once API client wiring is configured. Generate a verification token, create the TXT record, verify ownership, then apply DNS and routing.

## Reconcile Failures

Check the domain `last_error`, Cloudflare token scope, Consul token, Nomad token, and Traefik dynamic configuration. Re-run reconcile after fixing the external system.

## Rollback Procedures

List recorded rollback actions first:

```bash
stackforge rollback list
stackforge rollback apply ROLLBACK_ID --yes --config examples/stackforge.single-node.yaml
```

- Failed Consul install: stop Consul, inspect config and ACL state, restore Consul snapshot if this was an upgrade, then run `stackforge install --resume`.
- Failed Nomad install: stop Nomad, restore previous jobs if needed, inspect server/client logs, then resume.
- Failed Traefik install: disable dashboard exposure, restore previous dynamic config, then resume.
- Failed database migration: restore the latest database backup before retrying migration.
- Failed control-plane deployment: disable `stackforge-control-plane.service`, restore the previous binary/env file, then resume.
- Failed domain DNS apply: delete or revert the Cloudflare record and run reconcile.
- Failed routing apply: remove the generated Consul/Traefik metadata and run reconcile.
- Failed upgrade: restore the pre-upgrade backup and roll components back one at a time.
- Failed node add/remove: restore inventory, check quorum, rejoin or drain nodes manually before retrying.

## Health Checks

Use `stackforge validate --config ...`, `stackforge inventory refresh --config ...`, `stackforge status`, Consul `/v1/status/leader`, Nomad `/v1/status/leader`, Traefik listener checks, Cloudflare API token verification, and database connectivity checks.

## Restore Backup

Validate the manifest and checksums, then:

```bash
stackforge backup restore BACKUP_ID --yes
```
