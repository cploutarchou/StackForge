# StackForge Staging Test Runbook

Use disposable Debian/Ubuntu servers only. Replace all example IPs, domains, CIDRs, SSH users, and key paths before running live commands.

## Single Node

Build:

```bash
go build -o bin/stackforge ./cmd/stackforge
```

Create real staging config:

```bash
cp examples/stackforge.single-node.yaml examples/stackforge.staging.single-node.yaml
```

Edit:

- Real server IP.
- Real SSH user.
- Real SSH key.
- Real admin CIDR.
- Real SSH CIDR.
- Real test domain, or disable domain routing if not testing DNS.
- Real Cloudflare token only if testing DNS.
- `cluster.name: stackforge-staging`.
- `cluster.environment: staging`.

Validate:

```bash
bin/stackforge validate --config examples/stackforge.staging.single-node.yaml --live
```

Dry-run:

```bash
bin/stackforge install --dry-run --config examples/stackforge.staging.single-node.yaml
```

Live install:

```bash
bin/stackforge install --config examples/stackforge.staging.single-node.yaml --yes
```

Verify:

```bash
bin/stackforge verify --cluster stackforge-staging
```

Backup:

```bash
bin/stackforge backup run --cluster stackforge-staging
```

Restore dry-run:

```bash
bin/stackforge backup restore <backup-id> --cluster stackforge-staging --dry-run
```

Uninstall dry-run:

```bash
bin/stackforge uninstall --cluster stackforge-staging --dry-run
```

Uninstall:

```bash
bin/stackforge uninstall --cluster stackforge-staging --confirm-destroy --preserve-data
```

## Three Nodes

Build:

```bash
go build -o bin/stackforge ./cmd/stackforge
```

Create real staging config:

```bash
cp examples/stackforge.three-node.yaml examples/stackforge.staging.three-node.yaml
```

Edit all three node addresses, SSH values, admin/SSH CIDRs, domains, and Cloudflare settings as above.

Run:

```bash
bin/stackforge validate --config examples/stackforge.staging.three-node.yaml --live
bin/stackforge install --dry-run --config examples/stackforge.staging.three-node.yaml
bin/stackforge install --config examples/stackforge.staging.three-node.yaml --yes
bin/stackforge verify --cluster stackforge-staging
bin/stackforge backup run --cluster stackforge-staging
bin/stackforge backup restore <backup-id> --cluster stackforge-staging --dry-run
bin/stackforge uninstall --cluster stackforge-staging --dry-run
bin/stackforge uninstall --cluster stackforge-staging --confirm-destroy --preserve-data
```

Passing criteria:

- Validation reports `safe: true`.
- Install completes with no failed steps.
- Verify reports `safe: true`.
- Backup creates a manifest.
- Restore dry-run reports no destructive live action.
- Uninstall dry-run prints a preserve-data plan before uninstall is executed.
