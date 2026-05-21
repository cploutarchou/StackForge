#!/usr/bin/env sh
set -eu

REPO="${STACKFORGE_REPO:-cploutarchou/StackForge}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARY_NAME="${BINARY_NAME:-stackforge}"
VERSION="${VERSION:-latest}"
VERIFY_CHECKSUM="${VERIFY_CHECKSUM:-true}"

log() {
  printf '%s\n' "$*" >&2
}

fail() {
  log "stackforge install: $*"
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

need uname
need mktemp
need tar
need grep
need install

case "$(uname -s)" in
  Linux) os="linux" ;;
  *) fail "only Linux installs are supported by this installer" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
  fail "curl or wget is required"
fi

fetch_url() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1"
  else
    wget -qO- "$1"
  fi
}

asset="stackforge_${os}_${arch}.tar.gz"
if [ "$VERSION" = "latest" ]; then
  url="https://github.com/${REPO}/releases/latest/download/${asset}"
  checksum_url="https://github.com/${REPO}/releases/latest/download/checksums.txt"
else
  url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
  checksum_url="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

log "Downloading StackForge ${VERSION} for ${os}/${arch}"
if ! fetch_url "$url" > "$tmp/$asset"; then
  fail "could not download ${asset} from ${url}"
fi
if [ ! -s "$tmp/$asset" ]; then
  fail "downloaded ${asset} is empty"
fi
if [ "$VERIFY_CHECKSUM" = "true" ]; then
  command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required for checksum verification; set VERIFY_CHECKSUM=false to bypass"
  if ! fetch_url "$checksum_url" > "$tmp/checksums.txt"; then
    fail "could not download checksums.txt from ${checksum_url}"
  fi
  grep "  ${asset}$" "$tmp/checksums.txt" > "$tmp/checksum-line" || fail "checksum not found for ${asset}"
  (cd "$tmp" && sha256sum -c checksum-line >/dev/null) || fail "checksum verification failed"
fi
tar -xzf "$tmp/$asset" -C "$tmp"
test -x "$tmp/stackforge" || fail "release archive did not contain executable stackforge"

if [ -d "$INSTALL_DIR" ] && [ -w "$INSTALL_DIR" ]; then
  install -m 0755 "$tmp/stackforge" "$INSTALL_DIR/$BINARY_NAME"
elif command -v sudo >/dev/null 2>&1; then
  sudo install -d -m 0755 "$INSTALL_DIR"
  sudo install -m 0755 "$tmp/stackforge" "$INSTALL_DIR/$BINARY_NAME"
else
  fail "$INSTALL_DIR is not writable and sudo is unavailable"
fi

test -x "$INSTALL_DIR/$BINARY_NAME" || fail "installed binary is not executable: $INSTALL_DIR/$BINARY_NAME"
installed_version="$("$INSTALL_DIR/$BINARY_NAME" version 2>/dev/null || true)"
if [ -z "$installed_version" ]; then
  fail "installed binary did not run successfully"
fi

log "Installed StackForge ${installed_version} at $INSTALL_DIR/$BINARY_NAME"
log "Run: $BINARY_NAME --help"
