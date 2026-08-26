"""Telegram webhook HTTP server (aiohttp), used as an alternative to long-polling."""

from __future__ import annotations

import asyncio
import logging

from aiohttp import web

from .telegram_client import Bot


class WebhookServer:
    def __init__(self, port: int, bot: Bot, logger: logging.Logger) -> None:
        self.port = port
        self.bot = bot
        self.logger = logger
        self._runner: web.AppRunner | None = None
        self._background_tasks: set[asyncio.Task] = set()

    async def start(self) -> None:
        app = web.Application()
        app.router.add_post("/webhook", self._handle_webhook)
        app.router.add_get("/health", self._handle_health)
        app.router.add_get("/health/", self._handle_health)

        self._runner = web.AppRunner(app)
        await self._runner.setup()
        site = web.TCPSite(self._runner, "0.0.0.0", self.port)
        await site.start()
        self.logger.info("Webhook server started port=%d", self.port)

    async def stop(self) -> None:
        if self._background_tasks:
            await asyncio.gather(*self._background_tasks, return_exceptions=True)
        if self._runner is not None:
            self.logger.info("Stopping webhook server...")
            await self._runner.cleanup()

    async def _handle_health(self, request: web.Request) -> web.Response:
        return web.json_response({"status": "ok"})

    async def _handle_webhook(self, request: web.Request) -> web.Response:
        try:
            update = await request.json()
        except ValueError as e:
            self.logger.error("Failed to parse webhook update error=%s", e)
            return web.Response(status=400, text="Bad request")

        self.logger.debug("Received webhook update body=%s", update)

        # Acknowledge immediately — Telegram expects a fast response. The
        # command handler runs as a detached task so it's never cancelled by
        # the HTTP response cycle.
        task = asyncio.create_task(self._process_update(update))
        self._background_tasks.add(task)
        task.add_done_callback(self._background_tasks.discard)

        return web.Response(status=200)

    async def _process_update(self, update: dict) -> None:
        message = update.get("message")
        if not message:
            return
        chat_id = str(message.get("chat", {}).get("id", ""))
        text = message.get("text")
        if not text:
            return

        self.logger.info("Processing webhook message chat_id=%s text=%s", chat_id, text)
        await self.bot.dispatch(chat_id, text)
