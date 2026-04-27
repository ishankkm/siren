#!/usr/bin/env bash
#
# install.sh — download and install the latest siren release tarball.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/ishankkm/siren/main/install.sh | sudo bash
#
# Or, pinning a version / overriding paths:
#   curl -fsSL https://raw.githubusercontent.com/ishankkm/siren/main/install.sh \
#     | sudo VERSION=v0.2.0 PREFIX=/opt/siren/bin bash
#
# What this script does (and only this):
#   1. Detects host arch (amd64 or arm64).
#   2. Resolves VERSION (env override, else GitHub "latest" redirect).
#   3. Downloads the matching tarball and checksums.txt from GitHub Releases.
#   4. Verifies sha256 against checksums.txt.
#   5. Extracts and installs the binary to ${PREFIX}/siren (default
#      /usr/local/bin/siren), preserving any existing copy as siren.prev.
#
# What it deliberately does NOT do:
#   - Create users, write config, install the systemd unit, or start
#     anything. Those are operator decisions and live in docs/DEPLOYMENT.md.
#   - Auto-update over time. Run again to upgrade.
#   - Talk to anything other than github.com.

set -euo pipefail

REPO="${REPO:-ishankkm/siren}"
PREFIX="${PREFIX:-/usr/local/bin}"
VERSION="${VERSION:-}"

err() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }
log() { printf 'install.sh: %s\n' "$*"; }

require() {
  command -v "$1" >/dev/null 2>&1 || err "required tool not found: $1"
}

require curl
require tar
require sha256sum
require install
require uname
require mktemp

case "$(uname -s)" in
  Linux) ;;
  *) err "siren is Linux-only; detected $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) err "unsupported arch: $(uname -m)" ;;
esac

if [ -z "$VERSION" ]; then
  # The /releases/latest URL redirects to /releases/tag/<vX.Y.Z>. We follow
  # the redirect with -I and parse the final URL — no JSON parser required,
  # so this works on a bare host without jq.
  log "resolving latest version from github.com/${REPO}/releases/latest"
  resolved=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/${REPO}/releases/latest") \
    || err "failed to resolve latest release"
  VERSION="${resolved##*/}"
  case "$VERSION" in
    v*) ;;
    *) err "could not parse version from URL: $resolved" ;;
  esac
fi
log "installing siren ${VERSION} (linux/${ARCH}) to ${PREFIX}/siren"

# Strip the leading 'v' for the tarball filename, which GoReleaser names
# without it (siren_0.1.0_linux_amd64.tar.gz).
ver_no_v="${VERSION#v}"
tarball="siren_${ver_no_v}_linux_${ARCH}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${VERSION}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

log "downloading ${tarball}"
curl -fsSL -o "${tmp}/${tarball}"     "${base_url}/${tarball}"     || err "download failed"
curl -fsSL -o "${tmp}/checksums.txt" "${base_url}/checksums.txt"   || err "checksums download failed"

log "verifying sha256"
( cd "$tmp" && sha256sum --check --ignore-missing checksums.txt ) \
  || err "checksum verification failed"

log "extracting"
tar -xzf "${tmp}/${tarball}" -C "$tmp" siren \
  || err "extract failed (binary 'siren' not in tarball?)"

dest="${PREFIX}/siren"
if [ -e "$dest" ]; then
  log "preserving previous binary at ${dest}.prev"
  install -m 0755 "$dest" "${dest}.prev"
fi
install -m 0755 "${tmp}/siren" "$dest" \
  || err "install to ${dest} failed (need sudo?)"

log "installed: $("$dest" -version 2>&1 || echo "${dest}")"
log "next steps: see https://github.com/${REPO}/blob/main/docs/DEPLOYMENT.md#3-install-on-the-host"
