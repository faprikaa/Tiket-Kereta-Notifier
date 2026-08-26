#!/usr/bin/env python3
"""Strategy: Playwright + Chromium, with manual stealth JS patches.

Different automation stack from the nodriver-based provider (WebDriver BiDi
protocol instead of raw CDP), plus hand-rolled patches for the fingerprint
signals Cloudflare commonly checks (navigator.webdriver, plugins, languages,
chrome runtime object, permissions.query).

Install:
    .venv/bin/pip install playwright
    .venv/bin/python -m playwright install chromium --with-deps

Usage:
    .venv/bin/python scripts/test_bookingkai_playwright_chromium.py --origin LPN --dest CKR --date 2026-09-04
"""

from __future__ import annotations

import asyncio

from _bookingkai_common import build_search_url, common_parser, find_chromium, missing_dependency, report

try:
    from playwright.async_api import async_playwright
except ImportError:
    raise missing_dependency("playwright", "pip install playwright && python -m playwright install chromium")

STEALTH_JS = """
Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
Object.defineProperty(navigator, 'languages', { get: () => ['id-ID', 'id', 'en-US', 'en'] });
Object.defineProperty(navigator, 'plugins', { get: () => [1, 2, 3, 4, 5] });
window.chrome = { runtime: {} };
const originalQuery = window.navigator.permissions.query;
window.navigator.permissions.query = (parameters) => (
    parameters.name === 'notifications'
        ? Promise.resolve({ state: Notification.permission })
        : originalQuery(parameters)
);
"""


async def main() -> None:
    p = common_parser("Playwright + Chromium with manual stealth patches")
    p.add_argument("--chromium", default="", help="Path to Chrome/Chromium binary (auto-detect if omitted)")
    args = p.parse_args()

    url = build_search_url(args.origin, args.dest, args.date)
    print(f"URL: {url}")
    print(f"Proxy: {args.proxy or '(none — direct connection)'}")

    launch_kwargs = {"headless": not args.no_headless, "args": ["--disable-blink-features=AutomationControlled"]}
    chromium_bin = args.chromium or find_chromium()
    if chromium_bin:
        launch_kwargs["executable_path"] = chromium_bin
        print(f"Using browser: {chromium_bin}")
    if args.proxy:
        launch_kwargs["proxy"] = {"server": args.proxy.replace("socks5h://", "socks5://", 1)}

    async with async_playwright() as pw:
        browser = await pw.chromium.launch(**launch_kwargs)
        context = await browser.new_context(
            locale="id-ID",
            timezone_id="Asia/Jakarta",
            viewport={"width": 1920, "height": 1080},
            user_agent=(
                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
                "(KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
            ),
        )
        await context.add_init_script(STEALTH_JS)
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
            report(html, title, "bookingkai_debug_playwright_chromium.html", args.proxy)
        finally:
            await browser.close()


if __name__ == "__main__":
    asyncio.run(main())
