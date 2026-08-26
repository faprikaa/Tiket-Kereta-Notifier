"""Thread/task-safe (within one asyncio loop) history storage for check results."""

from __future__ import annotations

from collections import deque

from .models import CheckResult


class HistoryStore:
    """Keeps the last `max_size` check results, newest first."""

    def __init__(self, max_size: int = 100) -> None:
        self._results: deque[CheckResult] = deque(maxlen=max_size)

    def add(self, result: CheckResult) -> None:
        self._results.appendleft(result)

    def get_last(self, n: int) -> list[CheckResult]:
        if n <= 0 or n > len(self._results):
            n = len(self._results)
        return list(self._results)[:n]

    def count(self) -> int:
        return len(self._results)
