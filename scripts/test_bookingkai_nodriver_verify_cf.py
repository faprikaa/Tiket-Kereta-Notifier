#!/usr/bin/env python3
"""Strategy: nodriver's built-in `Tab.verify_cf()` — the one strategy here
aimed squarely at the *interactive* Cloudflare Turnstile checkbox rather
than the automatic JS challenge. It screenshots the viewport, template-matches
the checkbox location with OpenCV, and dispatches a real CDP mouse click on
it — this is the gap the project's own docs call out ("CAPTCHA interaktif
tetap tidak dipecahkan otomatis").

Works headless (it's a real screenshot via CDP, no display needed), but only
helps if Cloudflare actually renders a clickable Turnstile box rather than a
fully-automatic managed challenge or an outright WAF block — run this after
confirming (e.g. with --no-headless once, or bookingkai_debug*.html from the
other scripts) that a checkbox is really what's being shown.

Install:
    .venv/bin/pip install opencv-python-headless
    (nodriver itself is already in requirements.txt)

Usage:
    .venv/bin/python scripts/test_bookingkai_nodriver_verify_cf.py --origin LPN --dest CKR --date 2026-09-04
"""

from __future__ import annotations

import asyncio

import nodriver

from _bookingkai_common import build_search_url, common_parser, find_chromium, missing_dependency, report

try:
    import cv2  # noqa: F401
except ImportError:
    raise missing_dependency("opencv-python", "pip install opencv-python-headless")


async def main() -> None:
    p = common_parser("nodriver verify_cf() — clicks the Turnstile checkbox via CDP mouse + template match")
    p.add_argument("--chromium", default="", help="Path to Chrome/Chromium binary (auto-detect if omitted)")
    args = p.parse_args()

    chromium_bin = args.chromium or find_chromium()
    if not chromium_bin:
        raise SystemExit("No Chrome/Chromium binary found. Pass --chromium /path/to/binary")
    print(f"Using browser: {chromium_bin}")

    url = build_search_url(args.origin, args.dest, args.date)
    print(f"URL: {url}")
    print(f"Proxy: {args.proxy or '(none — direct connection)'}")

    browser_args = ["--disable-blink-features=AutomationControlled", "--disable-dev-shm-usage", "--disable-gpu"]
    if args.proxy:
        browser_args.append(f"--proxy-server={args.proxy.replace('socks5h://', 'socks5://', 1)}")

    # expert=False (default) is required — verify_cf refuses to run in expert mode.
    browser = await nodriver.start(
        headless=not args.no_headless,
        browser_executable_path=chromium_bin,
        browser_args=browser_args,
        lang="id-ID",
        sandbox=False,
    )
    try:
        from notifier.providers.bookingkai_parse import is_cloudflare_challenge

        tab = await browser.get("https://booking.kai.id/")
        print("Waiting for challenge page to render...")
        await asyncio.sleep(4)

        html = await tab.get_content()
        if is_cloudflare_challenge(html):
            print("Challenge detected — attempting to click the Turnstile checkbox...")
            try:
                await tab.verify_cf(flash=True)
                print("Click dispatched, waiting for the page to settle...")
                await asyncio.sleep(5)
            except Exception as e:  # noqa: BLE001
                print(f"  verify_cf() raised: {e}")
        else:
            print("No challenge detected on homepage (nothing to click).")

        print("Navigating to search URL...")
        await tab.get(url)
        await asyncio.sleep(3)
        html = await tab.get_content()
        title = await tab.evaluate("document.title", return_by_value=True)
        report(html, title, "bookingkai_debug_nodriver_verify_cf.html", args.proxy)
    finally:
        browser.stop()


if __name__ == "__main__":
    asyncio.run(main())
