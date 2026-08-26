"""Minimal async Telegram Bot API client: send/receive messages, webhook management."""

from __future__ import annotations

import asyncio
import logging
import time
from datetime import datetime

import httpx

MAX_MESSAGE_LENGTH = 4096


class RateLimiter:
    """Simple token-bucket limiter: `rate` tokens/sec, burst capacity `burst`."""

    def __init__(self, rate: float, burst: int) -> None:
        self._rate = rate
        self._capacity = burst
        self._tokens = float(burst)
        self._updated = time.monotonic()
        self._lock = asyncio.Lock()

    async def acquire(self) -> None:
        async with self._lock:
            while True:
                now = time.monotonic()
                self._tokens = min(self._capacity, self._tokens + (now - self._updated) * self._rate)
                self._updated = now
                if self._tokens >= 1:
                    self._tokens -= 1
                    return
                await asyncio.sleep((1 - self._tokens) / self._rate)


class TelegramClient:
    def __init__(self, token: str, chat_id: str, logger: logging.Logger) -> None:
        self.token = token
        self.chat_id = chat_id
        self.logger = logger
        self.limiter = RateLimiter(rate=2, burst=4)
        self.last_update_id = 0
        self._client = httpx.AsyncClient(timeout=10.0)

    def _url(self, method: str) -> str:
        return f"https://api.telegram.org/bot{self.token}/{method}"

    async def close(self) -> None:
        await self._client.aclose()

    async def send_message(self, text: str, chat_id: str | None = None) -> bool:
        target = chat_id or self.chat_id
        if not target:
            return False

        timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S WIB")
        full_message = f"[{timestamp}] {text}"
        if len(full_message) > MAX_MESSAGE_LENGTH:
            full_message = full_message[: MAX_MESSAGE_LENGTH - 25] + "\n\n[Message truncated]"

        return await self._send_to_single_chat(full_message, target)

    async def _send_to_single_chat(self, message: str, chat_id: str) -> bool:
        await self.limiter.acquire()
        try:
            resp = await self._client.post(self._url("sendMessage"), json={"chat_id": chat_id, "text": message})
        except httpx.HTTPError as e:
            self.logger.error("Failed to send telegram message chat_id=%s error=%s", chat_id, e)
            return False

        if resp.status_code == 200:
            self.logger.debug("Telegram message sent chat_id=%s", chat_id)
            return True

        try:
            result = resp.json()
        except ValueError:
            return False

        new_id = (result.get("parameters") or {}).get("migrate_to_chat_id")
        if new_id is not None:
            new_chat_id = str(int(new_id))
            self.logger.info("Chat migrated old_id=%s new_id=%s", chat_id, new_chat_id)
            self.chat_id = new_chat_id
            return await self._send_to_single_chat(message, new_chat_id)
        return False

    async def get_updates(self) -> list[dict]:
        resp = await self._client.get(
            self._url("getUpdates"),
            params={"offset": self.last_update_id + 1, "timeout": 5},
            timeout=15.0,
        )
        resp.raise_for_status()
        return resp.json().get("result", [])

    async def set_webhook(self, url: str) -> None:
        resp = await self._client.post(self._url("setWebhook"), json={"url": url, "allowed_updates": ["message"]})
        result = resp.json()
        if not result.get("ok"):
            raise RuntimeError(f"telegram API error: {result.get('description')}")
        self.logger.info("Webhook set successfully url=%s", url)

    async def delete_webhook(self) -> None:
        resp = await self._client.post(self._url("deleteWebhook"))
        result = resp.json()
        if not result.get("ok"):
            raise RuntimeError(f"telegram API error: {result.get('description')}")
        self.logger.info("Webhook deleted successfully")

    async def get_webhook_info(self) -> dict:
        resp = await self._client.get(self._url("getWebhookInfo"))
        return resp.json().get("result", {})


CommandHandler = "Callable[[str, str], Awaitable[None]]"  # chat_id, args -> None


class Bot:
    """Routes incoming Telegram commands (from polling or webhook) to handlers."""

    def __init__(self, telegram: TelegramClient, logger: logging.Logger) -> None:
        self.telegram = telegram
        self.logger = logger
        self.commands: dict[str, "callable"] = {}

    def register_command(self, cmd: str, handler) -> None:
        self.commands[cmd] = handler

    @staticmethod
    def _parse_command(text: str) -> tuple[str, str]:
        cmd_text = text.split("@", 1)[0] if "@" in text.split(" ", 1)[0] else text
        parts = cmd_text.split(" ", 1)
        cmd = parts[0]
        args = parts[1] if len(parts) > 1 else ""
        return cmd, args

    async def dispatch(self, chat_id: str, text: str) -> None:
        cmd, args = self._parse_command(text)
        handler = self.commands.get(cmd)
        if handler is not None:
            await handler(chat_id, args)

    async def poll_once(self) -> None:
        try:
            updates = await self.telegram.get_updates()
        except (httpx.HTTPError, ValueError) as e:
            self.logger.error("Error getting updates error=%s", e)
            return

        for update in updates:
            update_id = update.get("update_id", 0)
            if update_id > self.telegram.last_update_id:
                self.telegram.last_update_id = update_id

            message = update.get("message")
            if not message:
                continue
            chat_id = str(message.get("chat", {}).get("id", ""))
            text = message.get("text")
            if not text:
                continue

            self.logger.info("Received command chat_id=%s text=%s", chat_id, text)
            await self.dispatch(chat_id, text)
