#!/usr/bin/env bash
# Show cloudflared errors/warnings from the hourly log files.
#
#   ./scripts/check-tunnel-logs.sh          # current hour
#   ./scripts/check-tunnel-logs.sh 6        # last 6 hourly files
#   ./scripts/check-tunnel-logs.sh all      # every file kept
#   ./scripts/check-tunnel-logs.sh follow   # tail -f the current hour
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dir="${CLOUDFLARED_LOG_DIR:-$root/logs}"
arg="${1:-1}"

shopt -s nullglob
files=("$dir"/cloudflared-*.log)
if [ ${#files[@]} -eq 0 ]; then
  echo "No cloudflared logs in $dir" >&2
  exit 1
fi
IFS=$'\n' files=($(printf '%s\n' "${files[@]}" | sort)); unset IFS

case "$arg" in
  follow) exec tail -f "${files[-1]}" ;;
  all)    selected=("${files[@]}") ;;
  *[!0-9]*) echo "usage: $0 [N|all|follow]" >&2; exit 2 ;;
  *)      selected=("${files[@]: -$arg}") ;;
esac

echo "Scanning ${#selected[@]} file(s) in $dir"
printf '  %s\n' "${selected[@]##*/}"
echo

# Same severity tokens tunnel.py greps for: ERR/FTL/PNC = error, WRN = warning.
if grep -nE '\b(ERR|FTL|PNC)\b|level=(error|fatal|panic)' "${selected[@]}"; then
  echo
  echo "^ errors found"
  status=1
else
  echo "No errors."
  status=0
fi

echo
echo "--- warnings ---"
grep -hE '\bWRN\b|level=warn' "${selected[@]}" || echo "No warnings."

echo
echo "--- last 10 lines ---"
tail -n 10 "${selected[-1]}"

exit "$status"
