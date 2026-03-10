"""
Bypass Cloudflare untuk booking.kai.id menggunakan DrissionPage.
DrissionPage mengontrol Chromium via DevTools Protocol (CDP) — 
tanpa WebDriver, jadi tidak terdeteksi sebagai bot.

Install:
    pip install DrissionPage

Pastikan Google Chrome / Chromium terinstall di system.

Usage:
    python scripts/bypass_cloudflare.py
"""

from DrissionPage import ChromiumPage, ChromiumOptions
from urllib.parse import quote
import time
import sys

# ============================================================
# KONFIGURASI — ubah sesuai kebutuhan
# ============================================================
ORIGIN = "PSE"
DESTINATION = "LPN"
DATE = "2026-03-15"  # format YYYY-MM-DD
PROXY = ""  # kosongkan jika tidak pakai, contoh: "socks5://127.0.0.1:40000"
HEADLESS = True  # True = tanpa window, False = tampilkan browser

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
    Scrape booking.kai.id search results using DrissionPage.
    Handles Cloudflare challenge automatically.
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

    print(f"🔍 Searching: {origin} → {dest} [{date}]")
    print(f"   URL: {url}")

    # Setup browser options
    options = ChromiumOptions()
    if HEADLESS:
        options.headless()
    if proxy:
        options.set_proxy(proxy)
        print(f"   Proxy: {proxy}")

    # Disable unnecessary features for speed
    options.set_argument("--disable-images")
    options.set_argument("--no-sandbox")
    options.set_argument("--disable-dev-shm-usage")
    options.set_argument("--disable-gpu")

    page = ChromiumPage(options)

    try:
        print("   Navigating to booking.kai.id...")
        page.get(url)

        # Wait for Cloudflare challenge to resolve (if any)
        # DrissionPage handles this automatically since it's a real browser
        max_wait = 30
        waited = 0
        while waited < max_wait:
            title = page.title or ""
            html = page.html or ""

            # Check if still on Cloudflare challenge
            if "Just a moment" in title or "cf_chl_opt" in html or "challenge-platform" in html:
                print(f"   ⏳ Waiting for Cloudflare challenge... ({waited}s)")
                time.sleep(2)
                waited += 2
                continue

            # Check if page loaded
            if "booking.kai.id" in title.lower() or "data-block" in html or "list-kereta" in html:
                print(f"   ✅ Cloudflare bypassed!")
                break

            # Generic wait
            time.sleep(1)
            waited += 1

        html = page.html

        if not html:
            print("❌ Failed to get page content")
            return []

        # Check for remaining Cloudflare blocks
        if "Just a moment" in html or "cf-browser-verification" in html:
            print("❌ Cloudflare challenge not resolved")
            with open("cf_blocked.html", "w", encoding="utf-8") as f:
                f.write(html)
            print("   Response saved to cf_blocked.html")
            return []

        print(f"   Got page ({len(html)} bytes)")

        # Save raw HTML for debugging
        with open("booking_kai_output.html", "w", encoding="utf-8") as f:
            f.write(html)

        # Parse trains from DOM directly via DrissionPage
        trains = []
        blocks = page.eles(".data-block.list-kereta")

        for block in blocks:
            # Extract hidden input values
            inputs = {}
            for inp in block.eles("input[type=hidden]"):
                name = inp.attr("name") or ""
                value = inp.attr("value") or ""
                if name:
                    inputs[name] = value

            # Check availability
            availability = "AVAILABLE"
            seats_left = "1"

            habis = block.eles("a.habis")
            if habis:
                availability = "FULL"
                seats_left = "0"

            sisa_elements = block.eles("small.sisa-kursi")
            for sisa in sisa_elements:
                text = sisa.text.strip()
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
                try:
                    price = f"Rp{int(price):,}".replace(",", ".")
                except ValueError:
                    price = f"Rp{price}"

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

    finally:
        page.quit()


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
