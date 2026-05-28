#!/usr/bin/env bash
set -euo pipefail

STACKFORGE_BIN="${STACKFORGE_BIN:-$HOME/.local/bin/stackforge}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_ed25519}"
SSH_USER="${SSH_USER:-root}"
SSH_PORT="${SSH_PORT:-22}"
CANARY_HOST="${CANARY_HOST:-}"
CANARY_PREFIX="${CANARY_PREFIX:-/__canary}"
CANARY_TIMEOUT_SECONDS="${CANARY_TIMEOUT_SECONDS:-180}"

usage() {
  cat <<'USAGE'
Run a real StackForge canary deployment end-to-end:
  - Deploy tiny Nomad app (exec driver)
  - Expose route via Traefik file provider
  - Probe service internally and externally
  - Always cleanup job + route

Usage:
  scripts/run-canary-e2e.sh [--host <public-ip-or-hostname>] [--ssh-user root] [--ssh-key ~/.ssh/id_ed25519] [--ssh-port 22]

Environment overrides:
  STACKFORGE_BIN, SSH_KEY, SSH_USER, SSH_PORT, CANARY_HOST, CANARY_PREFIX, CANARY_TIMEOUT_SECONDS
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)
      CANARY_HOST="${2:-}"
      shift 2
      ;;
    --ssh-user)
      SSH_USER="${2:-}"
      shift 2
      ;;
    --ssh-key)
      SSH_KEY="${2:-}"
      shift 2
      ;;
    --ssh-port)
      SSH_PORT="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1" >&2
    exit 2
  }
}

need ssh
need curl
need python3

if [[ ! -x "$STACKFORGE_BIN" ]]; then
  echo "StackForge binary not found/executable at: $STACKFORGE_BIN" >&2
  exit 2
fi

if [[ ! -f "${SSH_KEY/#\~/$HOME}" ]]; then
  echo "SSH private key not found: $SSH_KEY" >&2
  exit 2
fi

if [[ -z "$CANARY_HOST" ]]; then
  STATUS_JSON="$($STACKFORGE_BIN status --output json)"
  CANARY_HOST="$(python3 - <<'PY' "$STATUS_JSON"
import json,sys
s=json.loads(sys.argv[1])
for n in s.get('nodes',[]):
    if 'control-plane' in (n.get('roles') or []):
        print((n.get('public_ip') or '').strip())
        break
PY
)"
fi

if [[ -z "$CANARY_HOST" ]]; then
  echo "Could not determine canary host. Pass --host explicitly." >&2
  exit 2
fi

STAMP="$(date -u +%Y%m%d%H%M%S)-$RANDOM"
CANARY_JOB="stackforge-canary-$STAMP"
CANARY_ROUTE="/etc/traefik/dynamic/${CANARY_JOB}.yaml"
CANARY_PATH="${CANARY_PREFIX%/}/$STAMP"

SSH_OPTS=(-i "${SSH_KEY/#\~/$HOME}" -o BatchMode=yes -o ConnectTimeout=20 -p "$SSH_PORT")

cleanup() {
  set +e
  ssh "${SSH_OPTS[@]}" "$SSH_USER@$CANARY_HOST" "nomad job stop -purge $CANARY_JOB >/dev/null 2>&1 || true; rm -f '$CANARY_ROUTE'"
}
trap cleanup EXIT

echo "[canary] host=$CANARY_HOST job=$CANARY_JOB path=$CANARY_PATH"

echo "[canary] deploying app + route"
ssh "${SSH_OPTS[@]}" "$SSH_USER@$CANARY_HOST" "CANARY_JOB='$CANARY_JOB' CANARY_ROUTE='$CANARY_ROUTE' CANARY_PATH='$CANARY_PATH' CANARY_TIMEOUT_SECONDS='$CANARY_TIMEOUT_SECONDS' bash -se" <<'REMOTE'
set -euo pipefail

cat >/tmp/${CANARY_JOB}.nomad.hcl <<HCL
job "${CANARY_JOB}" {
  datacenters = ["dc1"]
  type        = "service"

  group "canary" {
    count = 1

    network {
      port "http" {
        static = 18080
      }
    }

    task "echo" {
      driver = "exec"

      config {
        command = "python3"
        args = [
          "-c",
          "from http.server import BaseHTTPRequestHandler,HTTPServer\\nclass H(BaseHTTPRequestHandler):\\n  def do_GET(self):\\n    body=b'stackforge-canary-ok'\\n    self.send_response(200)\\n    self.send_header('Content-Type','text/plain')\\n    self.send_header('Content-Length',str(len(body)))\\n    self.end_headers()\\n    self.wfile.write(body)\\n  def log_message(self, *a):\\n    pass\\nHTTPServer(('0.0.0.0',18080),H).serve_forever()"
        ]
      }

      resources {
        cpu    = 100
        memory = 64
      }

      service {
        name = "${CANARY_JOB}"
        port = "http"

        check {
          type     = "http"
          path     = "/"
          interval = "10s"
          timeout  = "2s"
        }
      }
    }
  }
}
HCL

nomad job run /tmp/${CANARY_JOB}.nomad.hcl

timeout ${CANARY_TIMEOUT_SECONDS}s bash -c 'until nomad job status ${CANARY_JOB} | grep -Eq "canary\\s+1\\s+1\\s+1"; do sleep 2; done'

cat >"${CANARY_ROUTE}" <<YAML
http:
  routers:
    ${CANARY_JOB}:
      rule: "PathPrefix(\"${CANARY_PATH}\")"
      entryPoints:
        - web
      service: ${CANARY_JOB}-svc
      priority: 1000

  services:
    ${CANARY_JOB}-svc:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:18080"
YAML

for i in $(seq 1 30); do
  if curl -fsS "http://127.0.0.1${CANARY_PATH}" >/tmp/${CANARY_JOB}.local.out; then break; fi
  sleep 1
done

echo "local_probe=$(cat /tmp/${CANARY_JOB}.local.out)"
REMOTE

echo "[canary] external probe"
for i in $(seq 1 30); do
  if curl -fsS --max-time 10 "http://$CANARY_HOST$CANARY_PATH" >/tmp/${CANARY_JOB}.external.out; then
    break
  fi
  sleep 1
done

EXTERNAL_RESULT="$(cat /tmp/${CANARY_JOB}.external.out)"
echo "external_probe=$EXTERNAL_RESULT"

if [[ "$EXTERNAL_RESULT" != *"stackforge-canary-ok"* ]]; then
  echo "[canary] FAIL: unexpected external probe response" >&2
  exit 1
fi

echo "[canary] PASS"
