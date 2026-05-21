# StackForge Installer

## Prerequisites

Use Debian 12+ or Ubuntu 22.04/24.04 with SSH access, sudo/root privileges, systemd, and outbound package repository access. Unsupported OS versions fail clearly.

## Network Requirements

Open only required ports. SSH is restricted to configured CIDRs. HTTP/HTTPS are public. Consul, Nomad, control-plane admin, and database ports are restricted to admin/private networks.

## SSH Requirements

Provide a private key path and SSH user in the install YAML or CLI flags. StackForge never stores private key contents in inventory.

## Install

Preflight disposable nodes before install:

```bash
bin/stackforge validate --config examples/stackforge.single-node.yaml
bin/stackforge validate --config examples/stackforge.three-node.yaml
```

Validation checks SSH, supported OS, sudo/root, package manager, required ports, private networking, UFW availability, DNS/domain inputs, and firewall exposure safety. StackForge uses UFW for live firewall management on Debian/Ubuntu; nftables-only hosts fail validation unless `--allow-no-firewall` is passed intentionally.

Single-node:

```bash
go build -o bin/stackforge ./cmd/stackforge
bin/stackforge install --dry-run --config examples/stackforge.single-node.yaml
bin/stackforge install --config examples/stackforge.single-node.yaml --yes
```

Three-node:

```bash
bin/stackforge install --dry-run --config examples/stackforge.three-node.yaml
bin/stackforge install --config examples/stackforge.three-node.yaml --yes
```

Resume:

```bash
bin/stackforge install --resume --config examples/stackforge.three-node.yaml
```

## Node Add/Remove

Edit config with the new desired node set, run dry-run, then resume install. Quorum removals are refused by default and must be handled manually until live quorum orchestration is completed.

## Upgrade

`stackforge upgrade` creates a backup unless `--skip-backup` is provided. Live upgrade currently refuses without explicit target versions and reachable inventory.

## Uninstall

`stackforge uninstall` prints the removal plan. Apply with:

```bash
stackforge uninstall --confirm-destroy
```

## Troubleshooting

Inspect `~/.stackforge/{cluster}/install-report.json`, `STACKFORGE_INSTALL_REPORT.md`, remote systemd logs, Consul logs, Nomad logs, Traefik logs, and database logs.

Use `stackforge install resume-status`, `stackforge install report`, `stackforge inventory refresh`, `stackforge inventory show`, and `stackforge rollback list` to inspect the latest operational state and recovery options.
