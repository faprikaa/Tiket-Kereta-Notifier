#!/usr/bin/env python3
"""Offline regression check: python scripts/test_telegram_security.py."""

import asyncio
import logging
import sys
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from notifier import app
from notifier.telegram_client import Bot, TelegramClient
from notifier.webhook_server import WebhookServer


async def main() -> None:
    logger = logging.getLogger("security-check")
    telegram = TelegramClient("test-token", "123", logger)
    try:
        bot = Bot(telegram, logger)
        handler = AsyncMock()
        bot.register_command("/toggle", handler)
        await bot.dispatch("456", "/toggle 1")
        handler.assert_not_awaited()
        await bot.dispatch("123", "/toggle 1")
        handler.assert_awaited_once_with("123", "1")
        handler.reset_mock()

        server = WebhookServer(8080, bot, logger)
        update = {"message": {"chat": {"id": 123}, "text": "/toggle 1"}}
        header = "X-Telegram-Bot-Api-Secret-Token"
        for headers in ({}, {header: "wrong"}, {header: "é"}):
            request = SimpleNamespace(headers=headers, json=AsyncMock(return_value=update))
            response = await server._handle_webhook(request)
            assert response.status == 403
            request.json.assert_not_awaited()
        handler.assert_not_awaited()

        for payload in ([], {"message": []}, {"message": {"chat": [], "text": 7}}):
            request = SimpleNamespace(headers={header: server.secret_token}, json=AsyncMock(return_value=payload))
            response = await server._handle_webhook(request)
            assert response.status == 400

        request = SimpleNamespace(headers={header: server.secret_token}, json=AsyncMock(return_value=update))
        response = await server._handle_webhook(request)
        assert response.status == 200
        await server.stop()
        handler.assert_awaited_once_with("123", "1")

        response = SimpleNamespace(json=lambda: {"ok": True})
        with patch.object(telegram._client, "post", new=AsyncMock(return_value=response)) as post:
            await telegram.set_webhook("https://example.test/webhook", server.secret_token)
            assert post.call_args.kwargs["json"]["secret_token"] == server.secret_token
        assert WebhookServer(8080, bot, logger).secret_token != server.secret_token

        shutdown = asyncio.Event()
        shutdown.set()
        cfg = SimpleNamespace(webhook=SimpleNamespace(enabled=False))
        with patch.object(telegram, "delete_webhook", new=AsyncMock()) as delete, \
             patch.object(telegram, "send_message", new=AsyncMock()):
            await app.run_bot(logger, cfg, telegram, bot, 1, shutdown)
            delete.assert_awaited_once()
        await asyncio.sleep(0)  # Let the cancelled polling task finish.
    finally:
        await telegram.close()
    print("Telegram security checks passed")


if __name__ == "__main__":
    asyncio.run(main())
