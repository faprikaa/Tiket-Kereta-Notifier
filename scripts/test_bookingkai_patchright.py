#!/usr/bin/env python3
"""Strategy: Patchright — a patched, drop-in Playwright fork built specifically
to defeat CDP-based bot detection (closes the runtime.Runtime.enable leak and
other CDP tells that Cloudflare/Datadome/Akamai fingerprint, which upstream
Playwright still exposes). No manual stealth JS needed — the patches are at
the browser-launch level.

Install:
    .venv/bin/pip install patchright
    .venv/bin/python -m patchright install chromium --with-deps

Usage:
    .venv/bin/python scripts/test_bookingkai_patchright.py --origin LPN --dest CKR --date 2026-09-04
"""

from __future__ import annotations

import asyncio

from _bookingkai_common import build_search_url, common_parser, missing_dependency, report

try:
    from patchright.async_api import async_playwright
except ImportError:
    raise missing_dependency("patchright", "pip install patchright && python -m patchright install chromium")


async def main() -> None:
    p = common_parser("Patchright (undetected Playwright fork) + Chromium")
    args = p.parse_args()

    url = build_search_url(args.origin, args.dest, args.date)
    print(f"URL: {url}")
    print(f"Proxy: {args.proxy or '(none — direct connection)'}")

    launch_kwargs = {"headless": not args.no_headless, "channel": "chrome"}
    if args.proxy:
        launch_kwargs["proxy"] = {"server": args.proxy.replace("socks5h://", "socks5://", 1)}

    async with async_playwright() as pw:
        # Patchright recommends persistent contexts (real profile shape) over launch()+new_context().
        context = await pw.chromium.launch_persistent_context(
            user_data_dir="",  # empty = ephemeral temp profile
            locale="id-ID",
            timezone_id="Asia/Jakarta",
            viewport={"width": 1920, "height": 1080},
            **launch_kwargs,
        )
        page = context.pages[0] if context.pages else await context.new_page()
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
            report(html, title, "bookingkai_debug_patchright.html", args.proxy)
        finally:
            await context.close()


if __name__ == "__main__":
    asyncio.run(main())
