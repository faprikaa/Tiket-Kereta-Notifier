#!/usr/bin/env python3
"""Strategy: Playwright + WebKit (Safari engine).

Third distinct rendering engine to try (after Chromium and Firefox). WebKit's
JS engine (JavaScriptCore) and fingerprint surface are the furthest from what
most bot fingerprinting is tuned against, since it's the least common engine
in headless automation. No proxy support in Playwright's WebKit on Linux —
if you need a proxy, use one of the Chromium/Firefox scripts instead.

Install:
    .venv/bin/pip install playwright
    .venv/bin/python -m playwright install webkit --with-deps

Usage:
    .venv/bin/python scripts/test_bookingkai_playwright_webkit.py --origin LPN --dest CKR --date 2026-09-04
"""

from __future__ import annotations

import asyncio

from _bookingkai_common import build_search_url, common_parser, missing_dependency, report

try:
    from playwright.async_api import async_playwright
except ImportError:
    raise missing_dependency("playwright", "pip install playwright && python -m playwright install webkit")


async def main() -> None:
    p = common_parser("Playwright + WebKit")
    args = p.parse_args()
    if args.proxy:
        print("NOTE: WebKit proxy support is unreliable on Linux; ignoring --proxy.")

    url = build_search_url(args.origin, args.dest, args.date)
    print(f"URL: {url}")

    async with async_playwright() as pw:
        browser = await pw.webkit.launch(headless=not args.no_headless)
        context = await browser.new_context(
            locale="id-ID",
            timezone_id="Asia/Jakarta",
            viewport={"width": 1920, "height": 1080},
        )
        page = await context.new_page()
        try:
            print("Warming up on homepage...")
            await page.goto("https://booking.kai.id/", timeout=60_000)
            for i in range(15):
                await asyncio.sleep(2)
                html = await page.content()
                from notifier.providers.bookingkai_parse import is_cloudflare_challenge
                if not is_cloudflare_challenge(html):
                    break
                print(f"  still challenged, waiting... ({i + 1})")

            print("Navigating to search URL...")
            await page.goto(url, timeout=60_000)
            await asyncio.sleep(3)
            html = await page.content()
            title = await page.title()
            report(html, title, "bookingkai_debug_playwright_webkit.html")
        finally:
            await browser.close()


if __name__ == "__main__":
    asyncio.run(main())
