#!/usr/bin/env python3
"""Strategy: Playwright + Firefox.

A completely different rendering/JS engine than Chromium — Cloudflare's
challenge fingerprinting is heavily Chromium/V8-oriented, so a Gecko-based
client can land in a different bucket entirely (for better or worse).
Firefox has no navigator.webdriver-via-CDP tell the way Chromium does.

Install:
    .venv/bin/pip install playwright
    .venv/bin/python -m playwright install firefox --with-deps

Usage:
    .venv/bin/python scripts/test_bookingkai_playwright_firefox.py --origin LPN --dest CKR --date 2026-09-04
"""

from __future__ import annotations

import asyncio

from _bookingkai_common import build_search_url, common_parser, missing_dependency, report

try:
    from playwright.async_api import async_playwright
except ImportError:
    raise missing_dependency("playwright", "pip install playwright && python -m playwright install firefox")


async def main() -> None:
    p = common_parser("Playwright + Firefox")
    args = p.parse_args()

    url = build_search_url(args.origin, args.dest, args.date)
    print(f"URL: {url}")
    print(f"Proxy: {args.proxy or '(none — direct connection)'}")

    launch_kwargs = {"headless": not args.no_headless}
    if args.proxy:
        launch_kwargs["proxy"] = {"server": args.proxy.replace("socks5h://", "socks5://", 1)}

    async with async_playwright() as pw:
        browser = await pw.firefox.launch(
            **launch_kwargs,
            firefox_user_prefs={
                "dom.webdriver.enabled": False,
                "useAutomationExtension": False,
                "general.useragent.locale": "id-ID",
                "intl.accept_languages": "id-ID,id,en-US,en",
            },
        )
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
            report(html, title, "bookingkai_debug_playwright_firefox.html", args.proxy)
        finally:
            await browser.close()


if __name__ == "__main__":
    asyncio.run(main())
