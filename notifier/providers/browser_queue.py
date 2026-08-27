"""Shared fetch queue for all BookingKAI providers.

Two-stage strategy against Cloudflare on booking.kai.id, cheapest first:

1. **curl_cffi impersonating a real Chrome/Safari** — no browser at all. It matches a
   real Chrome's TLS/JA3 fingerprint and HTTP/2 frame ordering, which is what
   Cloudflare's first-pass bot check keys off. Measured against the live site
   this returns the full 25-train result page in well under a second, with no
   browser process and no hundreds of MB of RAM.
2. **Camoufox** — a hardened Firefox that patches fingerprint surfaces
   (canvas, WebGL, fonts, audio, screen/hardware, timezone) at the C++ level
   rather than via injected JS. Slower and heavy, so it is launched lazily on
   the first stage-1 failure and then kept alive.

A strategy sweep over 11 approaches (scripts/test_bookingkai_*.py) found only
these two clear the site; nodriver, plain/patched Playwright across all three
engines, and DrissionPage all got the WAF block page.

All bookingkai train configs share one queue so requests are serialized —
this keeps Cloudflare's `cf_clearance` cookie alive across requests instead of
re-triggering a challenge for every train.
"""

from __future__ import annotations

import asyncio
import logging
import random
import shutil
import time
from dataclasses import dataclass, field

from curl_cffi import requests as cffi_requests

from ..models import Train
from .bookingkai_parse import (
    extract_net_error,
    is_cloudflare_challenge,
    is_navigation_error,
    is_waiting_room,
    parse_trains,
)

# Impersonation targets verified to clear booking.kai.id — all three returned
# the same byte-identical 200 result page through the proxy. One is picked at
# random per request. edge101 and safari15_5 are deliberately excluded: both
# get a hard 403 WAF page from the same IP.
IMPERSONATE_POOL = ("chrome124", "chrome120", "safari17_2_ios")

