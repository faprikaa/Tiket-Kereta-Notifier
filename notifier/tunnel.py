"""Cloudflare Quick Tunnel (cloudflared) wrapper."""

from __future__ import annotations

import asyncio
import datetime as dt
import logging
import os
import re
from pathlib import Path
from typing import TextIO

import httpx

URL_RE = re.compile(r"https://[a-zA-Z0-9-]+\.trycloudflare\.com")

# cloudflared writes structured lines like: 2026-09-09T12:00:00Z ERR failed to ...
ERROR_RE = re.compile(r"\b(ERR|FTL|PNC)\b|level=(error|fatal|panic)", re.IGNORECASE)
WARN_RE = re.compile(r"\bWRN\b|level=warn", re.IGNORECASE)
# Fatal startup problems worth failing fast on instead of waiting out the timeout.
FATAL_RE = re.compile(
    r"(executable file not found|no such file or directory|command not found|"
    r"failed to parse config|error parsing YAML|unknown flag)",
    re.IGNORECASE,
)

# A quick tunnel's DNS record is published after cloudflared registers its edge
# connection — cloudflared's own banner says "it may take some time to be
# reachable". Until then the hostname is NXDOMAIN everywhere, Telegram included.
# Kept short on purpose: this probe is only advisory, and waiting on it delays
# setWebhook, whose retries do the real waiting from Telegram's own resolver.
READY_TIMEOUT = float(os.environ.get("CLOUDFLARED_READY_TIMEOUT", "60"))

PROJECT_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_CONFIG = PROJECT_ROOT / "cloudflared.yml"
DEFAULT_LOG_DIR = PROJECT_ROOT / "logs"


def config_path() -> Path | None:
    """Config file cloudflared should use, or None when there is none to pass."""
    override = os.environ.get("CLOUDFLARED_CONFIG")
    path = Path(override).expanduser() if override else DEFAULT_CONFIG
    if not path.is_absolute():
        path = (PROJECT_ROOT / path).resolve()
    return path if path.is_file() else None


def log_dir() -> Path:
    override = os.environ.get("CLOUDFLARED_LOG_DIR")
    path = Path(override).expanduser() if override else DEFAULT_LOG_DIR
    if not path.is_absolute():
        path = (PROJECT_ROOT / path).resolve()
    return path


class HourlyLogWriter:
    """Appends cloudflared output to logs/cloudflared-YYYYMMDD-HH.log.

    The filename is recomputed per line, so the file rolls over on the hour
    without any restart or scheduler.
    """

    def __init__(self, directory: Path, logger: logging.Logger) -> None:
        self.directory = directory
        self.logger = logger
        self._file: TextIO | None = None
        self._stamp = ""
        self._disabled = False
        self._last_path: Path | None = None

    @property
    def path(self) -> Path | None:
        """Newest log file written, kept after close so errors can point at it."""
        return self._last_path

    def write(self, line: str) -> None:
        handle = self._handle_for(dt.datetime.now())
        if handle is None:
            return
        try:
            handle.write(f"{dt.datetime.now().isoformat(timespec='seconds')} {line}\n")
            handle.flush()
        except OSError as e:
            self._disable(e)

    def _handle_for(self, now: dt.datetime) -> TextIO | None:
        if self._disabled:
            return None
        stamp = now.strftime("%Y%m%d-%H")
        if self._file is not None and stamp == self._stamp:
            return self._file
        try:
            self.directory.mkdir(parents=True, exist_ok=True)
            new_file = open(self.directory / f"cloudflared-{stamp}.log", "a", encoding="utf-8")
        except OSError as e:
            self._disable(e)
            return None
        self.close()
        self._file = new_file
        self._stamp = stamp
        self._last_path = Path(new_file.name)
        self.logger.info("cloudflared log file=%s", new_file.name)
        return new_file

    def _disable(self, error: OSError) -> None:
        self._disabled = True
        self.logger.warning("cloudflared file logging disabled dir=%s error=%s", self.directory, error)
        self.close()

    def close(self) -> None:
        if self._file is not None:
            try:
                self._file.close()
            except OSError:
                pass
            self._file = None
            self._stamp = ""


