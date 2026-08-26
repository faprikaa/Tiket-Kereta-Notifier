"""Cloudflare Quick Tunnel (cloudflared) wrapper."""

from __future__ import annotations

import asyncio
import logging
import re

import httpx

URL_RE = re.compile(r"https://[a-zA-Z0-9-]+\.trycloudflare\.com")


class Tunnel:
    def __init__(self, logger: logging.Logger) -> None:
        self.logger = logger
        self.url = ""
        self._process: asyncio.subprocess.Process | None = None
        self._started = False

    async def start(self, local_url: str) -> str:
        if self._started:
            return self.url

        self._process = await asyncio.create_subprocess_exec(
            "cloudflared", "tunnel", "--url", local_url,
            stdout=asyncio.subprocess.DEVNULL,
            stderr=asyncio.subprocess.PIPE,
        )
        self.logger.info("Starting cloudflared tunnel... local_url=%s", local_url)

        url_future: asyncio.Future[str] = asyncio.get_running_loop().create_future()

        async def read_stderr() -> None:
            assert self._process is not None and self._process.stderr is not None
            async for raw_line in self._process.stderr:
                line = raw_line.decode(errors="replace").rstrip()
                self.logger.debug("cloudflared output=%s", line)

                match = URL_RE.search(line)
                if match and "api.trycloudflare.com" not in match.group(0):
                    if not url_future.done():
                        url_future.set_result(match.group(0))

                lowered = line.lower()
                if ("error" in lowered or "failed" in lowered) and ("not found" in lowered or "not installed" in lowered):
                    if not url_future.done():
                        url_future.set_exception(RuntimeError(f"cloudflared not installed: {line}"))

        reader_task = asyncio.create_task(read_stderr())

        try:
            url = await asyncio.wait_for(url_future, timeout=30)
        except asyncio.TimeoutError:
            await self.stop()
            reader_task.cancel()
            raise RuntimeError("timeout waiting for tunnel URL") from None
        except RuntimeError:
            await self.stop()
            reader_task.cancel()
            raise

        self.url = url
        self._started = True
        self.logger.info("Tunnel started public_url=%s", url)

        try:
            await self._wait_for_ready(url, timeout=30)
        except RuntimeError as e:
            self.logger.warning("Tunnel health check failed, proceeding anyway error=%s", e)

        return url

    async def _wait_for_ready(self, url: str, timeout: float) -> None:
        self.logger.info("Waiting for tunnel to be accessible...")
        await asyncio.sleep(5)  # DNS propagation

        health_url = url + "/health"
        deadline = asyncio.get_running_loop().time() + timeout
        attempt = 0

        async with httpx.AsyncClient(timeout=5.0) as client:
            while asyncio.get_running_loop().time() < deadline:
                attempt += 1
                try:
                    resp = await client.get(health_url)
                    if resp.status_code == 200:
                        self.logger.info("Tunnel is ready! attempts=%d", attempt)
                        return
                except httpx.HTTPError as e:
                    self.logger.debug("Tunnel not ready yet attempt=%d error=%s", attempt, e)
                await asyncio.sleep(1)

        raise RuntimeError(f"tunnel not accessible after {timeout}s")

    async def stop(self) -> None:
        if not self._started and self._process is None:
            return
        self.logger.info("Stopping cloudflared tunnel...")
        if self._process is not None:
            self._process.terminate()
            try:
                await asyncio.wait_for(self._process.wait(), timeout=5)
            except asyncio.TimeoutError:
                self._process.kill()
                await self._process.wait()
        self._started = False
        self.url = ""
        self.logger.info("Tunnel stopped")

    def is_running(self) -> bool:
        return self._started
