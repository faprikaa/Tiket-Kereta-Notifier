"""Shared data model for train search results and provider status."""

from __future__ import annotations

import time
from dataclasses import dataclass, field


@dataclass
class Train:
    """A standardized train search result."""

    name: str
    departure_time: str = ""
    arrival_time: str = ""
    train_class: str = ""
    price: int = 0  # Rupiah, 0 = unknown
    availability: str = "FULL"  # "AVAILABLE" or "FULL"
    seats_left: str = "0"  # e.g. "50", "0" — kept as string since some providers only know yes/no

    @property
    def is_available(self) -> bool:
        return self.availability == "AVAILABLE" or (self.seats_left not in ("", "0"))


@dataclass
class CheckResult:
    """A single check result, kept for the /history command."""

    timestamp: float = field(default_factory=time.time)
    trains_found: int = 0
    available_trains: list[Train] = field(default_factory=list)
    error: str = ""
    method: str = ""


@dataclass
class ProviderStatus:
    start_time: float
    total_checks: int
    successful_checks: int
    failed_checks: int
    last_check_time: float
    last_check_found: bool
    last_check_error: str
    origin: str
    destination: str
    date: str
    train_name: str
    interval: float  # seconds


class StatusTracker:
    """Tracks check counters and pause state for a single provider.

    Provider scheduler loops run as a single asyncio task each, so plain
    attribute mutation is safe without locks (no cross-coroutine interleaving
    within a single update).
    """

    def __init__(self) -> None:
        self.start_time = time.time()
        self.total_checks = 0
        self.successful_checks = 0
        self.failed_checks = 0
        self.last_check_time: float = 0.0
        self.last_check_found = False
        self.last_check_error = ""
        self.paused = False

    def record_check_start(self) -> None:
        self.total_checks += 1
        self.last_check_time = time.time()

    def record_check_success(self, found: bool) -> None:
        self.successful_checks += 1
        self.last_check_found = found
        self.last_check_error = ""

    def record_check_error(self, err: str) -> None:
        self.failed_checks += 1
        self.last_check_found = False
        self.last_check_error = err