class Tunnel:
    def __init__(self, logger: logging.Logger) -> None:
        self.logger = logger
        self.url = ""
        self.ready = False
        self.ready_error = ""
        self.error_count = 0
        self.last_error = ""
        self._process: asyncio.subprocess.Process | None = None
        self._started = False
        self._reader_task: asyncio.Task | None = None
        self._writer = HourlyLogWriter(log_dir(), logger)

    @property
    def log_file(self) -> str:
        path = self._writer.path
        return str(path) if path is not None else ""

    async def start(self, local_url: str) -> str:
        if self._started:
            return self.url

        args = ["tunnel"]
        cfg = config_path()
        if cfg is not None:
            args += ["--config", str(cfg)]
            self.logger.info("cloudflared config=%s", cfg)
        else:
            self.logger.warning("cloudflared config not found, using built-in defaults expected=%s", DEFAULT_CONFIG)
        args += ["--url", local_url]

        self._process = await asyncio.create_subprocess_exec(
            "cloudflared",
            *args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.STDOUT,
        )
        self.logger.info("Starting cloudflared tunnel... local_url=%s", local_url)

        url_future: asyncio.Future[str] = asyncio.get_running_loop().create_future()

        async def read_output() -> None:
            assert self._process is not None and self._process.stdout is not None
            async for raw_line in self._process.stdout:
                line = raw_line.decode(errors="replace").rstrip()
                if not line:
                    continue
                self._writer.write(line)

                if ERROR_RE.search(line):
                    self.error_count += 1
                    self.last_error = line
                    self.logger.error("cloudflared %s", line)
                elif WARN_RE.search(line):
                    self.logger.warning("cloudflared %s", line)
                else:
                    self.logger.debug("cloudflared output=%s", line)

                match = URL_RE.search(line)
                if match and "api.trycloudflare.com" not in match.group(0):
                    if not url_future.done():
                        url_future.set_result(match.group(0))

                if FATAL_RE.search(line) and not url_future.done():
                    url_future.set_exception(RuntimeError(f"cloudflared failed to start: {line}"))

            if not url_future.done():
                url_future.set_exception(RuntimeError("cloudflared exited before printing a tunnel URL"))

        self._reader_task = asyncio.create_task(read_output())

        try:
            url = await asyncio.wait_for(asyncio.shield(url_future), timeout=30)
        except asyncio.TimeoutError:
            await self.stop()
            raise RuntimeError(f"timeout waiting for tunnel URL (see {self.log_file or 'cloudflared output'})") from None
        except RuntimeError:
            await self.stop()
            raise

        self.url = url
        self._started = True
        self.logger.info("Tunnel started public_url=%s log_file=%s", url, self.log_file)

        self.ready = await self._wait_for_ready(url, timeout=READY_TIMEOUT)

        return url

    async def _wait_for_ready(self, url: str, timeout: float) -> bool:
        """Probe the tunnel from outside. False means unproven, not necessarily broken.

        A quick tunnel is often reachable a while after cloudflared registers its
        connection, and the probe itself can fail for reasons that don't affect
        Telegram (egress filtering, DNS caching). So the caller keeps going and
        this only reports what it saw.
        """
        self.logger.info("Waiting for tunnel to be accessible... url=%s timeout=%ss", url, timeout)
        await asyncio.sleep(5)  # DNS propagation

        health_url = url + "/health"
        loop = asyncio.get_running_loop()
        deadline = loop.time() + timeout
        attempt = 0
        last_error = ""
        reported = False

        # trust_env=False: a host-wide HTTP(S)_PROXY/ALL_PROXY must not reroute
        # the one request whose whole point is to test the public path.
        async with httpx.AsyncClient(timeout=10.0, trust_env=False, follow_redirects=True) as client:
            while loop.time() < deadline:
                attempt += 1
                try:
                    resp = await client.get(health_url)
                    if resp.status_code == 200:
                        self.logger.info("Tunnel is ready! attempts=%d", attempt)
                        return True
                    last_error = f"HTTP {resp.status_code}"
                except httpx.HTTPError as e:
                    last_error = f"{type(e).__name__}: {e}"
                self.logger.debug("Tunnel not ready yet attempt=%d error=%s", attempt, last_error)

                # Say something once instead of going quiet for minutes, but keep
                # waiting — DNS for a fresh quick tunnel can take a while.
                if not reported and attempt >= 5:
                    reported = True
                    self.logger.info(
                        "Tunnel not reachable yet, still waiting url=%s error=%s", health_url, last_error
                    )

                # Backoff: hammering an NXDOMAIN every second only refreshes the
                # resolver's negative cache entry.
                await asyncio.sleep(min(15.0, 2.0 * attempt))

        self.ready_error = last_error or "no response"
        self.logger.warning(
            "Tunnel health probe failed url=%s attempts=%d last_error=%s log_file=%s "
            "(tunnel process still running; continuing anyway)",
            health_url, attempt, self.ready_error, self.log_file,
        )
        return False

    async def stop(self) -> None:
        if not self._started and self._process is None:
            return
        self.logger.info("Stopping cloudflared tunnel...")
        if self._process is not None:
            if self._process.returncode is None:
                self._process.terminate()
                try:
                    await asyncio.wait_for(self._process.wait(), timeout=5)
                except asyncio.TimeoutError:
                    self._process.kill()
                    await self._process.wait()
            self._process = None
        if self._reader_task is not None:
            self._reader_task.cancel()
            await asyncio.gather(self._reader_task, return_exceptions=True)
            self._reader_task = None
        self._writer.close()
        self._started = False
        self.url = ""
        self.ready = False
        self.logger.info("Tunnel stopped errors_seen=%d", self.error_count)

    def is_running(self) -> bool:
        return self._started
