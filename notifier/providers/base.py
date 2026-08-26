"""Shared scheduler/history/status/notify plumbing for all providers."""

from __future__ import annotations

import abc
import asyncio
import logging
import random
from typing import Awaitable, Callable

from ..config import FlatTrainConfig
from ..history import HistoryStore
from ..models import CheckResult, ProviderStatus, StatusTracker, Train
from ..utils import format_price, is_wildcard

NotifyFunc = Callable[[str], Awaitable[None]]


class BaseProvider(abc.ABC):
    """Common polling/filtering/notification behavior for a train × provider pair.

    Subclasses implement `_fetch()`, the raw single-shot search against the
    upstream source, and control per-provider notification formatting via
    `provider_key` / `include_class_in_notify`.
    """

    provider_key: str = "provider"
    include_class_in_notify: bool = False
    default_interval: float = 300.0

    def __init__(self, logger: logging.Logger, flat: FlatTrainConfig, index: int) -> None:
        self.logger = logger
        self.origin = flat.origin
        self.destination = flat.destination
        self.date = flat.date
        self.train_name = flat.name
        self.max_price = flat.max_price
        self.interval = flat.interval or self.default_interval
        self.index = index
        self.notes = flat.notes
        self.proxy_url = flat.proxy_url
        self.history = HistoryStore(100)
        self.status = StatusTracker()

    @property
    def name(self) -> str:
        return f"{self.provider_key}:{self.train_name}:{self.origin}→{self.destination}"

    @abc.abstractmethod
    async def _fetch(self) -> tuple[list[Train], str]:
        """Fetch all trains on the route. Returns (trains, fetch_method)."""

    def _extra_filter(self, trains: list[Train]) -> list[Train]:
        """Hook for provider-specific filters applied after the name filter (e.g. departure hour)."""
        return trains

    async def _search_with_method(self) -> tuple[list[Train], str]:
        trains, method = await self._fetch()
        if self.train_name and not is_wildcard(self.train_name):
            target = self.train_name.lower()
            trains = [t for t in trains if target in t.name.lower()]
        trains = self._extra_filter(trains)
        return trains, method

    async def search(self) -> list[Train]:
        """Search and filter by the configured train name (wildcard = no filter)."""
        trains, _method = await self._search_with_method()
        return trains

    async def search_all(self) -> list[Train]:
        """Return every train on the route, ignoring name/hour filters."""
        trains, _method = await self._fetch()
        return trains

    def _available(self, trains: list[Train]) -> list[Train]:
        result = []
        for t in trains:
            if not t.is_available:
                continue
            if self.max_price and t.price and t.price > self.max_price:
                continue
            result.append(t)
        return result

    def _format_notify(self, available: list[Train]) -> str:
        lines = [
            f"🚂 #{self.index} {self.train_name}",
            f"📍 {self.origin}→{self.destination} [{self.date}]",
            f"✅ Tersedia! ({len(available)} found) via {self.provider_key}",
        ]
        if self.notes:
            lines.append(f"📝 {self.notes}")
        lines.append("")
        for t in available:
            label = f"{t.name} [{t.train_class}]" if self.include_class_in_notify and t.train_class else t.name
            lines.append(f"• {label}")
            lines.append(f"  💺 {t.seats_left} seats @ {format_price(t.price)}")
        return "\n".join(lines) + "\n"

    async def start_scheduler(self, notify: NotifyFunc) -> None:
        self.logger.info("%s scheduler started interval=%ss target=%s", self.provider_key, self.interval, self.train_name)

        def jittered() -> float:
            jitter = self.interval * 0.1
            return self.interval + random.uniform(-jitter, jitter)

        while True:
            try:
                await asyncio.sleep(jittered())
            except asyncio.CancelledError:
                return

            if self.status.paused:
                continue

            self.status.record_check_start()
            try:
                trains, method = await self._search_with_method()
            except asyncio.CancelledError:
                return
            except Exception as e:  # noqa: BLE001 - provider errors must not kill the scheduler
                self.logger.error("Poll failed provider=%s route=%s→%s date=%s train=%s error=%s",
                                   self.provider_key, self.origin, self.destination, self.date, self.train_name, e)
                self.status.record_check_error(str(e))
                self.history.add(CheckResult(error=str(e)))
                continue

            available = self._available(trains)
            self.status.record_check_success(len(available) > 0)
            self.history.add(CheckResult(trains_found=len(trains), available_trains=available, method=method))

            if available:
                await notify(self._format_notify(available))

    def get_history(self, n: int) -> list[CheckResult]:
        return self.history.get_last(n)

    def get_status(self) -> ProviderStatus:
        s = self.status
        return ProviderStatus(
            start_time=s.start_time,
            total_checks=s.total_checks,
            successful_checks=s.successful_checks,
            failed_checks=s.failed_checks,
            last_check_time=s.last_check_time,
            last_check_found=s.last_check_found,
            last_check_error=s.last_check_error,
            origin=self.origin,
            destination=self.destination,
            date=self.date,
            train_name=self.train_name,
            interval=self.interval,
        )

    def set_paused(self, paused: bool) -> None:
        self.status.paused = paused

    def is_paused(self) -> bool:
        return self.status.paused
