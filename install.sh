#!/usr/bin/env bash
# Cremio installer and updater for Linux, macOS, and FreeBSD
#
# One-liner:
#   curl -fsSL https://raw.githubusercontent.com/itssoap/cremio/main/install.sh | bash
#
set -euo pipefail

REPO="itssoap/cremio"
BINARY="cremio"

# --- helpers ---------------------------------------------------------
info()  { printf "   \033[90m%s\033[0m\n" "$1"; }
step()  { printf "\033[36m::\033[0m %s\n" "$1"; }
err()   { printf "\033[31mERROR:\033[0m %s\n" "$1"; exit 1; }

detect_arch() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64)  echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        armv7l|arm)    echo "arm"   ;;
        i386|i686)     echo "386"   ;;
        *) err "Unsupported architecture: $arch" ;;
    esac
}

detect_os() {
    local os
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$os" in
        linux)   echo "linux"   ;;
        darwin)  echo "darwin"  ;;
        freebsd) echo "freebsd" ;;
        *) err "Unsupported OS: $os" ;;
    esac
}

get_latest_release() {
    local api="https://api.github.com/repos/${REPO}/releases/latest"
    local response

    # Try with curl (supports redirects, auth headers from environment)
    if command -v curl >/dev/null 2>&1; then
        response=$(curl -fsSL "$api" 2>/dev/null) || {
            # Fallback: try wget
            if command -v wget >/dev/null 2>&1; then
                response=$(wget -qO- "$api" 2>/dev/null) || err "Could not reach GitHub API. Check your internet connection."
            else
                err "Neither curl nor wget found. Install one of them and try again."
            fi
        }
    elif command -v wget >/dev/null 2>&1; then
        response=$(wget -qO- "$api" 2>/dev/null) || err "Could not reach GitHub API. Check your internet connection."
    else
        err "Neither curl nor wget found. Install one of them and try again."
    fi

    printf '%s\n' "$response"
}

download() {
    local url="$1"
    local dest="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$dest" "$url"
    else
        wget -qO "$dest" "$url"
    fi
}

# --- main ------------------------------------------------------------
printf '\n  \033[32mCremio Unix Installer\033[0m\n'
info "https://github.com/${REPO}"
printf '\n'

# 1. Detect platform
OS=$(detect_os)
ARCH=$(detect_arch)
PLATFORM="${OS}-${ARCH}"
step "Detected platform: ${PLATFORM}"

# 2. Fetch latest release
step "Fetching latest release..."
RELEASE_DATA=$(get_latest_release)

# Extract tag from the JSON response
TAG=$(printf '%s\n' "$RELEASE_DATA" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
if [ -z "$TAG" ]; then
    err "Could not parse release tag from GitHub API response."
fi

# 3. Check if already installed and up-to-date
INSTALL_DIR="${HOME}/.local/bin"
VERSION_FILE="${HOME}/.local/share/cremio/.version"
BIN_PATH="${INSTALL_DIR}/${BINARY}"

installed_tag=""
if [ -f "$VERSION_FILE" ]; then
    installed_tag=$(cat "$VERSION_FILE")
fi

if [ -n "$installed_tag" ] && [ -f "$BIN_PATH" ]; then
    if [ "$installed_tag" = "$TAG" ]; then
        printf '\n  \033[32mCremio %s is already up to date at:\033[0m\n' "$installed_tag"
        info "$BIN_PATH"
        printf '\n'
        exit 0
    fi
    step "New version available: ${TAG} (installed: ${installed_tag})"
else
    step "Installing cremio ${TAG}..."
fi

# 4. Find the matching release asset download URL
ASSET_PATTERN="cremio-${PLATFORM}"

# Search from the asset's "name" line to the next closing brace of that JSON object,
# then extract browser_download_url within that range.
# Tolerates trailing extensions/suffixes (e.g. .tar.gz, -v1.2.3).
ASSET_URL=$(printf '%s\n' "$RELEASE_DATA" | sed -n "/\"name\": *\"${ASSET_PATTERN}[^\"]*\"/,/}/ s/.*\"browser_download_url\": *\"\([^\"]*\)\".*/\1/p" | head -1)

if [ -z "$ASSET_URL" ]; then
    available=$(printf '%s\n' "$RELEASE_DATA" | grep '"name":' | sed -E 's/.*"name": *"([^"]+)".*/\1/' | tr '\n' ' ')
    err "No release asset found matching '${ASSET_PATTERN}'. Available assets: ${available}"
fi

info "Downloading ${ASSET_PATTERN} ..."

# 5. Download and install
mkdir -p "$INSTALL_DIR"
mkdir -p "$(dirname "$VERSION_FILE")"

TMP_DIR="${TMPDIR:-/tmp}"
TMP_FILE="${TMP_DIR}/cremio_$(date +%s)_$$"

download "$ASSET_URL" "$TMP_FILE"
chmod +x "$TMP_FILE"
mv "$TMP_FILE" "$BIN_PATH"

info "Installed to ${BIN_PATH}"

# 6. Record version
printf '%s' "$TAG" > "$VERSION_FILE"

# 7. Add to PATH if needed
step "Checking PATH..."
if ! echo "$PATH" | tr ':' '\n' | grep -qxF "$INSTALL_DIR"; then
    info "Adding ${INSTALL_DIR} to PATH via shell profile"

    # Detect shell profile
    SHELL_PROFILE=""
    case "$(basename "$SHELL")" in
        zsh)  SHELL_PROFILE="${HOME}/.zshrc"  ;;
        bash) SHELL_PROFILE="${HOME}/.bashrc" ;;
        fish) SHELL_PROFILE="${HOME}/.config/fish/config.fish" ;;
        *)    SHELL_PROFILE="${HOME}/.profile" ;;
    esac

    if [ "$(basename "$SHELL")" = "fish" ]; then
        echo "fish_add_path ${INSTALL_DIR}" >> "$SHELL_PROFILE"
    else
        printf '\n# Added by cremio installer\nexport PATH="%s:$PATH"\n' "$INSTALL_DIR" >> "$SHELL_PROFILE"
    fi

    info "Added to ${SHELL_PROFILE}. Restart your shell or run:"
    info "  export PATH=\"${INSTALL_DIR}:\$PATH\""
else
    info "${INSTALL_DIR} is already on PATH."
fi

# 8. Verify
step "Verifying installation..."
if [ -f "$BIN_PATH" ] && [ -s "$BIN_PATH" ]; then
    size=$(du -h "$BIN_PATH" | cut -f1)
    info "cremio is present at ${BIN_PATH} (${size})"
else
    err "Installation verification failed: ${BIN_PATH} not found or empty"
fi

printf '\n  \033[32mCremio %s installed successfully!\033[0m\n' "$TAG"
printf '  \033[36mRun "cremio" in a new terminal to get started.\033[0m\n'
printf '  \033[90mRe-run this script anytime to check for updates.\033[0m\n'
printf '\n'
