#!/usr/bin/env python3
"""Strategy: nodriver (same library as the real provider), but with a
heavier evasion + human-simulation pass than scripts/test_bookingkai.py:

  - CDP-level stealth JS injected before ANY page script runs (raw
    Page.addScriptToEvaluateOnNewDocument via tab.send), patching
    navigator.webdriver/plugins/languages/permissions and WebGL
    vendor/renderer strings.
  - A random, more "real" desktop viewport + matching UA per run.
  - Small random mouse moves + a scroll after the homepage loads, before
    reading content — some Cloudflare Managed Challenge configurations key
    off total absence of any pointer/scroll activity on a page that a real
    user would have interacted with.
  - Arrives via a Google search results page as the referrer, instead of
    navigating to booking.kai.id directly — the project's own notes suggest
    the WAF block may be IP-reputation driven rather than fingerprinting, so
    this is the "does referrer/origin matter" experiment specifically.

nodriver is already in requirements.txt — no extra install needed.

Usage:
    .venv/bin/python scripts/test_bookingkai_nodriver_stealth.py --origin LPN --dest CKR --date 2026-09-04
    .venv/bin/python scripts/test_bookingkai_nodriver_stealth.py --origin LPN --dest CKR --date 2026-09-04 --via-google
"""

from __future__ import annotations

import asyncio
import random

import nodriver
from nodriver.cdp import page as cdp_page

from _bookingkai_common import build_search_url, common_parser, find_chromium, report

STEALTH_JS = """
Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
Object.defineProperty(navigator, 'languages', { get: () => ['id-ID', 'id', 'en-US', 'en'] });
Object.defineProperty(navigator, 'plugins', { get: () => [1, 2, 3, 4, 5] });
window.chrome = { runtime: {} };
const getParameter = WebGLRenderingContext.prototype.getParameter;
WebGLRenderingContext.prototype.getParameter = function (parameter) {
    if (parameter === 37445) return 'Intel Inc.';
    if (parameter === 37446) return 'Intel Iris OpenGL Engine';
    return getParameter.call(this, parameter);
};
"""

VIEWPORTS = [(1920, 1080), (1536, 864), (1366, 768), (1440, 900)]


async def _human_jiggle(tab: nodriver.Tab) -> None:
    for _ in range(3):
        x, y = random.randint(100, 1200), random.randint(100, 700)
        try:
            await tab.mouse_move(x, y)
        except Exception:  # noqa: BLE001 - best-effort, not all nodriver versions expose this
            break
        await asyncio.sleep(random.uniform(0.2, 0.6))
    try:
        await tab.scroll_down(random.randint(200, 500))
    except Exception:  # noqa: BLE001
        pass


async def main() -> None:
    p = common_parser("nodriver + CDP-level stealth patches + human-like mouse/scroll")
    p.add_argument("--chromium", default="", help="Path to Chrome/Chromium binary (auto-detect if omitted)")
    p.add_argument("--via-google", action="store_true", help="Arrive from a Google search results page instead of direct")
    args = p.parse_args()

    chromium_bin = args.chromium or find_chromium()
    if not chromium_bin:
        raise SystemExit("No Chrome/Chromium binary found. Pass --chromium /path/to/binary")
    print(f"Using browser: {chromium_bin}")

    url = build_search_url(args.origin, args.dest, args.date)
    print(f"URL: {url}")
    print(f"Proxy: {args.proxy or '(none — direct connection)'}")

    width, height = random.choice(VIEWPORTS)
    print(f"Viewport: {width}x{height}")

    browser_args = [
        "--disable-blink-features=AutomationControlled",
        "--disable-dev-shm-usage",
        "--disable-gpu",
        f"--window-size={width},{height}",
    ]
    if args.proxy:
        browser_args.append(f"--proxy-server={args.proxy.replace('socks5h://', 'socks5://', 1)}")

    browser = await nodriver.start(
        headless=not args.no_headless,
        browser_executable_path=chromium_bin,
        browser_args=browser_args,
        lang="id-ID",
        sandbox=False,
    )
    try:
        from notifier.providers.bookingkai_parse import is_cloudflare_challenge

        if args.via_google:
            print("Arriving via Google search (referrer experiment)...")
            tab = await browser.get(f"https://www.google.com/search?q={args.origin}+{args.dest}+booking.kai.id")
            await asyncio.sleep(2)
            await tab.get("https://booking.kai.id/")
        else:
            tab = await browser.get("https://booking.kai.id/")

        await tab.send(cdp_page.add_script_to_evaluate_on_new_document(STEALTH_JS))

        print("Warming up on homepage...")
        for i in range(15):
            await asyncio.sleep(2)
            html = await tab.get_content()
            if not is_cloudflare_challenge(html):
                break
            print(f"  still challenged, waiting... ({i + 1})")

        await _human_jiggle(tab)

        print("Navigating to search URL...")
        await tab.get(url)
        await asyncio.sleep(3)
        await _human_jiggle(tab)

        html = await tab.get_content()
        title = await tab.evaluate("document.title", return_by_value=True)
        report(html, title, "bookingkai_debug_nodriver_stealth.html", args.proxy)
    finally:
        browser.stop()


if __name__ == "__main__":
    asyncio.run(main())
