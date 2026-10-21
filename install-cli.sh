#!/usr/bin/env bash
# Copyright 2022-2024 LiveKit, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# LiveKit CLI installer for Linux and macOS. Downloads the latest GitHub
# release archive, installs the binary + completions system-wide, then installs
# LiveKit's skills and Docs MCP server into the coding agents on this machine.
#
# Environment:
#   LK_INSTALL_PATH         where to put lk (default /usr/local/bin)
#   LK_INSTALL_SKILLS       1 installs skills even without a terminal; 0 skips them
#   LK_INSTALL_MAX_ATTEMPTS download attempts (default 5)
#   LK_INSTALL_RETRY_DELAY  seconds between attempts (default 2)

set -u
set -o errtrace
set -o errexit
set -o pipefail

REPO="livekit-cli"
BIN_NAME="lk"
INSTALL_PATH="${LK_INSTALL_PATH:-/usr/local/bin}"
INSTALL_PATH="${INSTALL_PATH%/}"

log()   { printf "%b\n" "$*"; }
warn()  { printf "%b\n" "$*" >&2; }
abort() { printf "%s\n" "$@" >&2; exit 1; }


# GitHub is frequently flaky , so every download is retried.
# Overridable for testing / impatient callers.
MAX_ATTEMPTS="${LK_INSTALL_MAX_ATTEMPTS:-5}"
RETRY_DELAY="${LK_INSTALL_RETRY_DELAY:-2}"

[[ "$MAX_ATTEMPTS" =~ ^[0-9]+$ ]] && [ "$MAX_ATTEMPTS" -ge 1 ] || abort "LK_INSTALL_MAX_ATTEMPTS must be a positive integer"
[[ "$RETRY_DELAY" =~ ^[0-9]+$ ]] && [ "$RETRY_DELAY" -ge 1 ] || abort "LK_INSTALL_RETRY_DELAY must be a positive integer (seconds)"

[ -n "${BASH_VERSION:-}" ] || abort "This script requires bash"
command -v curl >/dev/null || abort "cURL is required and is not found"

OS="$(uname)"
case "$OS" in
  Darwin)
    OS_NAME="darwin"
    # /usr/local/bin is on macOS's default PATH (/etc/paths), but a fresh Apple
    # silicon Mac may not have it yet; it's created below.
    BASH_COMPLETION_PATH="/usr/local/etc/bash_completion.d"
    ZSH_COMPLETION_PATH="/usr/local/share/zsh/site-functions"
    FISH_COMPLETION_PATH="/usr/local/share/fish/vendor_completions.d"
    ;;
  Linux)
    OS_NAME="linux"
    BASH_COMPLETION_PATH="/usr/share/bash-completion/completions"
    ZSH_COMPLETION_PATH="/usr/share/zsh/site-functions"
    FISH_COMPLETION_PATH="/usr/share/fish/vendor_completions.d"
    ;;
  *) abort "Installer is only supported on Linux and macOS. On Windows, run: winget install LiveKit.LiveKitCLI" ;;
esac

case "$(uname -m)" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)             abort "Unsupported architecture: $(uname -m)" ;;
esac

# macOS before 15 has shasum but not sha256sum.
if command -v sha256sum >/dev/null; then
  sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null; then
  sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  abort "sha256sum or shasum is required and neither was found"
fi

# Use sudo only for a directory this user can't write to (or create).
SUDO_PREFIX=""
if [ -d "$INSTALL_PATH" ]; then
  [ -w "$INSTALL_PATH" ] || SUDO_PREFIX="sudo"
elif [ "$OS_NAME" = "darwin" ] || [ -n "${LK_INSTALL_PATH:-}" ]; then
  parent="$(dirname "$INSTALL_PATH")"
  while [ ! -d "$parent" ]; do parent="$(dirname "$parent")"; done
  [ -w "$parent" ] || SUDO_PREFIX="sudo"
else
  abort "Could not install, $INSTALL_PATH doesn't exist"
fi
if [ -n "$SUDO_PREFIX" ]; then
  log "sudo is required to install to $INSTALL_PATH"
fi

# The latest release's tag, from where /releases/latest redirects. This needs
# no API token or jq, which macOS before 15 doesn't ship.
LATEST_URL=$(curl -fsSLI -o /dev/null -w '%{url_effective}' --retry "$MAX_ATTEMPTS" --retry-delay "$RETRY_DELAY" \
  "https://github.com/livekit/$REPO/releases/latest")
VERSION="${LATEST_URL##*/v}"

