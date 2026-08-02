#!/usr/bin/env bash
# Cremio installer and updater for Linux, macOS, and FreeBSD
#
# Install / update (latest stable):
#   curl -fsSL https://raw.githubusercontent.com/itssoap/cremio/main/install.sh | bash
#
# Check for an update without installing:
#   curl -fsSL https://raw.githubusercontent.com/itssoap/cremio/main/install.sh | bash -s -- --check
#
# Install / update to the newest pre-release:
#   curl -fsSL https://raw.githubusercontent.com/itssoap/cremio/main/install.sh | bash -s -- --pre-release
#
set -euo pipefail

REPO="itssoap/cremio"
BINARY="cremio"

# --- flags -----------------------------------------------------------
PRE_RELEASE=false
CHECK_ONLY=false
NO_PATH=false
UNINSTALL=false
PURGE_DATA=false

while [ $# -gt 0 ]; do
    case "$1" in
        --pre-release|--prerelease|--pre) PRE_RELEASE=true ;;
        --check|--check-only|--dry-run)   CHECK_ONLY=true ;;
        --no-path)                        NO_PATH=true ;;
        --uninstall|--remove)             UNINSTALL=true ;;
        --purge-data|--purge)             PURGE_DATA=true ;;
        -h|--help)
            cat <<'EOF'
Cremio installer / updater

Usage: install.sh [options]

Options:
  --pre-release   Install or update to the newest pre-release build
  --check         Only check whether a new version is available (no changes)
  --no-path       Do not modify shell profile / PATH
  --uninstall     Remove cremio: binary, .version, and the PATH entry (prompts
                  before deleting config/data)
  --purge-data    With --uninstall, also delete config/data (history, session)
                  without prompting
  -h, --help      Show this help

Targets for replacement are detected in this order:
  1. ./cremio in the current working directory
  2. cremio found on your PATH
  3. Fresh install to ~/.local/bin

Update detection compares the SHA-256 of your installed binary against the
SHA-256 published for the release asset. If GitHub does not expose a digest,
it falls back to comparing version tags.
EOF
            exit 0
            ;;
        *) printf '\033[31mERROR:\033[0m Unknown option: %s (try --help)\n' "$1"; exit 1 ;;
    esac
    shift
done

# --- helpers ---------------------------------------------------------
info()  { printf "   \033[90m%s\033[0m\n" "$1"; }
step()  { printf "\033[36m::\033[0m %s\n" "$1"; }
warn()  { printf "\033[33m!!\033[0m %s\n" "$1"; }
err()   { printf "\033[31mERROR:\033[0m %s\n" "$1"; exit 1; }

detect_arch() {
    local arch
    arch=$(uname -m)
    # On Apple Silicon Macs, a Rosetta 2-translated process (an Intel-built
    # Terminal/shell, or curl/bash launched under "Open using Rosetta") reports
    # "x86_64" from `uname -m` even though the hardware is arm64. Detect that
    # and correct it -- the same class of bug as Windows-on-ARM (#18), just on
    # a different OS. sysctl.proc_translated is 1 only while this specific
    # process is translated; hw.optional.arm64 is 1 whenever the physical CPU
    # is Apple Silicon, translated or not. Both sysctls are absent on Intel
    # Macs, where the comparisons simply fail and arch stays "x86_64".
    if [ "$arch" = "x86_64" ] && [ "$(uname -s)" = "Darwin" ]; then
        if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null)" = "1" ] || \
           [ "$(sysctl -n hw.optional.arm64 2>/dev/null)" = "1" ]; then
            arch="arm64"
        fi
    fi
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

# sha256_file <path> -> lowercase hex digest, or empty if no tool available
sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        echo ""
    fi
}

http_get() {
    # http_get <url> -> body on stdout, honouring rate-limit errors
    local url="$1" response http_code
    if command -v curl >/dev/null 2>&1; then
        local tmp; tmp=$(mktemp)
        http_code=$(curl -sSL -o "$tmp" -w "%{http_code}" "$url" 2>/dev/null) || http_code="000"
        response=$(cat "$tmp"); rm -f "$tmp"
    elif command -v wget >/dev/null 2>&1; then
        response=$(wget -qO- "$url" 2>/dev/null) || http_code="000"
        http_code="200"
    else
        err "Neither curl nor wget found. Install one of them and try again."
    fi

    case "$http_code" in
        403|429) err "GitHub API rate limit exceeded. Wait a few minutes and try again OR fetch it from https://github.com/${REPO}/releases." ;;
        200)     ;;
        000)     err "Could not reach GitHub API. Check your internet connection." ;;
        *)       err "GitHub API returned HTTP ${http_code}. Check your connection and try again." ;;
    esac

    if printf '%s' "$response" | grep -qi '"rate limit'; then
        err "GitHub API rate limit exceeded. Wait a few minutes and try again OR fetch it from https://github.com/${REPO}/releases."
    fi
    [ -n "$response" ] || err "Empty response from GitHub API."
    printf '%s\n' "$response"
}

