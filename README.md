# StackForge

StackForge is a Go-based infrastructure and domain control plane for operating Nomad, Consul, Traefik, PostgreSQL, and domain routing infrastructure on Debian/Ubuntu servers.

Build:

```bash
go build -o bin/stackforge ./cmd/stackforge
```

Install the latest Linux release:

```bash
curl -fsSL https://raw.githubusercontent.com/cploutarchou/StackForge/master/scripts/install-stackforge.sh | sh
```

Install a specific release:

```bash
curl -fsSL https://raw.githubusercontent.com/cploutarchou/StackForge/master/scripts/install-stackforge.sh | VERSION=v0.1.1 sh
```

Installer options:

- `INSTALL_DIR`: install directory, defaults to `/usr/local/bin`.
- `VERSION`: release version, defaults to `latest`.
- `STACKFORGE_REPO`: GitHub repository, defaults to `cploutarchou/StackForge`.
- `VERIFY_CHECKSUM=false`: bypass checksum verification only when `sha256sum` is unavailable and the release source has been reviewed.

Dry-run a single-node install:

```bash
bin/stackforge install --dry-run --config examples/stackforge.single-node.yaml
```

Start the control-plane API:

```bash
STACKFORGE_ADMIN_API_KEYS="$(openssl rand -base64 32)" bin/stackforge serve
```

Operator documentation starts in [`docs/STACKFORGE.md`](docs/STACKFORGE.md).
