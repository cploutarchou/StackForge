# StackForge

StackForge is a Go-based infrastructure and domain control plane for Debian 12+ and Ubuntu 22.04/24.04 servers. It installs and operates Nomad, Consul, Traefik, PostgreSQL, and a StackForge control-plane API.

## Architecture

The source of truth is the database. Consul KV and service metadata are runtime/discovery metadata only. The CLI manages install state under `~/.stackforge/{cluster-name}`. The control plane exposes authenticated REST endpoints for domains, verification, DNS, routing, reconciliation, and audit queries.

## Components

- `stackforge` CLI
- StackForge Control Plane API
- StackForge reconciler
- Consul
- Nomad
- Traefik
- PostgreSQL
- Cloudflare DNS API integration

## Install Modes

Single-node mode runs all roles on one host and is not highly available. Three-node mode provides Consul and Nomad quorum. Custom mode allows explicit role assignment.

## Domain Flow

Add a domain, generate a TXT verification token, publish the TXT record, verify ownership, apply DNS, apply routing, then reconcile until desired and actual state match.

## Reconciliation Flow

Desired state comes from PostgreSQL. Actual state comes from Cloudflare, Consul metadata, Nomad service/job state, and Traefik routing configuration. Routing is refused until ownership is verified.
