"""Shared glue for the scripts/test_bookingkai_*.py strategy scripts.

Not a strategy itself — every test_bookingkai_*.py script tries a different
browser/library/technique against Cloudflare on booking.kai.id, but they all
need the same URL-building, CLI args, and "is this page blocked?" reporting.
Keeping that here means each strategy script only contains the part that's
actually different: how it drives (or doesn't drive) a browser.

Run any strategy script with --help to see its specific options; all of them
share at least --origin, --dest, --date, --proxy, --no-headless.
"""

from __future__ import annotations

import argparse
import shutil
import sys
from pathlib import Path
from urllib.parse import quote

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from notifier.providers.bookingkai import MONTH_NAMES  # noqa: E402
from notifier.providers.bookingkai_parse import (  # noqa: E402
    extract_net_error,
    is_cloudflare_challenge,
    is_navigation_error,
    is_waiting_room,
    parse_trains,
)
from notifier.providers.browser_queue import CHROMIUM_CANDIDATES  # noqa: E402


def find_chromium() -> str:
    for name in CHROMIUM_CANDIDATES:
        path = shutil.which(name)
        if path:
            return path
    return ""


def build_search_url(origin: str, dest: str, date: str) -> str:
    year, month, day = date.split("-")
    date_indo = f"{int(day):02d}-{MONTH_NAMES[int(month)]}-{int(year)}"
    return (
        f"https://booking.kai.id/?origination={origin}&destination={dest}"
        f"&tanggal={quote(date_indo)}&adult=1&infant=0&submit=Cari+%26+Pesan+Tiket"
    )


def common_parser(description: str) -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description=description)
    p.add_argument("--origin", required=True)
    p.add_argument("--dest", required=True)
    p.add_argument("--date", required=True, help="YYYY-MM-DD")
    p.add_argument("--proxy", default="", help="e.g. socks5://127.0.0.1:40000 (omit to go direct)")
    p.add_argument("--no-headless", action="store_true", help="Show the browser window (needs a display or Xvfb)")
    return p


def report(html: str, title: str, debug_file: str, proxy: str = "") -> None:
    """Save the page and print a verdict, using the same detectors the real provider uses."""
    Path(debug_file).write_text(html, encoding="utf-8")
    print(f"Page title: {title!r}")
    print(f"Saved page to {debug_file} ({len(html)} bytes)")

    if is_navigation_error(html):
        print(f"RESULT: navigation failed — {extract_net_error(html)}")
        if proxy:
            print("        (check that the proxy is actually reachable from this host)")
    elif is_cloudflare_challenge(html):
        print("RESULT: BLOCKED by Cloudflare (JS challenge or WAF block page)")
    elif is_waiting_room(html):
        print("RESULT: BLOCKED by Cloudflare Waiting Room")
    else:
        trains = parse_trains(html)
        print(f"RESULT: OK — {len(trains)} trains parsed")
        for t in trains:
            print(f"  - {t.name} [{t.train_class}] {t.departure_time}->{t.arrival_time} "
                  f"seats={t.seats_left} price={t.price} avail={t.availability}")


def missing_dependency(package: str, install_cmd: str) -> "SystemExit":
    return SystemExit(
        f"'{package}' is not installed.\n"
        f"Install it with:\n  {install_cmd}\n"
        "(this is an experimental/optional dependency — see scripts/requirements-strategies.txt)"
    )
