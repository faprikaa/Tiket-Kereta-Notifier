#!/usr/bin/env python3
"""Standalone BookingKAI/Cloudflare test — no Telegram, no scheduler, one search.

Usage:
    .venv/bin/python scripts/test_bookingkai.py --origin LPN --dest CKR --date 2026-09-04
    .venv/bin/python scripts/test_bookingkai.py --origin LPN --dest CKR --date 2026-09-04 --proxy socks5://127.0.0.1:40000
    .venv/bin/python scripts/test_bookingkai.py --origin LPN --dest CKR --date 2026-09-04 --no-headless

Saves the fetched page to bookingkai_debug.html regardless of outcome, so you
can inspect exactly what Chrome landed on.
"""

from __future__ import annotations

import argparse
import asyncio
import shutil
import sys
from pathlib import Path
from urllib.parse import quote

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import nodriver

from notifier.providers.bookingkai import MONTH_NAMES
from notifier.providers.bookingkai_parse import (
    extract_net_error,
    is_cloudflare_challenge,
    is_navigation_error,
    is_waiting_room,
    parse_trains,
)
from notifier.providers.browser_queue import CHROMIUM_CANDIDATES


def find_chromium() -> str:
    for name in CHROMIUM_CANDIDATES:
        path = shutil.which(name)
        if path:
            return path
    return ""


async def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--origin", required=True)
    p.add_argument("--dest", required=True)
    p.add_argument("--date", required=True, help="YYYY-MM-DD")
    p.add_argument("--proxy", default="", help="e.g. socks5://127.0.0.1:40000 (omit to go direct)")
    p.add_argument("--chromium", default="", help="Path to Chrome/Chromium binary (auto-detect if omitted)")
    p.add_argument("--no-headless", action="store_true", help="Show the browser window")
    args = p.parse_args()

    chromium_bin = args.chromium or find_chromium()
    if not chromium_bin:
        sys.exit("No Chrome/Chromium binary found. Pass --chromium /path/to/binary")
    print(f"Using browser: {chromium_bin}")
    print(f"Proxy: {args.proxy or '(none — direct connection)'}")

    year, month, day = args.date.split("-")
    date_indo = f"{int(day):02d}-{MONTH_NAMES[int(month)]}-{int(year)}"
    url = (
        f"https://booking.kai.id/?origination={args.origin}&destination={args.dest}"
        f"&tanggal={quote(date_indo)}&adult=1&infant=0&submit=Cari+%26+Pesan+Tiket"
    )
    print(f"URL: {url}")

    browser_args = ["--disable-blink-features=AutomationControlled", "--disable-dev-shm-usage", "--disable-gpu"]
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
        tab = await browser.get("https://booking.kai.id/")
        print("Warming up on homepage...")
        for i in range(15):
            await asyncio.sleep(2)
            html = await tab.get_content()
            if not is_cloudflare_challenge(html):
                break
            print(f"  still challenged, waiting... ({i + 1})")
        title = await tab.evaluate("document.title", return_by_value=True)
        print(f"Homepage title: {title!r}")

        print("Navigating to search URL...")
        await tab.get(url)
        await asyncio.sleep(3)
        html = await tab.get_content()
        title = await tab.evaluate("document.title", return_by_value=True)

        Path("bookingkai_debug.html").write_text(html, encoding="utf-8")
        print(f"Saved page to bookingkai_debug.html ({len(html)} bytes)")
        print(f"Page title: {title!r}")

        if is_navigation_error(html):
            print(f"RESULT: navigation failed — {extract_net_error(html)}")
            if args.proxy:
                print("        (check that the proxy is actually reachable from this host)")
        elif is_cloudflare_challenge(html):
            print("RESULT: blocked by Cloudflare (challenge or WAF block page)")
        elif is_waiting_room(html):
            print("RESULT: blocked by Cloudflare Waiting Room")
        else:
            trains = parse_trains(html)
            print(f"RESULT: OK — {len(trains)} trains parsed")
            for t in trains:
                print(f"  - {t.name} [{t.train_class}] {t.departure_time}->{t.arrival_time} "
                      f"seats={t.seats_left} price={t.price} avail={t.availability}")
    finally:
        browser.stop()


if __name__ == "__main__":
    asyncio.run(main())
