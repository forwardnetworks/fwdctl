#!/bin/sh
# Install fwdctl from a GitHub release: picks the binary for this machine and checks its checksum.
#
#   curl -fsSL https://raw.githubusercontent.com/forwardnetworks/fwdctl/main/install.sh | sh
#
# With the gh CLI installed and logged in the script uses it; otherwise it downloads over plain HTTPS.
# Supported: linux/amd64 and macOS (Apple silicon darwin/arm64, Intel darwin/amd64). Windows: scripts/install.ps1.
# FORWARD_SKILLS_VERSION=v0.1.2   pin a release (default: the latest)
# FORWARD_SKILLS_BIN=DIR          install directory (default: ~/.local/bin)
set -eu

GH_REPO="${FWDCTL_GH_REPO:-${FORWARD_SKILLS_GH_REPO:-forwardnetworks/fwdctl}}"
DIR="${FORWARD_SKILLS_BIN:-$HOME/.local/bin}"
VERSION="${FORWARD_SKILLS_VERSION:-latest}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os/$arch" in
  linux/x86_64|linux/amd64) target=linux_amd64 ;;
  darwin/arm64|darwin/aarch64) target=darwin_arm64 ;;
  darwin/x86_64|darwin/amd64) target=darwin_amd64 ;;
  *) echo "fwdctl has no build for $os/$arch (linux/amd64, darwin/arm64, darwin/amd64 and, with install.ps1, windows/amd64)" >&2; exit 1 ;;
esac

if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then USE_GH=1; else USE_GH=0; fi
if [ "$USE_GH" = 0 ] && ! command -v curl >/dev/null 2>&1; then echo "need curl (or the gh CLI, logged in) to download fwdctl" >&2; exit 1; fi

if [ "$VERSION" = latest ]; then
  if [ "$USE_GH" = 1 ]; then VERSION="$(gh release view --repo "$GH_REPO" --json tagName -q .tagName)"
  else VERSION="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$GH_REPO/releases/latest" | sed 's|.*/||')"; fi
  case "$VERSION" in v[0-9]*) ;; *) echo "could not find the latest release of $GH_REPO" >&2; exit 1 ;; esac
fi

asset="fwdctl_${VERSION}_${target}.tar.gz"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
echo "installing fwdctl $VERSION ($target)"
if [ "$USE_GH" = 1 ]; then
  gh release download "$VERSION" --repo "$GH_REPO" --dir "$tmp" --pattern "$asset" --pattern SHA256SUMS
else
  base="https://github.com/$GH_REPO/releases/download/$VERSION"
  curl -fsSL -o "$tmp/$asset" "$base/$asset" && curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" ||
    { echo "download failed" >&2; exit 1; }
fi

want="$(grep " $asset\$" "$tmp/SHA256SUMS" | awk '{print $1}')"
[ -n "$want" ] || { echo "$asset is not listed in SHA256SUMS" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$tmp/$asset" | awk '{print $1}')"; else got="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"; fi
[ "$got" = "$want" ] || { echo "checksum mismatch for $asset: got $got, want $want" >&2; exit 1; }

tar -C "$tmp" -xzf "$tmp/$asset" fwdctl
mkdir -p "$DIR"
mv "$tmp/fwdctl" "$DIR/fwdctl"
chmod 755 "$DIR/fwdctl"
[ "$os" = darwin ] && xattr -d com.apple.quarantine "$DIR/fwdctl" 2>/dev/null || true
"$DIR/fwdctl" version
case ":$PATH:" in *":$DIR:"*) ;; *) echo "note: $DIR is not on your PATH" ;; esac
