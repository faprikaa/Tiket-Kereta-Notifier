#!/usr/bin/env python3
"""Strategy: Camoufox — a hardened Firefox build purpose-made for
anti-detect scraping. Patches fingerprint surfaces (canvas, WebGL, fonts,
audio context, screen/hardware specs, timezone/locale) at the C++ level
inside the browser itself rather than via injected JS, which is what most
"stealth" JS patches (like the ones in test_bookingkai_playwright_chromium.py)
can't do — those are detectable-in-principle because they modify JS objects
after the page's JS context already exists.

Install:
    .venv/bin/pip install -U camoufox[geoip]
    .venv/bin/python -m camoufox fetch

Usage:
    .venv/bin/python scripts/test_bookingkai_camoufox.py --origin LPN --dest CKR --date 2026-09-04
"""

from __future__ import annotations

import asyncio

from _bookingkai_common import build_search_url, common_parser, missing_dependency, report

try:
    from camoufox.async_api import AsyncCamoufox
except ImportError:
    raise missing_dependency("camoufox", "pip install -U camoufox[geoip] && python -m camoufox fetch")


async def main() -> None:
    p = common_parser("Camoufox (hardened anti-fingerprint Firefox)")
    args = p.parse_args()

    url = build_search_url(args.origin, args.dest, args.date)
    print(f"URL: {url}")
    print(f"Proxy: {args.proxy or '(none — direct connection)'}")

    camoufox_kwargs = {
        "headless": not args.no_headless,
        "os": ("windows", "macos", "linux"),
        "locale": "id-ID",
        "geoip": True,
    }
    if args.proxy:
        camoufox_kwargs["proxy"] = {"server": args.proxy.replace("socks5h://", "socks5://", 1)}

    async with AsyncCamoufox(**camoufox_kwargs) as browser:
        page = await browser.new_page()
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
            report(html, title, "bookingkai_debug_camoufox.html", args.proxy)
        finally:
            await page.close()


if __name__ == "__main__":
    asyncio.run(main())
