#!/bin/sh
# Install max-context from GitHub Release. Run: sh install.sh
set -e
VERSION="${MAX_CONTEXT_VERSION:-0.1.0}"
OWNER="maxcontext"
REPO="max-context"
TAG="v${VERSION#v}"
BASE="https://github.com/${OWNER}/${REPO}/releases/download/${TAG}"
OS=$(uname -s)
ARCH=$(uname -m)
case "$OS" in Darwin) PLATFORM="darwin";; Linux) PLATFORM="linux";; *) echo "Unsupported: $OS"; exit 1;; esac
case "$ARCH" in x86_64|amd64) ARCH="amd64";; aarch64|arm64) ARCH="arm64";; *) echo "Unsupported: $ARCH"; exit 1;; esac
ARCHIVE="max-context_${VERSION}_${PLATFORM}_${ARCH}.tar.gz"
TMP=$(mktemp -d)
trap "rm -rf $TMP" EXIT
curl -fsSL -o "$TMP/archive.tar.gz" "$BASE/$ARCHIVE"
curl -fsSL -o "$TMP/checksums.txt" "$BASE/checksums.txt"
(cd "$TMP" && grep " $ARCHIVE" checksums.txt | sha256sum -c -)
tar -xzf "$TMP/archive.tar.gz" -C "$TMP"
BIN="$TMP/max-context"
chmod +x "$BIN"
DIR="${MAX_CONTEXT_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$DIR"
mv "$BIN" "$DIR/max-context"
echo "Installed to $DIR/max-context"

# jq is required by the plugin's hook scripts (grep-interceptor.sh, post-write-reindex.sh).
# If absent, install a pinned static binary alongside max-context — no sudo needed.
if ! command -v jq >/dev/null 2>&1; then
  JQ_VERSION="1.7.1"
  case "$PLATFORM-$ARCH" in
    linux-amd64)  JQ_ASSET="jq-linux-amd64" ;;
    linux-arm64)  JQ_ASSET="jq-linux-arm64" ;;
    darwin-amd64) JQ_ASSET="jq-macos-amd64" ;;
    darwin-arm64) JQ_ASSET="jq-macos-arm64" ;;
    *)            JQ_ASSET="" ;;
  esac
  if [ -n "$JQ_ASSET" ]; then
    echo "jq not found; installing jq ${JQ_VERSION} to $DIR/jq"
    curl -fsSL -o "$DIR/jq" "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/${JQ_ASSET}"
    chmod +x "$DIR/jq"
  else
    echo "warning: jq missing and no prebuilt binary for $PLATFORM-$ARCH — install jq manually before using the plugin hooks"
  fi
fi
