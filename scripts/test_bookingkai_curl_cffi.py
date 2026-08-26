#!/usr/bin/env python3
"""Strategy: curl_cffi — no browser at all. Impersonates a real browser's
TLS/JA3 fingerprint and HTTP/2 frame ordering at the request layer, which is
what Cloudflare's *first-pass* bot check (before any JS challenge) actually
keys off. This is the cheapest possible thing to try: fast, no Chromium
process, no rendering — and it doubles as a way to check whether the site
even needs a JS challenge for a given IP, or just blocks non-browser TLS
fingerprints outright. It cannot solve an interactive JS/Turnstile challenge
if one is served; use it to characterize how the block happens, not to push
past a real challenge.

curl_cffi is already in requirements.txt — no extra install needed.

Usage:
    .venv/bin/python scripts/test_bookingkai_curl_cffi.py --origin LPN --dest CKR --date 2026-09-04
    .venv/bin/python scripts/test_bookingkai_curl_cffi.py --origin LPN --dest CKR --date 2026-09-04 --impersonate safari17_2_ios
"""

from __future__ import annotations

from curl_cffi import requests

from _bookingkai_common import build_search_url, common_parser, report

# A handful of impersonation targets worth sweeping — different TLS/JA3 +
# HTTP/2 fingerprints. Cloudflare's non-JS heuristics can treat these very
# differently even though they're all "just curl" under the hood.
IMPERSONATE_TARGETS = ("chrome124", "chrome120", "edge101", "safari17_2_ios", "safari15_5")


def main() -> None:
    p = common_parser("curl_cffi (TLS/JA3 impersonation, no browser)")
    p.add_argument(
        "--impersonate", default="", help=f"One target to try, or omit to sweep all of: {', '.join(IMPERSONATE_TARGETS)}"
    )
    args = p.parse_args()

    url = build_search_url(args.origin, args.dest, args.date)
    print(f"URL: {url}")
    print(f"Proxy: {args.proxy or '(none — direct connection)'}")

    targets = [args.impersonate] if args.impersonate else list(IMPERSONATE_TARGETS)
    proxies = {"http": args.proxy, "https": args.proxy} if args.proxy else None

    for target in targets:
        print(f"\n=== impersonate={target} ===")
        try:
            resp = requests.get(
                url,
                impersonate=target,
                proxies=proxies,
                timeout=30,
                headers={
                    "Accept-Language": "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7",
                    "Referer": "https://booking.kai.id/",
                },
            )
        except Exception as e:  # noqa: BLE001
            print(f"RESULT: request failed — {e}")
            continue

        print(f"HTTP {resp.status_code}")
        debug_file = f"bookingkai_debug_curl_cffi_{target}.html"
        report(resp.text, target, debug_file, args.proxy)


if __name__ == "__main__":
    main()
