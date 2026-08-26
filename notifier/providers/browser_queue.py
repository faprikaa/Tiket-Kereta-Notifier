"""Shared persistent-browser queue for all BookingKAI providers.

Uses nodriver — a pure-CDP browser automation library (no Selenium/WebDriver
protocol at all) — to drive a real Chromium instance against booking.kai.id.
Because there is no webdriver layer, none of the usual `navigator.webdriver`
/ CDP-detection signals Cloudflare looks for are present, so it clears the
managed challenge without needing a proxy in most cases (still headless, so
it cannot solve an interactive CAPTCHA if one is shown).

All bookingkai train configs share one BrowserQueue so requests are
serialized through a single persistent tab — this keeps Cloudflare's
`cf_clearance` cookie alive across requests instead of re-triggering a
challenge for every train.
"""

from __future__ import annotations

import asyncio
import logging
import shutil
import time
from dataclasses import dataclass, field
from pathlib import Path

import nodriver

from ..models import Train
from .bookingkai_parse import is_cloudflare_challenge, is_waiting_room, parse_trains

CHROMIUM_CANDIDATES = (
    "chromium-browser",
    "chromium",
    "google-chrome",
    "google-chrome-stable",
)


def find_chromium() -> str:
    for name in CHROMIUM_CANDIDATES:
        path = shutil.which(name)
        if path:
            return path
    return ""


@dataclass
class _Job:
    search_url: str
    future: asyncio.Future = field(default_factory=asyncio.Future)


class BrowserQueue:
    def __init__(self, logger: logging.Logger) -> None:
        self.logger = logger
        self._browser: nodriver.Browser | None = None
        self._tab: nodriver.Tab | None = None
        self._jobs: asyncio.Queue[_Job | None] = asyncio.Queue()
        self._worker_task: asyncio.Task | None = None
        self._challenge_failures = 0
        self._next_challenge_retry = 0.0

    @classmethod
    async def create(
        cls,
        logger: logging.Logger,
        proxy_url: str,
        chromium_path: str,
        user_data_dir: str,
        headless: bool,
    ) -> "BrowserQueue":
        q = cls(logger)

        chromium_bin = chromium_path or find_chromium()
        if not chromium_bin:
            raise RuntimeError("Chromium binary not found")
        logger.info("Using chromium binary: %s", chromium_bin)

        browser_args = [
            "--disable-blink-features=AutomationControlled",
            "--window-size=1920,1080",
            "--no-sandbox=True"
        ]
        if proxy_url:
            chrome_proxy = proxy_url.replace("socks5h://", "socks5://", 1)
            browser_args.append(f"--proxy-server={chrome_proxy}")
            logger.info("BookingKAI browser using proxy=%s", chrome_proxy)

        if user_data_dir:
            Path(user_data_dir).mkdir(parents=True, exist_ok=True)

        logger.info("Launching browser headless=%s", headless)
        q._browser = await nodriver.start(
            headless=headless,
            browser_executable_path=chromium_bin,
            user_data_dir=user_data_dir or None,
            browser_args=browser_args,
            lang="id-ID",
            sandbox=False,
        )
        q._tab = await q._browser.get("https://booking.kai.id/")

        logger.info("Warming up browser — visiting booking.kai.id homepage...")
        for attempt in range(15):
            await asyncio.sleep(2)
            html = await q._tab.get_content()
            if not is_cloudflare_challenge(html):
                logger.info("✅ Browser warmup complete")
                break
            logger.debug("Warmup: waiting for challenge... attempt=%d", attempt + 1)

        q._worker_task = asyncio.create_task(q._worker())
        logger.info("BookingKAI queue started headless=%s user_data_dir=%s proxy=%s", headless, user_data_dir, proxy_url)
        return q

    async def _worker(self) -> None:
        while True:
            job = await self._jobs.get()
            if job is None:
                return
            try:
                trains, method = await self._do_fetch(job.search_url)
                if not job.future.done():
                    job.future.set_result((trains, method))
            except Exception as e:  # noqa: BLE001
                if not job.future.done():
                    job.future.set_exception(e)

    async def _do_fetch(self, search_url: str) -> tuple[list[Train], str]:
        if time.time() < self._next_challenge_retry:
            wait_until = time.strftime("%H:%M:%S", time.localtime(self._next_challenge_retry))
            raise RuntimeError(f"BookingKAI challenge backoff active until {wait_until}")

        try:
            trains = await self._fetch_via_browser(search_url)
        except Exception as e:
            msg = str(e).lower()
            if "cloudflare" in msg or "captcha" in msg:
                self._record_challenge()
            raise
        self._challenge_failures = 0
        self._next_challenge_retry = 0.0
        return trains, "browser"

    def _record_challenge(self) -> None:
        self._challenge_failures += 1
        self._next_challenge_retry = time.time() + _challenge_backoff(self._challenge_failures)

    async def _fetch_via_browser(self, search_url: str) -> list[Train]:
        if self._tab is None:
            raise RuntimeError("no persistent browser tab available")

        self.logger.debug("Browser navigating (nodriver, persistent tab) url=%s", search_url)
        await asyncio.wait_for(self._tab.get(search_url), timeout=90)
        await asyncio.sleep(2)  # let the page settle (JS-rendered blocks, lazy content)

        html = await self._tab.get_content()

        # Headless mode cannot solve interactive challenges. Fail fast so the
        # queue can apply bounded backoff without blocking other jobs.
        if is_cloudflare_challenge(html):
            raise RuntimeError("blocked by Cloudflare challenge or CAPTCHA; manual intervention required")
        if is_waiting_room(html):
            raise RuntimeError("blocked by Cloudflare Waiting Room; retry later")

        return parse_trains(html)

    async def enqueue(self, search_url: str) -> tuple[list[Train], str]:
        job = _Job(search_url=search_url)
        await self._jobs.put(job)
        return await job.future

    async def close(self) -> None:
        if self._worker_task is not None:
            await self._jobs.put(None)
            await self._worker_task
        if self._browser is not None:
            self._browser.stop()
        self.logger.info("BookingKAI browser queue stopped")


def _challenge_backoff(attempt: int) -> float:
    attempt = max(attempt, 1)
    delay = 30.0
    i = 1
    while i < attempt and delay < 900.0:
        delay *= 2
        i += 1
    return min(delay, 900.0)