get_release_data() {
    # Prints the JSON for the release we should install.
    if $PRE_RELEASE; then
        # Newest release overall (includes pre-releases): first element of /releases
        local list_file newest_tag
        list_file="${TMPDIR:-/tmp}/cremio_list_$$.json"
        http_get "https://api.github.com/repos/${REPO}/releases" > "$list_file"
        newest_tag=$(grep -m1 '"tag_name"' "$list_file" | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
        rm -f "$list_file"
        [ -n "$newest_tag" ] || err "No releases found for ${REPO}."
        http_get "https://api.github.com/repos/${REPO}/releases/tags/${newest_tag}"
    else
        http_get "https://api.github.com/repos/${REPO}/releases/latest"
    fi
}

download() {
    local url="$1" dest="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$dest" "$url"
    else
        wget -qO "$dest" "$url"
    fi
}

# strip_path_line <profile> <dir>: remove the PATH lines this installer added.
strip_path_line() {
    local profile="$1" dir="$2" tmp
    [ -f "$profile" ] || return 0
    tmp=$(mktemp)
    grep -vF "# Added by cremio installer" "$profile" \
        | grep -vF "export PATH=\"${dir}:\$PATH\"" \
        | grep -vF "fish_add_path ${dir}" > "$tmp"
    if ! cmp -s "$tmp" "$profile"; then
        cp "$profile" "${profile}.cremio.bak"
        mv "$tmp" "$profile"
        info "Removed PATH entry from ${profile} (backup: ${profile}.cremio.bak)"
    else
        rm -f "$tmp"
    fi
}

do_uninstall() {
    printf '\n  \033[32mCremio Uninstaller\033[0m\n'
    info "https://github.com/${REPO}"
    printf '\n'

    local install_dir="${HOME}/.local/bin"
    local version_file="${HOME}/.local/share/cremio/.version"

    # Locate the binary: PATH first, then default. Never a CWD local build.
    local bin_path=""
    if command -v "$BINARY" >/dev/null 2>&1; then
        bin_path=$(command -v "$BINARY")
    fi
    local cwd_bin="$(pwd)/${BINARY}"
    if [ -n "$bin_path" ] && [ "$bin_path" = "$cwd_bin" ]; then
        warn "Ignoring ${BINARY} in the current directory (looks like a local build)."
        bin_path=""
    fi
    if [ -z "$bin_path" ] && [ -f "${install_dir}/${BINARY}" ]; then
        bin_path="${install_dir}/${BINARY}"
    fi

    if [ -n "$bin_path" ] && [ -f "$bin_path" ]; then
        if rm -f "$bin_path" 2>/dev/null; then
            info "Removed ${bin_path}"
        else
            warn "Could not remove ${bin_path} (permission denied?)"
        fi
    else
        info "No installed ${BINARY} found (nothing to remove)."
    fi

    local bin_dir
    bin_dir=$(dirname "${bin_path:-${install_dir}/${BINARY}}")

    if [ -f "$version_file" ]; then
        rm -f "$version_file"
        info "Removed ${version_file}"
    fi
    local share_dir
    share_dir=$(dirname "$version_file")
    [ -d "$share_dir" ] && rmdir "$share_dir" 2>/dev/null && info "Removed ${share_dir}" || true

    step "Cleaning PATH..."
    for profile in "${HOME}/.zshrc" "${HOME}/.bashrc" "${HOME}/.profile" "${HOME}/.config/fish/config.fish"; do
        strip_path_line "$profile" "$bin_dir"
    done

    # Config/data (separate from the binary): keep by default.
    local data_dir="${HOME}/.config/cremio"
    if [ -d "$data_dir" ]; then
        local purge=$PURGE_DATA
        if ! $purge; then
            printf '\n'
            warn "Config and data (watch history, saved account session) live in:"
            info "  ${data_dir}"
            printf '   Remove this too? History and login will be lost [y/N] '
            local answer=""
            read -r answer </dev/tty 2>/dev/null || answer=""
            case "$answer" in y|Y|yes|YES) purge=true ;; *) purge=false ;; esac
        fi
        if $purge; then
            rm -rf "$data_dir"
            info "Removed ${data_dir}"
        else
            info "Kept ${data_dir}"
        fi
    fi

    printf '\n  \033[32mCremio uninstalled.\033[0m\n'
    printf '  \033[90mOpen a new terminal for the PATH change to take effect.\033[0m\n\n'
}

