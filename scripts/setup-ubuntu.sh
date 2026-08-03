#!/usr/bin/env bash
set -Eeuo pipefail

readonly CLOUDFLARED_VERSION="2026.7.2"
readonly CLOUDFLARED_SHA256="ec905ea7b7e327ff8abdde8cb64697a2152de74dbcdbf6aec9db8364eb3886cd"
readonly CURL_IMPERSONATE_VERSION="0.6.1"
readonly CURL_IMPERSONATE_SHA256="fa1e1614f7ba69ccc66721a0f38be457a3647eb64c75d66974b56186e3316b12"

MODE="install"
if [[ "${1:-}" == "--check" ]]; then
  MODE="check"
elif [[ $# -gt 0 ]]; then
  echo "Usage: $0 [--check]" >&2
  exit 2
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
GO_REQUIRED="$(awk '/^go / { print $2; exit }' "$ROOT_DIR/go.mod")"
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
  for name in chromium-browser chromium google-chrome-stable google-chrome; do
    if has "$name"; then
      command -v "$name"
      return 0
    fi
  done
  return 1
}

go_is_compatible() {
  has go || return 1
  local installed
  installed="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
  [[ -n "$installed" ]] && dpkg --compare-versions "$installed" ge "$GO_REQUIRED"
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
  for command_name in curl cloudflared curl_chrome110 tmux; do
    if has "$command_name"; then
      log "$command_name: $(command -v "$command_name")"
    else
      log "$command_name: MISSING"
      missing=1
    fi
  done

  if go_is_compatible; then
    log "Go: $(go env GOVERSION) ($(command -v go))"
  else
    log "Go >= $GO_REQUIRED: MISSING"
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
    build-essential ca-certificates curl fonts-liberation libnss3 \
    nss-plugin-pem tar tmux xz-utils chromium-browser
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

install_curl_impersonate() {
  has curl_chrome110 && return
  local tmp_dir archive
  tmp_dir="$(mktemp -d)"
  archive="$tmp_dir/curl-impersonate.tar.gz"
  download_verified \
    "https://github.com/lwthiker/curl-impersonate/releases/download/v${CURL_IMPERSONATE_VERSION}/curl-impersonate-v${CURL_IMPERSONATE_VERSION}.x86_64-linux-gnu.tar.gz" \
    "$CURL_IMPERSONATE_SHA256" "$archive"
  tar -xzf "$archive" -C "$tmp_dir"
  "${SUDO[@]}" install -m 0755 "$tmp_dir"/curl-impersonate-* /usr/local/bin/
  "${SUDO[@]}" install -m 0755 "$tmp_dir"/curl_* /usr/local/bin/
  rm -rf "$tmp_dir"
}

install_go() {
  go_is_compatible && return
  local tmp_dir archive checksum install_dir
  tmp_dir="$(mktemp -d)"
  archive="$tmp_dir/go.tar.gz"
  install_dir="/opt/go/$GO_REQUIRED"
  checksum="$(curl --fail --location --silent --show-error "https://go.dev/dl/go${GO_REQUIRED}.linux-amd64.tar.gz.sha256")"
  download_verified "https://go.dev/dl/go${GO_REQUIRED}.linux-amd64.tar.gz" "$checksum" "$archive"
  "${SUDO[@]}" mkdir -p "$install_dir"
  "${SUDO[@]}" tar -xzf "$archive" --strip-components=1 -C "$install_dir"
  "${SUDO[@]}" ln -sfn "$install_dir/bin/go" /usr/local/bin/go
  "${SUDO[@]}" ln -sfn "$install_dir/bin/gofmt" /usr/local/bin/gofmt
  rm -rf "$tmp_dir"
  hash -r
}

require_supported_host
if [[ "$MODE" == "check" ]]; then
  report_status || exit 1
  log "All runtime dependencies are ready"
  exit 0
fi

install_apt_dependencies
install_cloudflared
install_curl_impersonate
install_go

cd "$ROOT_DIR"
log "Downloading Go modules"
go mod download
mkdir -p bin
log "Building bin/tiket-kereta-notifier"
go build -o bin/tiket-kereta-notifier ./cmd

report_status || fail "setup completed with missing dependencies"
log "Setup complete. Edit config.yml, then run the bot in tmux."
