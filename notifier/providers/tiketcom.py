"""Tiket.com provider — JSON API fetched via curl_cffi's Chrome TLS/JA3 impersonation.

Tiket.com fronts its API with Cloudflare Turnstile, which flags plain HTTP
clients on TLS fingerprint alone. The Go version shelled out to the
curl_chrome110 binary for this; curl_cffi provides the same libcurl-impersonate
technique as an installable Python wheel, so no external binary/setup step is
needed anymore.
"""

from __future__ import annotations

import asyncio
import logging

from curl_cffi import AsyncSession

from ..config import FlatTrainConfig
from ..models import Train
from .base import BaseProvider

API_URL = "https://www.tiket.com/ms-gateway/tix-train-search-v2/v7/train/journeys"

HEADERS = {
    "accept": "application/json, text/plain, */*",
    "x-audience": "tiket.com",
}


def _format_time(t: str) -> str:
    parts = t.split(":")
    return f"{parts[0]}:{parts[1]}" if len(parts) >= 2 else t


def _parse_trains(journeys: list[dict]) -> list[Train]:
    trains: list[Train] = []
    for journey in journeys:
        for seg in journey.get("segmentSchedules") or []:
            price = 0
            for fare in seg.get("scheduleFares") or []:
                if fare.get("paxType") == "ADULT":
                    try:
                        price = int(float(fare.get("priceAmount") or 0))
                    except (TypeError, ValueError):
                        price = 0
                    break

            seats_left = seg.get("availableSeats") or 0
            availability = "AVAILABLE" if seats_left > 0 else "FULL"

            wagon = seg.get("wagonClass") or {}
            sub = seg.get("subClass") or {}
            train_class = wagon.get("detail", "")
            if sub.get("code"):
                train_class = f"{train_class} ({sub['code']})"

            trains.append(
                Train(
                    name=seg.get("trainName", ""),
                    train_class=train_class,
                    price=price,
                    departure_time=_format_time(seg.get("departureTime", "")),
                    arrival_time=_format_time(seg.get("arrivalTime", "")),
                    availability=availability,
                    seats_left=str(seats_left),
                )
            )
    return trains


class TiketcomProvider(BaseProvider):
    provider_key = "tiketcom"
    include_class_in_notify = True
    default_interval = 300.0

    def __init__(self, logger: logging.Logger, flat: FlatTrainConfig, index: int) -> None:
        super().__init__(logger, flat, index)
        self.date_compact = flat.date_yyyymmdd()

    async def _fetch(self) -> tuple[list[Train], str]:
        if not self.origin or not self.destination:
            raise ValueError("origin and destination required")

        self.logger.info("Searching Tiket.com origin=%s dest=%s date=%s", self.origin, self.destination, self.date_compact)

        params = {
            "orig": self.origin,
            "otype": "STATION",
            "dest": self.destination,
            "dtype": "STATION",
            "ttype": "ONE_WAY",
            "ddate": self.date_compact,
            "acount": 1,
            "icount": 0,
        }

        proxies = {"https": self.proxy_url, "http": self.proxy_url} if self.proxy_url else None
        async with AsyncSession() as session:
            resp = await asyncio.wait_for(
                session.get(API_URL, params=params, headers=HEADERS, impersonate="chrome", proxies=proxies),
                timeout=60,
            )

        body = resp.json()
        code = body.get("code")
        if code != "SUCCESS":
            message = body.get("message", "")
            combined = f"{code} {message}".lower()
            if any(k in combined for k in ("turnstile", "captcha", "challenge", "ray-id", "cloudflare")):
                raise RuntimeError(f"⚠️ Tiket.com is blocked by Turnstile/Captcha! Try using proxy_url in config ({code}: {message})")
            raise RuntimeError(f"API error: {code} - {message}")

        data = body.get("data") or {}
        depart = data.get("departJourneys")
        if not depart:
            raise RuntimeError("no journey data in response")

        return _parse_trains(depart.get("journeys") or []), "chrome"
