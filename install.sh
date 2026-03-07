#!/usr/bin/env bash
set -euo pipefail

REPO="jimmyken793/bbsit"

# --- helpers ---
die() { echo "error: $*" >&2; exit 1; }

# --- preflight ---
[ "$(id -u)" -eq 0 ] || die "must run as root (try: curl ... | sudo bash)"
command -v curl  >/dev/null || die "curl is required"
command -v dpkg  >/dev/null || die "dpkg is required (Debian/Ubuntu)"

# --- detect architecture ---
case "$(uname -m)" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)       die "unsupported architecture: $(uname -m)" ;;
esac

# --- resolve latest version ---
echo "Fetching latest release..."
VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' | head -1 | sed 's/.*"v\(.*\)".*/\1/')
[ -n "$VERSION" ] || die "could not determine latest version"
echo "Latest version: v${VERSION} (${ARCH})"

# --- download and install ---
DEB="bbsit_${VERSION}_${ARCH}.deb"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${DEB}"
TMP=$(mktemp /tmp/bbsit-XXXXXX.deb)
trap 'rm -f "$TMP"' EXIT

echo "Downloading ${URL}..."
curl -fSL -o "$TMP" "$URL" || die "download failed — check that a release exists for v${VERSION} (${ARCH})"

echo "Installing..."
dpkg -i "$TMP"

echo ""
echo "bbsit installed successfully (v${VERSION})"
echo ""
echo "Next steps:"
echo "  sudo systemctl enable --now bbsit"
echo "  sudo vi /opt/bbsit/config.yaml"
echo "  open http://<host-ip>:9090"
