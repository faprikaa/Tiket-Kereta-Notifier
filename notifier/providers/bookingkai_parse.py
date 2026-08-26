"""HTML parsing for booking.kai.id search result pages."""

from __future__ import annotations

from bs4 import BeautifulSoup

from ..models import Train


def is_cloudflare_challenge(html: str) -> bool:
    return any(
        marker in html
        for marker in ("cf_chl_opt", "challenge-platform", "Just a moment", "cf-browser-verification")
    )


def is_waiting_room(html: str) -> bool:
    return "cfwaitingroom" in html or "Waiting Room" in html


def _format_price(raw: str) -> int:
    digits = "".join(c for c in raw if c.isdigit())
    return int(digits) if digits else 0


def parse_trains(html: str) -> list[Train]:
    soup = BeautifulSoup(html, "html.parser")
    trains: list[Train] = []

    for block in soup.select("div.data-block.list-kereta"):
        inputs: dict[str, str] = {}
        for inp in block.select("input[type=hidden]"):
            name = inp.get("name")
            if name:
                inputs[name] = inp.get("value", "") or ""

        availability = "AVAILABLE"
        seats_left = "1"

        if block.select("a.habis"):
            availability = "FULL"
            seats_left = "0"

        for sisa in block.select("small.sisa-kursi"):
            text = sisa.get_text(strip=True)
            if text == "Habis":
                availability = "FULL"
                seats_left = "0"
            elif text == "Tersedia":
                availability = "AVAILABLE"
                seats_left = "1"

        train_class = inputs.get("kelas_gerbong", "")
        subkelas = inputs.get("subkelas", "")
        if subkelas:
            train_class = f"{train_class} ({subkelas})"

        name = inputs.get("kereta", "")
        if not name:
            continue

        trains.append(
            Train(
                name=name,
                train_class=train_class,
                price=_format_price(inputs.get("harga", "")),
                departure_time=inputs.get("timestart", ""),
                arrival_time=inputs.get("timeend", ""),
                availability=availability,
                seats_left=seats_left,
            )
        )

    return trains
