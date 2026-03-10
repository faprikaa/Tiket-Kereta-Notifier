"""
Bypass Cloudflare untuk booking.kai.id tanpa browser.
Pakai curl_cffi (curl-impersonate) untuk mimic TLS fingerprint Chrome.

Install:
    pip install curl_cffi beautifulsoup4

Usage:
    python scripts/bypass_cloudflare.py
"""

from curl_cffi import requests
from bs4 import BeautifulSoup
from urllib.parse import quote
import json
import sys

# ============================================================
# KONFIGURASI — ubah sesuai kebutuhan
# ============================================================
ORIGIN = "PSE"
DESTINATION = "LPN"
DATE = "2026-03-15"  # format YYYY-MM-DD
PROXY = ""  # kosongkan jika tidak pakai proxy, contoh: "socks5h://127.0.0.1:40000"

# ============================================================

MONTH_NAMES = [
    "", "Januari", "Februari", "Maret", "April", "Mei", "Juni",
    "Juli", "Agustus", "September", "Oktober", "November", "Desember",
]


def format_date_indo(date_str: str) -> str:
    """Convert '2026-03-15' to '15-Maret-2026'."""
    parts = date_str.split("-")
    year, month, day = int(parts[0]), int(parts[1]), int(parts[2])
    return f"{day:02d}-{MONTH_NAMES[month]}-{year}"


def search_trains(origin: str, dest: str, date: str, proxy: str = "") -> list[dict]:
    """
    Scrape booking.kai.id search results.
    Returns list of train dicts.
    """
    date_indo = format_date_indo(date)

    url = (
        f"https://booking.kai.id/"
        f"?origination={origin}"
        f"&destination={dest}"
        f"&tanggal={quote(date_indo)}"
        f"&adult=1&infant=0"
        f"&submit=Cari+%26+Pesan+Tiket"
    )

    headers = {
        "accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
        "accept-language": "en-US,en;q=0.8",
        "cache-control": "no-cache",
        "pragma": "no-cache",
        "sec-ch-ua": '"Not:A-Brand";v="99", "Brave";v="133", "Chromium";v="133"',
        "sec-ch-ua-mobile": "?0",
        "sec-ch-ua-platform": '"Windows"',
        "sec-fetch-dest": "document",
        "sec-fetch-mode": "navigate",
        "sec-fetch-site": "same-origin",
        "sec-fetch-user": "?1",
        "upgrade-insecure-requests": "1",
        "referer": "https://booking.kai.id/",
    }

    kwargs = {
        "headers": headers,
        "impersonate": "chrome110",
        "timeout": 60,
        "allow_redirects": True,
    }

    if proxy:
        kwargs["proxies"] = {"https": proxy, "http": proxy}

    print(f"🔍 Searching: {origin} → {dest} [{date}]")
    print(f"   URL: {url}")
    if proxy:
        print(f"   Proxy: {proxy}")

    resp = requests.get(url, **kwargs)

    # Check for Cloudflare blocks
    html = resp.text
    if resp.status_code == 403 or "Just a moment" in html or "cf-browser-verification" in html:
        print(f"❌ Blocked by Cloudflare challenge (status {resp.status_code})")
        # Save response for debugging
        with open("cf_blocked.html", "w", encoding="utf-8") as f:
            f.write(html)
        print("   Response saved to cf_blocked.html")
        return []

    if "cf_chl_opt" in html or "challenge-platform" in html:
        print("❌ Blocked by Cloudflare JS challenge")
        with open("cf_blocked.html", "w", encoding="utf-8") as f:
            f.write(html)
        print("   Response saved to cf_blocked.html")
        return []

    if resp.status_code != 200:
        print(f"❌ HTTP {resp.status_code}")
        return []

    print(f"✅ Got response ({len(html)} bytes)")

    # Save raw HTML for debugging
    with open("booking_kai_output.html", "w", encoding="utf-8") as f:
        f.write(html)

    # Parse trains
    return parse_trains(html)


def parse_trains(html: str) -> list[dict]:
    """Parse train data dari HTML booking.kai.id."""
    soup = BeautifulSoup(html, "html.parser")
    trains = []

    # Find all train blocks: div.data-block.list-kereta
    blocks = soup.find_all("div", class_=lambda c: c and "data-block" in c and "list-kereta" in c)

    for block in blocks:
        # Extract hidden input values
        inputs = {}
        for inp in block.find_all("input", type="hidden"):
            name = inp.get("name", "")
            value = inp.get("value", "")
            if name:
                inputs[name] = value

        # Check availability
        availability = "AVAILABLE"
        seats_left = "1"

        # Check for "habis" class on <a> tags
        habis_link = block.find("a", class_=lambda c: c and "habis" in c)
        if habis_link:
            availability = "FULL"
            seats_left = "0"

        # Check sisa-kursi text
        sisa = block.find("small", class_=lambda c: c and "sisa-kursi" in c)
        if sisa:
            text = sisa.get_text(strip=True)
            if text == "Habis":
                availability = "FULL"
                seats_left = "0"
            elif text == "Tersedia":
                availability = "AVAILABLE"
                seats_left = "1"

        # Build class string
        class_str = inputs.get("kelas_gerbong", "")
        subkelas = inputs.get("subkelas", "")
        if subkelas:
            class_str += f" ({subkelas})"

        # Format price
        price = inputs.get("harga", "")
        if price:
            price = f"Rp{int(price):,}".replace(",", ".")

        trains.append({
            "name": inputs.get("kereta", "N/A"),
            "class": class_str,
            "price": price,
            "departure": inputs.get("timestart", ""),
            "arrival": inputs.get("timeend", ""),
            "availability": availability,
            "seats_left": seats_left,
        })

    return trains


def print_results(trains: list[dict]):
    """Print train results as a formatted table."""
    if not trains:
        print("\n⚠️  Tidak ada kereta ditemukan.")
        return

    print(f"\n🚂 Ditemukan {len(trains)} kereta:\n")
    print(f"{'No':<4} {'Kereta':<20} {'Kelas':<15} {'Berangkat':<10} {'Tiba':<10} {'Harga':<15} {'Status':<10}")
    print("-" * 84)

    for i, t in enumerate(trains, 1):
        status = "✅" if t["availability"] == "AVAILABLE" else "❌"
        print(f"{i:<4} {t['name']:<20} {t['class']:<15} {t['departure']:<10} {t['arrival']:<10} {t['price']:<15} {status}")


def main():
    trains = search_trains(ORIGIN, DESTINATION, DATE, PROXY)
    print_results(trains)


if __name__ == "__main__":
    main()
