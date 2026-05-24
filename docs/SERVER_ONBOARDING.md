# Server Onboarding

Server onboarding is handled by two commands:

- `stackforge nodes bootstrap`: copy SSH public keys and verify passwordless SSH.
- `stackforge nodes onboard`: run a higher-level onboarding flow that can bootstrap SSH, install Docker, run the main StackForge install, and verify the result.

## What Information You Need

For each server, collect:

- Server name, such as `node-1`.
- Public IP or hostname.
- Private IP if available.
- SSH username.
- SSH port.
- Authentication method: `private-key` or `password`.
- Local public SSH key path, usually `~/.ssh/id_ed25519.pub`.
- Roles:
  - `consul-server`
  - `nomad-server`
  - `nomad-client`
  - `traefik`
  - `database`
  - `control-plane`
  - `docker-host`

For the cluster, collect:

- Cluster name.
- Environment.
- Control-plane domain.
- Admin CIDRs for API/dashboard access.
- SSH CIDRs for SSH access.
- Private and public network interface names if you want validation to check them.

## SSH Bootstrap

`nodes bootstrap` reads your local public key, connects to each server, and appends the public key to:

```text
~/.ssh/authorized_keys
```

The remote command is idempotent. It creates `~/.ssh`, touches `authorized_keys`, appends the key only if it is missing, and sets permissions.

Dry run:

```bash
stackforge nodes bootstrap \
  --dry-run \
  --node node-1=203.0.113.10 \
  --ssh-user root \
  --public-key ~/.ssh/id_ed25519.pub
```

Private-key auth:

```bash
stackforge nodes bootstrap \
  --node node-1=203.0.113.10 \
  --ssh-user root \
  --ssh-port 22 \
  --public-key ~/.ssh/id_ed25519.pub \
  --auth private-key
```

Password auth:

```bash
stackforge nodes bootstrap \
  --node node-1=203.0.113.10 \
  --ssh-user root \
  --auth password
```

Password behavior:

- The CLI prompts securely.
- The password is used only to copy the public key.
- The password is not stored in inventory.
- The password is not written to reports.
- After copy, StackForge verifies key-based SSH.

If password auth cannot use the secure prompt, the command refuses.

## Interactive Bootstrap

If no `--node` flags are passed and stdin is a TTY, `nodes bootstrap` asks:

1. How many servers to bootstrap.
2. Server name.
3. Public IP or hostname.
4. SSH username.
5. SSH port.
6. Auth method.
7. Public key path.

Then it prints a plan and asks for confirmation unless `--dry-run` or `--yes` is used.

## Higher-Level Onboarding

Run onboarding from a config file:

```bash
stackforge nodes onboard --dry-run --config stackforge.yaml
```

Live:

```bash
stackforge nodes onboard \
  --config stackforge.yaml \
  --confirm-production
```

When using `--config`, onboarding:

1. Loads the YAML config.
2. Converts configured nodes into bootstrap targets.
3. Uses `ssh.copy_public_key` to decide whether to copy your SSH key.
4. Installs Docker when enabled by onboarding options.
5. Runs the main install flow.
6. Runs post-onboarding verification.

Without `--config`, onboarding requires a TTY and asks for all cluster and node details.

## Docker Installation Behavior

Docker install is implemented in `internal/stackforge/components`.

The command:

- checks whether `docker` exists and the daemon responds;
- installs Docker apt prerequisites;
- adds Docker’s apt key and apt source;
- installs `docker-ce`, `docker-ce-cli`, `containerd.io`, `docker-buildx-plugin`, and `docker-compose-plugin`;
- enables and starts the Docker service;
- checks `docker info`.

`nodes onboard` runs Docker install before the main StackForge install when Docker install is enabled.

The main `install.Run()` flow does not install Docker as a normal component step.

## Required Base Packages

The main install flow installs missing base packages:

- `curl`
- `wget`
- `unzip`
- `ca-certificates`
- `gnupg`
- `lsb-release`
- `jq`
- `ufw`
- `systemd`
- `openssl`
- `iproute2`
- `tar`
- `gzip`
- `apt-transport-https`

## Supported OS Checks

Install shell checks accept:

- Debian 12.x
- Debian 13.x
- Ubuntu 22.04
- Ubuntu 24.04
- Ubuntu 26.04

Live validation parsing currently accepts:

- Debian 12.x
- Debian 13.x
- Ubuntu 22.04
- Ubuntu 24.04

The separate `osdetect` package accepts Debian 12+ and Ubuntu 22.04/24.04. This inconsistency should be fixed before claiming full Ubuntu 26.04 support.

## Required Ports

The firewall plan includes these ports:

| Port | Purpose | Exposure |
| --- | --- | --- |
| SSH port from config | SSH | `allowed_ssh_cidrs` |
| `80/tcp` | Public HTTP | public |
| `443/tcp` | Public HTTPS | public |
| `8080/tcp` by default | StackForge API | `allowed_admin_cidrs` |
| `8500/tcp` | Consul HTTP/UI | `allowed_admin_cidrs` |
| `4646/tcp` | Nomad HTTP/UI | `allowed_admin_cidrs` |
| `8300/tcp` | Consul RPC | private |
| `8301/tcp/udp` | Consul LAN gossip | private |
| `8302/tcp/udp` | Consul WAN gossip | private |
| `8600/tcp/udp` | Consul DNS | private |
| `4647/tcp` | Nomad RPC | private |
| `4648/tcp/udp` | Nomad Serf | private |
| `5432/tcp` | PostgreSQL | private |

If Traefik dashboard is enabled, port `8080/tcp` is also allowed from admin CIDRs for the dashboard.

## Firewall Requirements

StackForge manages UFW only.

Live validation fails if:

- UFW is missing and `--allow-no-firewall` is not passed.
- Only nftables is detected and firewall bypass is not allowed.
- Required internal/admin services would be exposed publicly.

Apply firewall rules:

```bash
stackforge firewall apply --config stackforge.yaml
```

Review the plan first:

```bash
stackforge firewall plan --config stackforge.yaml
```

## Validation Checklist

Before live onboarding:

- Replace all example IPs and `example.com` domains.
- Use real `allowed_admin_cidrs` and `allowed_ssh_cidrs`.
- Make sure `control_plane.admin_api_keys` is not only `change-me`.
- Confirm Consul and Nomad server role counts are odd.
- Make sure at least one node has `database`.
- Make sure at least one node has `control-plane`.
- Confirm private node addresses are not public addresses.
- Confirm SSH private key path exists.
- Run `stackforge validate --config stackforge.yaml`.
- Run `stackforge validate --config stackforge.yaml --live --production`.
- Run `stackforge install --dry-run --config stackforge.yaml`.

## Inventory Updates

Bootstrap and install write inventory to:

```text
~/.stackforge/<cluster>/inventory.yaml
```

During bootstrap, nodes are marked `ssh-ready`.

During install, successful steps update install status and component/service fields.

After install, inventory refresh attempts to observe live state from each node.
