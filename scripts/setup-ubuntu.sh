#!/usr/bin/env bash
set -Eeuo pipefail

readonly CLOUDFLARED_VERSION="2026.7.2"
readonly CLOUDFLARED_SHA256="ec905ea7b7e327ff8abdde8cb64697a2152de74dbcdbf6aec9db8364eb3886cd"
readonly PYTHON_MIN="3.10"

MODE="install"
if [[ "${1:-}" == "--check" ]]; then
  MODE="check"
elif [[ $# -gt 0 ]]; then
  echo "Usage: $0 [--check]" >&2
  exit 2
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUDO=()
if [[ ${EUID} -ne 0 ]]; then
  SUDO=(sudo)
fi

log() { printf '[setup] %s\n' "$*"; }
fail() { printf '[setup] ERROR: %s\n' "$*" >&2; exit 1; }
has() { command -v "$1" >/dev/null 2>&1; }

require_supported_host() {
  [[ -r /etc/os-release ]] || fail "/etc/os-release not found"
  # shellcheck disable=SC1091
  source /etc/os-release
  [[ "${ID:-}" == "ubuntu" ]] || fail "only Ubuntu is supported (found ${ID:-unknown})"
  [[ "$(uname -m)" == "x86_64" ]] || fail "only Ubuntu amd64/x86_64 is supported"
  if [[ ${EUID} -ne 0 ]] && ! has sudo; then
    fail "sudo is required when setup is not run as root"
  fi
}

find_chromium() {
  local name
  # google-chrome(-stable) checked first: on Ubuntu 22.04+, `apt install
  # chromium-browser` installs a snap trampoline script rather than a real
  # binary, which routinely fails to launch headlessly as root.
  for name in google-chrome-stable google-chrome chromium chromium-browser; do
    if has "$name"; then
      command -v "$name"
      return 0
    fi
  done
  return 1
}

python_is_compatible() {
  has python3 || return 1
  python3 -c "import sys; sys.exit(0 if sys.version_info >= tuple(map(int, '$PYTHON_MIN'.split('.'))) else 1)"
}

report_status() {
  local missing=0 chromium_path=""
  if chromium_path="$(find_chromium)"; then
    log "Chromium: $chromium_path"
  else
    log "Chromium: MISSING"
    missing=1
  fi

  local command_name
  for command_name in cloudflared tmux; do
    if has "$command_name"; then
      log "$command_name: $(command -v "$command_name")"
    else
      log "$command_name: MISSING"
      missing=1
    fi
  done

  if python_is_compatible; then
    log "Python: $(python3 --version) ($(command -v python3))"
  else
    log "Python >= $PYTHON_MIN: MISSING"
    missing=1
  fi

  if [[ -d "$ROOT_DIR/.venv" ]] && "$ROOT_DIR/.venv/bin/python" -c "import nodriver, httpx, curl_cffi, aiohttp, bs4, yaml, cryptography" >/dev/null 2>&1; then
    log "Python venv dependencies: OK ($ROOT_DIR/.venv)"
  else
    log "Python venv dependencies: MISSING"
    missing=1
  fi

  return "$missing"
}

download_verified() {
  local url="$1" expected="$2" destination="$3"
  curl --fail --location --silent --show-error "$url" --output "$destination"
  printf '%s  %s\n' "$expected" "$destination" | sha256sum --check --status ||
    fail "checksum mismatch for $url"
}

install_apt_dependencies() {
  log "Installing Ubuntu packages"
  "${SUDO[@]}" apt-get update
  "${SUDO[@]}" env DEBIAN_FRONTEND=noninteractive apt-get install -y \
    ca-certificates curl gnupg fonts-liberation libnss3 nss-plugin-pem \
    tmux python3 python3-venv python3-pip
}

install_google_chrome() {
  # Prefer a real Google Chrome .deb over `apt install chromium-browser`,
  # which on Ubuntu 22.04+ is a snap trampoline script — that routinely
  # fails to launch headlessly as root under CDP automation (nodriver).
  find_chromium >/dev/null && return
  log "Installing Google Chrome (stable)"
  "${SUDO[@]}" install -d -m 0755 /etc/apt/keyrings
  curl --fail --location --silent --show-error https://dl.google.com/linux/linux_signing_key.pub |
    "${SUDO[@]}" gpg --dearmor -o /etc/apt/keyrings/google-chrome.gpg
  echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/google-chrome.gpg] http://dl.google.com/linux/chrome/deb/ stable main" |
    "${SUDO[@]}" tee /etc/apt/sources.list.d/google-chrome.list >/dev/null
  "${SUDO[@]}" apt-get update
  "${SUDO[@]}" env DEBIAN_FRONTEND=noninteractive apt-get install -y google-chrome-stable
}

install_cloudflared() {
  has cloudflared && return
  local tmp
  tmp="$(mktemp)"
  download_verified \
    "https://github.com/cloudflare/cloudflared/releases/download/${CLOUDFLARED_VERSION}/cloudflared-linux-amd64" \
    "$CLOUDFLARED_SHA256" "$tmp"
  "${SUDO[@]}" install -m 0755 "$tmp" /usr/local/bin/cloudflared
  rm -f "$tmp"
}

setup_venv() {
  cd "$ROOT_DIR"
  if [[ ! -d .venv ]]; then
    log "Creating virtualenv at .venv"
    python3 -m venv .venv
  fi
  log "Installing Python dependencies (requirements.txt)"
  .venv/bin/pip install --upgrade pip -q
  .venv/bin/pip install -r requirements.txt -q
}

require_supported_host
if [[ "$MODE" == "check" ]]; then
  report_status || exit 1
  log "All runtime dependencies are ready"
  exit 0
fi

install_apt_dependencies
install_google_chrome
install_cloudflared
setup_venv

report_status || fail "setup completed with missing dependencies"
log "Setup complete. Edit config.yml, then run:"
log "  .venv/bin/python main.py -c config.yml"
