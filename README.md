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

Dry-run a single-node install:

```bash
bin/stackforge install --dry-run --config examples/stackforge.single-node.yaml
```

Start the control-plane API:

```bash
STACKFORGE_ADMIN_API_KEYS="$(openssl rand -base64 32)" bin/stackforge serve
```

Operator documentation starts in [`docs/STACKFORGE.md`](docs/STACKFORGE.md).
