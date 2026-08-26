#!/usr/bin/env python3
"""Strategy: SeleniumBase UC Mode (undetected-chromedriver-based).

Different from every other script here: it's built to physically click the
Cloudflare Turnstile "verify you are human" checkbox using OS-level mouse
input (via pyautogui) rather than a synthetic JS/CDP click, which is exactly
the interactive-challenge case the rest of this project can't solve
headlessly. Needs a real or virtual display — on a headless server, install
Xvfb and either run under `xvfb-run` or pass --no-headless with `xvfb=True`
baked in below.

This script is synchronous (SeleniumBase's API), unlike the others.

Install:
    .venv/bin/pip install seleniumbase
    sudo apt install -y xvfb  # only needed on a headless Linux server

Usage:
    xvfb-run -a .venv/bin/python scripts/test_bookingkai_seleniumbase_uc.py --origin LPN --dest CKR --date 2026-09-04
"""

from __future__ import annotations

import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from _bookingkai_common import build_search_url, common_parser, missing_dependency, report

try:
    from seleniumbase import SB
except ImportError:
    raise missing_dependency("seleniumbase", "pip install seleniumbase")


def main() -> None:
    p = common_parser("SeleniumBase UC Mode (undetected-chromedriver)")
    args = p.parse_args()

    url = build_search_url(args.origin, args.dest, args.date)
    print(f"URL: {url}")
    print(f"Proxy: {args.proxy or '(none — direct connection)'}")

    sb_kwargs = {
        "uc": True,
        "headless": False if args.no_headless else None,  # UC mode does best undetected/xvfb, not true headless
        "xvfb": True,  # auto-spin a virtual display on Linux if no real one is attached
        "locale_code": "id",
    }
    if args.proxy:
        sb_kwargs["proxy"] = args.proxy.replace("socks5h://", "socks5://", 1)

    with SB(**sb_kwargs) as sb:
        print("Warming up on homepage (UC reconnect + auto-click Turnstile if shown)...")
        sb.uc_open_with_reconnect("https://booking.kai.id/", reconnect_time=4)
        try:
            sb.uc_gui_click_captcha()
        except Exception as e:  # noqa: BLE001 - no captcha present is not an error
            print(f"  (no clickable captcha found / click skipped: {e})")
        time.sleep(3)

        print("Navigating to search URL...")
        sb.uc_open_with_reconnect(url, reconnect_time=4)
        time.sleep(3)

        html = sb.get_page_source()
        title = sb.get_title()
        report(html, title, "bookingkai_debug_seleniumbase_uc.html", args.proxy)


if __name__ == "__main__":
    main()
