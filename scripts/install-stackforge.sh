#!/usr/bin/env sh
set -eu

REPO="${STACKFORGE_REPO:-cploutarchou/StackForge}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARY_NAME="${BINARY_NAME:-stackforge}"
VERSION="${VERSION:-latest}"

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

case "$(uname -s)" in
  Linux) os="linux" ;;
  *) fail "only Linux installs are supported by this installer" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

if command -v curl >/dev/null 2>&1; then
  fetch="curl -fsSL"
elif command -v wget >/dev/null 2>&1; then
  fetch="wget -qO-"
else
  fail "curl or wget is required"
fi

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
$fetch "$url" > "$tmp/$asset"
if command -v sha256sum >/dev/null 2>&1; then
  $fetch "$checksum_url" > "$tmp/checksums.txt"
  grep "  ${asset}$" "$tmp/checksums.txt" > "$tmp/checksum-line" || fail "checksum not found for ${asset}"
  (cd "$tmp" && sha256sum -c checksum-line >/dev/null) || fail "checksum verification failed"
fi
tar -xzf "$tmp/$asset" -C "$tmp"
test -x "$tmp/stackforge" || fail "release archive did not contain executable stackforge"

if [ -w "$INSTALL_DIR" ]; then
  install -m 0755 "$tmp/stackforge" "$INSTALL_DIR/$BINARY_NAME"
elif command -v sudo >/dev/null 2>&1; then
  sudo install -m 0755 "$tmp/stackforge" "$INSTALL_DIR/$BINARY_NAME"
else
  fail "$INSTALL_DIR is not writable and sudo is unavailable"
fi

log "Installed $("$INSTALL_DIR/$BINARY_NAME" version 2>/dev/null || printf '%s' "$INSTALL_DIR/$BINARY_NAME")"
log "Run: $BINARY_NAME --help"
