<p align="center">
  <img src="assets/stackforge-logo.svg" alt="StackForge logo" width="520">
</p>

# StackForge

StackForge is a simple Go control plane for building and managing production-ready infrastructure.

It helps developers and DevOps teams install, validate, and operate services like Nomad, Consul, Traefik, PostgreSQL, and domain routing on Debian or Ubuntu servers.

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