[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || abort "Invalid version: $VERSION"

ARCHIVE_NAME="${BIN_NAME}_${VERSION}_${OS_NAME}_${ARCH}.tar.gz"
ARCHIVE_URL="https://github.com/livekit/$REPO/releases/download/v${VERSION}/${ARCHIVE_NAME}"
CHECKSUMS_URL="https://github.com/livekit/$REPO/releases/download/v${VERSION}/checksums.txt"

log "Installing $REPO $VERSION"
log "Downloading from $ARCHIVE_URL..."

TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

curl -fsSL --retry "$MAX_ATTEMPTS" --retry-delay "$RETRY_DELAY" "$ARCHIVE_URL" -o "$TEMP_DIR/$ARCHIVE_NAME"
curl -fsSL --retry "$MAX_ATTEMPTS" --retry-delay "$RETRY_DELAY" "$CHECKSUMS_URL" -o "$TEMP_DIR/checksums.txt"

# Verify the archive against the release's checksums.txt before extracting. The checksums
# file is fetched from the same release over HTTPS, so this guards against corrupted/partial
# downloads and accidental mismatches; it is not a substitute for signature verification
# against an out-of-band key.
log "Verifying checksum..."
expected_sum=$(awk -v f="$ARCHIVE_NAME" '$2 == f {print $1}' "$TEMP_DIR/checksums.txt")
[ -n "$expected_sum" ] || abort "Could not find a checksum for $ARCHIVE_NAME in checksums.txt"
actual_sum=$(sha256 "$TEMP_DIR/$ARCHIVE_NAME")
[ "$expected_sum" = "$actual_sum" ] || \
  abort "Checksum verification failed for $ARCHIVE_NAME (expected $expected_sum, got $actual_sum)"

tar xzf "$TEMP_DIR/$ARCHIVE_NAME" -C "$TEMP_DIR"

$SUDO_PREFIX mkdir -p "$INSTALL_PATH"
$SUDO_PREFIX mv "$TEMP_DIR/$BIN_NAME" "$INSTALL_PATH/$BIN_NAME"
$SUDO_PREFIX ln -sf "$INSTALL_PATH/$BIN_NAME" "$INSTALL_PATH/livekit-cli"

# Install completions if the corresponding system directories exist. The fish
# completion ships in the archive (no need to invoke lk to regenerate it).
# Skipped for a custom LK_INSTALL_PATH, which is usually a user-level install.
if [ -d "$TEMP_DIR/autocomplete" ] && [ -z "${LK_INSTALL_PATH:-}" ]; then
  [ -d "$BASH_COMPLETION_PATH" ] && \
    $SUDO_PREFIX install -m 0644 "$TEMP_DIR/autocomplete/bash_autocomplete" "$BASH_COMPLETION_PATH/livekit-cli" || true
  [ -d "$ZSH_COMPLETION_PATH" ] && \
    $SUDO_PREFIX install -m 0644 "$TEMP_DIR/autocomplete/zsh_autocomplete" "$ZSH_COMPLETION_PATH/_livekit-cli" || true
  [ -d "$FISH_COMPLETION_PATH" ] && \
    $SUDO_PREFIX install -m 0644 "$TEMP_DIR/autocomplete/fish_autocomplete" "$FISH_COMPLETION_PATH/livekit-cli.fish" || true
fi

log "\n$BIN_NAME is installed to $INSTALL_PATH\n"

# Another lk earlier on PATH (often Homebrew's) would shadow this one.
found="$(command -v "$BIN_NAME" 2>/dev/null || true)"
if [ -z "$found" ]; then
  warn "Note: $INSTALL_PATH isn't on your PATH. Add it to run \`$BIN_NAME\` by name.\n"
elif [ "$found" != "$INSTALL_PATH/$BIN_NAME" ]; then
  warn "Note: $found comes first on your PATH, so \`$BIN_NAME\` runs that copy, not this one."
  warn "Remove it (for Homebrew: brew uninstall livekit-cli) or put $INSTALL_PATH earlier on PATH.\n"
fi

# Install LiveKit's skills and Docs MCP server into the coding agents on this
# machine, so they know how to build LiveKit agents. Runs when someone is
# watching (stdout is a terminal, since stdin is the script under curl | bash)
# or LK_INSTALL_SKILLS=1; never in CI, as root, or with LK_INSTALL_SKILLS=0.
# A failure here doesn't fail the install.
install_skills="${LK_INSTALL_SKILLS:-}"
if [ -z "$install_skills" ]; then
  if [ -t 1 ] && [ -z "${CI:-}" ]; then install_skills=1; else install_skills=0; fi
fi
if [ "$install_skills" = "1" ]; then
  if [ "$(id -u)" = "0" ]; then
    log "Skipping LiveKit skills when installing as root; as your own user, run: lk skills install --global"
  else
    log "Installing LiveKit skills for your coding agents (LK_INSTALL_SKILLS=0 skips this)..."
    if ! "$INSTALL_PATH/$BIN_NAME" skills install --global --yes; then
      warn "Couldn't install LiveKit skills. Run \`lk skills install --global\` to try again."
    fi
  fi
fi
