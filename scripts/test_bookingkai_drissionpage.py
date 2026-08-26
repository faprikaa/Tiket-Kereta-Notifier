#!/usr/bin/env python3
"""Strategy: DrissionPage — another pure-CDP browser controller (like
nodriver, no Selenium/WebDriver layer), but a completely independent
implementation with its own connection/timing behavior and built-in
`get(..., retry=...)` handling. Worth trying because two CDP libraries that
both avoid navigator.webdriver can still behave differently enough
(navigation timing, header order, JS execution order) to land on different
sides of Cloudflare's heuristics.

Install:
    .venv/bin/pip install DrissionPage

Usage:
    .venv/bin/python scripts/test_bookingkai_drissionpage.py --origin LPN --dest CKR --date 2026-09-04
"""

from __future__ import annotations

import time

from _bookingkai_common import build_search_url, common_parser, find_chromium, missing_dependency, report

try:
    from DrissionPage import ChromiumOptions, ChromiumPage
except ImportError:
    raise missing_dependency("DrissionPage", "pip install DrissionPage")


def main() -> None:
    p = common_parser("DrissionPage (independent CDP-only browser controller)")
    p.add_argument("--chromium", default="", help="Path to Chrome/Chromium binary (auto-detect if omitted)")
    args = p.parse_args()

    url = build_search_url(args.origin, args.dest, args.date)
    print(f"URL: {url}")
    print(f"Proxy: {args.proxy or '(none — direct connection)'}")

    co = ChromiumOptions()
    chromium_bin = args.chromium or find_chromium()
    if chromium_bin:
        co.set_browser_path(chromium_bin)
        print(f"Using browser: {chromium_bin}")
    if not args.no_headless:
        co.headless(True)
    co.set_argument("--disable-blink-features=AutomationControlled")
    co.set_argument("--disable-dev-shm-usage")
    co.set_argument("--window-size=1920,1080")
    if args.proxy:
        co.set_proxy(args.proxy.replace("socks5h://", "socks5://", 1))

    page = ChromiumPage(co)
    try:
        print("Warming up on homepage...")
        page.get("https://booking.kai.id/")
        from notifier.providers.bookingkai_parse import is_cloudflare_challenge
        for i in range(15):
            time.sleep(2)
            html = page.html
            if not is_cloudflare_challenge(html):
                break
            print(f"  still challenged, waiting... ({i + 1})")

        print("Navigating to search URL...")
        page.get(url)
        time.sleep(3)
        html = page.html
        title = page.title
        report(html, title, "bookingkai_debug_drissionpage.html", args.proxy)
    finally:
        page.quit()


if __name__ == "__main__":
    main()
