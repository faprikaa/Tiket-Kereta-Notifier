#!/usr/bin/env bash
# Runs every scripts/test_bookingkai*.py strategy sequentially against the
# same route/date, logs each one, and prints a summary of the RESULT lines.
#
# Usage:
#   scripts/run_all_bookingkai_tests.sh --origin LPN --dest CKR --date 2026-09-04 [--proxy socks5://host:port]
set -u

ORIGIN=""
DEST=""
DATE=""
PROXY=""

while [ $# -gt 0 ]; do
  case "$1" in
    --origin) ORIGIN="$2"; shift 2 ;;
    --dest) DEST="$2"; shift 2 ;;
    --date) DATE="$2"; shift 2 ;;
    --proxy) PROXY="$2"; shift 2 ;;
    *) echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

if [ -z "$ORIGIN" ] || [ -z "$DEST" ] || [ -z "$DATE" ]; then
  echo "Usage: $0 --origin LPN --dest CKR --date 2026-09-04 [--proxy socks5://host:port]" >&2
  exit 1
fi

cd "$(dirname "$0")/.."
PY=".venv/bin/python"
LOGDIR="/tmp/bookingkai-strategy-logs"
mkdir -p "$LOGDIR"

EXTRA_ARGS=(--origin "$ORIGIN" --dest "$DEST" --date "$DATE")
if [ -n "$PROXY" ]; then
  EXTRA_ARGS+=(--proxy "$PROXY")
fi

SCRIPTS=(
  scripts/test_bookingkai.py
  scripts/test_bookingkai_nodriver_stealth.py
  scripts/test_bookingkai_nodriver_verify_cf.py
  scripts/test_bookingkai_playwright_chromium.py
  scripts/test_bookingkai_playwright_firefox.py
  scripts/test_bookingkai_playwright_webkit.py
  scripts/test_bookingkai_patchright.py
  scripts/test_bookingkai_camoufox.py
  scripts/test_bookingkai_drissionpage.py
  scripts/test_bookingkai_curl_cffi.py
)

for script in "${SCRIPTS[@]}"; do
  name="$(basename "$script" .py)"
  echo "=== $name ==="
  "$PY" "$script" "${EXTRA_ARGS[@]}" 2>&1 | tee "$LOGDIR/$name.log"
  echo
done

# SeleniumBase UC mode is synchronous and wants a (virtual) display.
if command -v xvfb-run >/dev/null 2>&1; then
  echo "=== test_bookingkai_seleniumbase_uc ==="
  xvfb-run -a "$PY" scripts/test_bookingkai_seleniumbase_uc.py "${EXTRA_ARGS[@]}" \
    2>&1 | tee "$LOGDIR/test_bookingkai_seleniumbase_uc.log"
else
  echo "=== test_bookingkai_seleniumbase_uc SKIPPED (xvfb-run not found; sudo apt install -y xvfb) ==="
fi

echo
echo "=== SUMMARY ==="
grep -H "^RESULT" "$LOGDIR"/*.log
