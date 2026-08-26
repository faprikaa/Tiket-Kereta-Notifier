"""booking.kai.id provider — official KAI site, scraped via a shared headless-Chromium queue."""

from __future__ import annotations

import logging
from datetime import datetime
from urllib.parse import quote

from ..config import FlatTrainConfig
from ..models import Train
from .base import BaseProvider
from .browser_queue import BrowserQueue

MONTH_NAMES = (
    "", "Januari", "Februari", "Maret", "April", "Mei", "Juni",
    "Juli", "Agustus", "September", "Oktober", "November", "Desember",
)


def _format_date_indo(date: str) -> str:
    d = datetime.strptime(date, "%Y-%m-%d")
    return f"{d.day:02d}-{MONTH_NAMES[d.month]}-{d.year}"


def _parse_departure_hour(time_str: str) -> int | None:
    if ":" not in time_str:
        return None
    try:
        h = int(time_str.split(":", 1)[0])
    except ValueError:
        return None
    return h if 0 <= h <= 23 else None


class BookingKaiProvider(BaseProvider):
    provider_key = "bookingkai"
    include_class_in_notify = True
    default_interval = 300.0

    def __init__(self, logger: logging.Logger, flat: FlatTrainConfig, index: int, queue: BrowserQueue) -> None:
        super().__init__(logger, flat, index)
        self.min_departure_hour = flat.min_departure_hour
        self.max_departure_hour = flat.max_departure_hour
        self.queue = queue

    def _extra_filter(self, trains: list[Train]) -> list[Train]:
        if self.min_departure_hour == 0 and self.max_departure_hour == 0:
            return trains
        filtered = []
        for t in trains:
            h = _parse_departure_hour(t.departure_time)
            if h is None:
                filtered.append(t)  # unknown format: include
                continue
            if self.min_departure_hour and h < self.min_departure_hour:
                continue
            if self.max_departure_hour and h > self.max_departure_hour:
                continue
            filtered.append(t)
        return filtered

    async def _fetch(self) -> tuple[list[Train], str]:
        date_indo = _format_date_indo(self.date)
        search_url = (
            f"https://booking.kai.id/?origination={self.origin}&destination={self.destination}"
            f"&tanggal={quote(date_indo)}&adult=1&infant=0&submit=Cari+%26+Pesan+Tiket"
        )
        self.logger.debug("Enqueuing booking.kai.id request url=%s", search_url)
        trains, method = await self.queue.enqueue(search_url)
        self.logger.info(
            "BookingKAI search complete route=%s→%s date=%s total=%d method=%s",
            self.origin, self.destination, self.date, len(trains), method,
        )
        return trains, method