# --- main ------------------------------------------------------------
if $UNINSTALL; then
    do_uninstall
    exit 0
fi

printf '\n  \033[32mCremio Unix Installer\033[0m\n'
info "https://github.com/${REPO}"
$PRE_RELEASE && info "channel: pre-release"
$CHECK_ONLY  && info "mode: check only (no changes will be made)"
printf '\n'

# 1. Detect platform
OS=$(detect_os)
ARCH=$(detect_arch)
PLATFORM="${OS}-${ARCH}"
step "Detected platform: ${PLATFORM}"

# 2. Fetch release info
step "Fetching release info..."
RELEASE_JSON="${TMPDIR:-/tmp}/cremio_release_$$.json"
trap 'rm -f "$RELEASE_JSON"' EXIT
get_release_data > "$RELEASE_JSON"
TAG=$(grep -m1 '"tag_name"' "$RELEASE_JSON" | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
[ -n "$TAG" ] || err "Could not parse release tag from GitHub API response."
info "Release: ${TAG}"

# 3. Locate the matching asset (download URL + published digest)
ASSET_PATTERN="cremio-${PLATFORM}"
ASSET_BLOCK_FILE="${TMPDIR:-/tmp}/cremio_asset_$$.txt"
sed -n "/\"name\": *\"${ASSET_PATTERN}[^\"]*\"/,/browser_download_url/p" "$RELEASE_JSON" > "$ASSET_BLOCK_FILE"
ASSET_URL=$(sed -n 's/.*"browser_download_url": *"\([^"]*\)".*/\1/p' "$ASSET_BLOCK_FILE" | sed -n '1p')
ASSET_DIGEST=$(sed -n 's/.*"digest": *"sha256:\([0-9a-fA-F]*\)".*/\1/p' "$ASSET_BLOCK_FILE" | sed -n '1p')
rm -f "$ASSET_BLOCK_FILE"

if [ -z "$ASSET_URL" ]; then
    available=$(grep '"name":' "$RELEASE_JSON" | sed -E 's/.*"name": *"([^"]+)".*/\1/' | tr '\n' ' ')
    err "No release asset found matching '${ASSET_PATTERN}'. Available assets: ${available}"
fi

# 4. Detect existing installation. Replacement targets, in priority order:
#    (a) ./cremio in CWD   (b) cremio on PATH
INSTALL_DIR="${HOME}/.local/bin"
VERSION_FILE="${HOME}/.local/share/cremio/.version"

target_bin=""
cwd_bin="$(pwd)/${BINARY}"
if [ -f "$cwd_bin" ]; then
    target_bin="$cwd_bin"
    info "Found local binary in CWD: ${target_bin}"
elif command -v "$BINARY" >/dev/null 2>&1; then
    target_bin=$(command -v "$BINARY")
    info "Found binary on PATH: ${target_bin}"
fi

# 5. Decide whether an update is needed (SHA-256 first, tag fallback)
up_to_date=false
if [ -n "$target_bin" ]; then
    if [ -n "$ASSET_DIGEST" ]; then
        local_sha=$(sha256_file "$target_bin")
        if [ -z "$local_sha" ]; then
            warn "No sha256 tool (sha256sum/shasum) found; falling back to version tag."
        else
            info "Installed SHA-256: ${local_sha}"
            info "Release   SHA-256: ${ASSET_DIGEST}"
            if [ "$local_sha" = "$ASSET_DIGEST" ]; then
                up_to_date=true
            fi
        fi
    fi

    # Tag fallback when no digest or no hashing tool
    if [ -z "$ASSET_DIGEST" ] || [ -z "${local_sha:-}" ]; then
        installed_tag=""
        [ -f "$VERSION_FILE" ] && installed_tag=$(cat "$VERSION_FILE")
        norm_tag=$(printf '%s' "$TAG" | sed 's/^v//')
        norm_existing=$(printf '%s' "$installed_tag" | sed 's/^v//')
        if [ -n "$norm_existing" ] && [ "$norm_existing" = "$norm_tag" ]; then
            up_to_date=true
        fi
    fi
fi

if $up_to_date; then
    printf '\n  \033[32mCremio %s is already up to date.\033[0m\n' "$TAG"
    info "$target_bin"
    printf '\n'
    exit 0
fi

if [ -n "$target_bin" ]; then
    if $CHECK_ONLY; then
        printf '\n  \033[33mUpdate available: %s\033[0m\n' "$TAG"
        info "Installed binary: ${target_bin}"
        info "Run without --check to install it."
        printf '\n'
        exit 0
    fi
    step "Updating cremio -> ${TAG}"
else
    if $CHECK_ONLY; then
        printf '\n  \033[33mCremio is not installed. Latest available: %s\033[0m\n' "$TAG"
        info "Run without --check to install it."
        printf '\n'
        exit 0
    fi
    step "Installing cremio ${TAG}..."
fi

# 6. Choose destination. Replace target in place when writable, else default dir.
BIN_PATH="${INSTALL_DIR}/${BINARY}"
replaced_in_place=false
if [ -n "$target_bin" ]; then
    if [ -w "$(dirname "$target_bin")" ]; then
        BIN_PATH="$target_bin"
        INSTALL_DIR="$(dirname "$target_bin")"
        replaced_in_place=true
        info "Replacing in-place: ${BIN_PATH}"
    else
        info "Cannot write to $(dirname "$target_bin") -- installing to ${INSTALL_DIR} instead"
    fi
fi

info "Downloading ${ASSET_PATTERN} ..."
mkdir -p "$INSTALL_DIR"
mkdir -p "$(dirname "$VERSION_FILE")"

TMP_DIR="${TMPDIR:-/tmp}"
TMP_FILE="${TMP_DIR}/cremio_$(date +%s)_$$"
download "$ASSET_URL" "$TMP_FILE"

# 7. Verify the download against the published digest before installing
if [ -n "$ASSET_DIGEST" ]; then
    dl_sha=$(sha256_file "$TMP_FILE")
    if [ -n "$dl_sha" ] && [ "$dl_sha" != "$ASSET_DIGEST" ]; then
        rm -f "$TMP_FILE"
        err "SHA-256 mismatch on downloaded file. Expected ${ASSET_DIGEST}, got ${dl_sha}. Aborting."
    fi
    [ -n "$dl_sha" ] && info "Download verified (sha256 ok)"
fi

chmod +x "$TMP_FILE"
mv "$TMP_FILE" "$BIN_PATH"
info "Installed to ${BIN_PATH}"

# 8. Record version
printf '%s' "$TAG" > "$VERSION_FILE"

# 9. Add to PATH if needed (skip on in-place replace or --no-path)
if ! $replaced_in_place && ! $NO_PATH; then
    step "Checking PATH..."
    if ! echo "$PATH" | tr ':' '\n' | grep -qxF "$INSTALL_DIR"; then
        info "Adding ${INSTALL_DIR} to PATH via shell profile"
        SHELL_PROFILE=""
        case "$(basename "${SHELL:-}")" in
            zsh)  SHELL_PROFILE="${HOME}/.zshrc"  ;;
            bash) SHELL_PROFILE="${HOME}/.bashrc" ;;
            fish) SHELL_PROFILE="${HOME}/.config/fish/config.fish" ;;
            *)    SHELL_PROFILE="${HOME}/.profile" ;;
        esac
        if [ "$(basename "${SHELL:-}")" = "fish" ]; then
            echo "fish_add_path ${INSTALL_DIR}" >> "$SHELL_PROFILE"
        else
            printf '\n# Added by cremio installer\nexport PATH="%s:$PATH"\n' "$INSTALL_DIR" >> "$SHELL_PROFILE"
        fi
        info "Added to ${SHELL_PROFILE}. Restart your shell or run:"
        info "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    else
        info "${INSTALL_DIR} is already on PATH."
    fi
fi

# 10. Verify
step "Verifying installation..."
if [ -f "$BIN_PATH" ] && [ -s "$BIN_PATH" ]; then
    size=$(du -h "$BIN_PATH" | cut -f1)
    info "cremio is present at ${BIN_PATH} (${size})"
else
    err "Installation verification failed: ${BIN_PATH} not found or empty"
fi

printf '\n  \033[32mCremio %s is ready!\033[0m\n' "$TAG"
printf '  \033[36mRun "cremio" to get started.\033[0m\n'
printf '  \033[90mRe-run with --check anytime to look for updates.\033[0m\n'
printf '\n'