CHROMIUM_CANDIDATES = (
    # Kept for scripts/test_bookingkai_*.py, which still drive Chromium
    # directly. The queue itself no longer launches Chromium.
    "google-chrome-stable",
    "google-chrome",
    "chromium",
    "chromium-browser",
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
        self._proxy_url = ""
        self._headless = True
        self._camoufox_cm = None
        self._browser = None
        self._page = None
        self._browser_lock = asyncio.Lock()
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
        # chromium_path/user_data_dir are accepted but unused: stage 1 needs no
        # browser and stage 2 is Firefox-based. Kept so config and callers
        # don't have to change.
        q = cls(logger)
        q._proxy_url = proxy_url
        q._headless = headless
        q._worker_task = asyncio.create_task(q._worker())
        logger.info(
            "BookingKAI queue started strategy=curl_cffi(%s)->camoufox proxy=%s",
            "|".join(IMPERSONATE_POOL), proxy_url or "(none)",
        )
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
            trains = await asyncio.to_thread(self._fetch_via_curl, search_url)
            method = "curl_cffi"
        except Exception as e:  # noqa: BLE001
            self.logger.info("BookingKAI curl_cffi stage failed (%s); falling back to camoufox", e)
            try:
                trains = await self._fetch_via_browser(search_url)
                method = "camoufox"
            except Exception as e2:
                msg = str(e2).lower()
                if "cloudflare" in msg or "captcha" in msg:
                    self._record_challenge()
                raise

        self._challenge_failures = 0
        self._next_challenge_retry = 0.0
        return trains, method

    def _record_challenge(self) -> None:
        self._challenge_failures += 1
        self._next_challenge_retry = time.time() + _challenge_backoff(self._challenge_failures)

    # --- stage 1: no browser ------------------------------------------------

    def _fetch_via_curl(self, search_url: str) -> list[Train]:
        """Blocking; run via asyncio.to_thread. Raises if the page isn't usable."""
        proxies = {"http": self._proxy_url, "https": self._proxy_url} if self._proxy_url else None
        impersonate = random.choice(IMPERSONATE_POOL)
        resp = cffi_requests.get(
            search_url,
            impersonate=impersonate,
            proxies=proxies,
            timeout=30,
            headers={
                "Accept-Language": "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7",
                "Referer": "https://booking.kai.id/",
            },
        )
        html = resp.text
        if resp.status_code != 200:
            raise RuntimeError(f"HTTP {resp.status_code} (impersonate={impersonate})")
        _raise_if_blocked(html)

        trains = parse_trains(html)
        if not trains:
            # Ambiguous: could be a genuinely empty day, or a page whose markup
            # stopped matching. Escalate to the browser rather than reporting
            # "no seats" on what might be a parsing miss.
            raise RuntimeError(f"0 trains parsed (html_len={len(html)})")
        return trains

    # --- stage 2: camoufox --------------------------------------------------

    async def _ensure_browser(self):
        async with self._browser_lock:
            if self._page is not None:
                return self._page
            try:
                from camoufox.async_api import AsyncCamoufox
            except ImportError as e:
                raise RuntimeError(
                    "camoufox is not installed, so the BookingKAI fallback is unavailable.\n"
                    "Install it with: pip install -U 'camoufox[geoip]' && python -m camoufox fetch"
                ) from e

            kwargs = {
                "headless": self._headless,
                "os": ("windows", "macos", "linux"),
                "locale": "id-ID",
                "geoip": True,
            }
            if self._proxy_url:
                kwargs["proxy"] = {"server": self._proxy_url.replace("socks5h://", "socks5://", 1)}

            self.logger.info("Launching camoufox fallback headless=%s", self._headless)
            self._camoufox_cm = AsyncCamoufox(**kwargs)
            self._browser = await self._camoufox_cm.__aenter__()
            self._page = await self._browser.new_page()

            self.logger.info("Camoufox warmup — visiting booking.kai.id homepage...")
            await self._page.goto("https://booking.kai.id/", timeout=60_000)
            for attempt in range(15):
                await asyncio.sleep(2)
                if not is_cloudflare_challenge(await self._page.content()):
                    self.logger.info("✅ Camoufox warmup complete")
                    break
                self.logger.debug("Warmup: waiting for challenge... attempt=%d", attempt + 1)
            return self._page

    async def _fetch_via_browser(self, search_url: str) -> list[Train]:
        page = await self._ensure_browser()

        self.logger.debug("Camoufox navigating url=%s", search_url)
        await page.goto(search_url, timeout=90_000)
        await asyncio.sleep(3)  # let the page settle (JS-rendered blocks, lazy content)

        html = await page.content()
        _raise_if_blocked(html)

        trains = parse_trains(html)
        if not trains:
            # Zero results can be legitimate (no service that day), but can also
            # mean the page structure silently didn't match our selectors. Log
            # enough to tell the two apart without dumping the full HTML.
            self.logger.warning(
                "BookingKAI: 0 trains parsed url=%s title=%r html_len=%d",
                search_url, await page.title(), len(html),
            )
        return trains

    # --- plumbing -----------------------------------------------------------

    async def enqueue(self, search_url: str) -> tuple[list[Train], str]:
        job = _Job(search_url=search_url)
        await self._jobs.put(job)
        return await job.future

    async def close(self) -> None:
        if self._worker_task is not None:
            await self._jobs.put(None)
            await self._worker_task
        if self._camoufox_cm is not None:
            await self._camoufox_cm.__aexit__(None, None, None)
        self.logger.info("BookingKAI queue stopped")


def _raise_if_blocked(html: str) -> None:
    if is_cloudflare_challenge(html):
        raise RuntimeError("blocked by Cloudflare challenge or CAPTCHA")
    if is_waiting_room(html):
        raise RuntimeError("blocked by Cloudflare Waiting Room; retry later")
    if is_navigation_error(html):
        raise RuntimeError(
            f"failed to reach booking.kai.id ({extract_net_error(html)}); "
            "if a proxy_url is configured, verify it is actually reachable from this host"
        )


def _challenge_backoff(attempt: int) -> float:
    attempt = max(attempt, 1)
    delay = 30.0
    i = 1
    while i < attempt and delay < 900.0:
        delay *= 2
        i += 1
    return min(delay, 900.0)
