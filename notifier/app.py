"""Application wiring: provider construction, startup validation, bot lifecycle."""

from __future__ import annotations

import asyncio
import logging

from .config import Config, FlatTrainConfig
from .preflight import PreflightError, check as preflight_check
from .providers import BookingKaiProvider, BrowserQueue, TiketcomProvider, TiketKaiProvider, TravelokaProvider
from .providers.base import BaseProvider
from .telegram_client import Bot, TelegramClient
from .tunnel import Tunnel
from .utils import is_wildcard
from .webhook_server import WebhookServer

STAGGER_DELAY = 15.0  # seconds between trains on the same provider, to avoid bursts


class StartupError(Exception):
    pass


async def build_providers(
    logger: logging.Logger, cfg: Config
) -> tuple[list[BaseProvider], BrowserQueue | None]:
    providers: list[BaseProvider] = []

    bk_queue: BrowserQueue | None = None
    for flat in cfg.flat_trains:
        if flat.provider_name == "bookingkai":
            bk_queue = await BrowserQueue.create(
                logger,
                flat.proxy_url,
                cfg.browser.headless,
            )
            break

    for i, flat in enumerate(cfg.flat_trains):
        provider = _build_provider(logger, flat, i + 1, bk_queue)
        providers.append(provider)
        logger.info(
            "Initialized train monitor train=%s provider=%s route=%s→%s date=%s interval=%ss",
            flat.name, flat.provider_name, flat.origin, flat.destination, flat.date, flat.interval,
        )

    return providers, bk_queue


def _build_provider(logger: logging.Logger, flat: FlatTrainConfig, index: int, bk_queue: BrowserQueue | None) -> BaseProvider:
    if flat.provider_name == "tiketkai":
        return TiketKaiProvider(logger, flat, index)
    if flat.provider_name == "traveloka":
        return TravelokaProvider(logger, flat, index)
    if flat.provider_name == "tiketcom":
        return TiketcomProvider(logger, flat, index)
    if flat.provider_name == "bookingkai":
        assert bk_queue is not None
        return BookingKaiProvider(logger, flat, index, bk_queue)
    raise StartupError(f"unknown provider '{flat.provider_name}' (use: tiketkai, traveloka, tiketcom, bookingkai)")


async def test_tiketcom_connection(logger: logging.Logger, provider: TiketcomProvider) -> None:
    logger.info("Testing Tiket.com connection...")
    try:
        trains = await provider.search()
    except Exception as e:
        low = str(e).lower()
        if any(k in low for k in ("turnstile", "captcha", "challenge", "ray-id", "cloudflare")):
            raise StartupError("⚠️ Tiket.com is blocked by Turnstile/Captcha! Try using proxy_url in config") from e
        raise StartupError(f"connection test failed: {e}") from e
    logger.info("✅ Tiket.com connection OK trains_found=%d", len(trains))


async def validate_trains_exist(logger: logging.Logger, providers: list[BaseProvider], cfg: Config) -> None:
    """Groups trains by (name, origin, destination, date) and validates each group once.

    If one provider errors or doesn't find the train, the next provider in the
    same group is tried before failing.
    """
    groups: dict[tuple[str, str, str, str], list[int]] = {}
    order: list[tuple[str, str, str, str]] = []

    for i, flat in enumerate(cfg.flat_trains):
        if is_wildcard(flat.name) or not flat.name:
            logger.info("No train name filter, skipping validation route=%s→%s", flat.origin, flat.destination)
            continue
        key = (flat.name.lower(), flat.origin, flat.destination, flat.date)
        if key not in groups:
            order.append(key)
        groups.setdefault(key, []).append(i)

    for key in order:
        indices = groups[key]
        flat = cfg.flat_trains[indices[0]]
        target = flat.name.lower()

        validated = False
        last_err: Exception | None = None

        for idx in indices:
            provider = providers[idx]
            provider_name = cfg.flat_trains[idx].provider_name
            logger.info("Validating train... train=%s provider=%s", flat.name, provider_name)

            try:
                trains = await provider.search()
            except Exception as e:
                logger.warning("Validation failed, trying next provider train=%s provider=%s error=%s", flat.name, provider_name, e)
                last_err = e
                continue

            matched = next((t for t in trains if target in t.name.lower()), None)
            if matched is not None:
                logger.info("✓ Train found train=%s matched=%s availability=%s via=%s",
                            flat.name, matched.name, matched.availability, provider_name)
                validated = True
                break

            names = [t.name for t in trains]
            last_err = RuntimeError(
                f"train '{flat.name}' not found on route {flat.origin} → {flat.destination} "
                f"(date: {flat.date}). Available: {names}"
            )

        if not validated:
            raise StartupError(f"failed to validate train {flat.name}: {last_err}")


HELP_TEXT_TEMPLATE = """🚂 Train Notifier — Monitoring {n} trains

/list - List semua kereta
/list [n] - Detail kereta #n
/check [n] - Check kereta #n (atau semua)
/all [n] - Semua kereta di rute #n
/status [n] - Status kereta #n
/history [n] [count] - History kereta #n
/toggle [n] - Pause/resume kereta #n"""


async def run_bot(logger: logging.Logger, cfg: Config, telegram: TelegramClient, bot: Bot, train_count: int, shutdown: asyncio.Event) -> None:
    help_text = HELP_TEXT_TEMPLATE.format(n=train_count)
    tunnel: Tunnel | None = None
    webhook: WebhookServer | None = None
    fallback_poll: asyncio.Task | None = None

    if cfg.webhook.enabled:
        tunnel = Tunnel(logger)
        webhook = WebhookServer(cfg.webhook.port, bot, logger)
        await webhook.start()

        async def start_tunnel() -> None:
            nonlocal fallback_poll
            try:
                public_url = await tunnel.start(f"http://127.0.0.1:{cfg.webhook.port}")
            except Exception as e:
                logger.error("Failed to start tunnel error=%s log_file=%s", e, tunnel.log_file)
                await _fall_back_to_polling(
                    logger, telegram, bot, shutdown, f"tunnel gagal start: {e}", help_text
                )
                fallback_poll = asyncio.create_task(_poll_loop(bot, shutdown))
                return

            # The health probe can fail while the tunnel is fine (egress
            # filtering, DNS still propagating). Try registering anyway —
            # Telegram's own setWebhook is the authoritative reachability test.
            note = ""
            if not tunnel.ready:
                note = f"\n⚠️ Health check tunnel gagal ({tunnel.ready_error}); webhook tetap dicoba."

            if await _register_webhook(logger, telegram, webhook, public_url, shutdown):
                await telegram.send_message(f"🚀 Bot started!\n🔗 {public_url}{note}\n\n{help_text}")
                return

            # Never leave the bot unreachable just because the tunnel hostname
            # never became resolvable — long-polling needs no inbound path.
            await _fall_back_to_polling(
                logger, telegram, bot, shutdown, "webhook tidak bisa dipasang", help_text
            )
            fallback_poll = asyncio.create_task(_poll_loop(bot, shutdown))

        asyncio.create_task(start_tunnel())
        await shutdown.wait()
        if fallback_poll is not None:
            fallback_poll.cancel()
    else:
        # Clear a webhook left by a previous run before calling getUpdates.
        await telegram.delete_webhook()
        await telegram.send_message(f"🚀 Bot started!\n\n{help_text}")
        logger.info("Bot running in long-polling mode. Press Ctrl+C to exit.")
        poll_task = asyncio.create_task(_poll_loop(bot, shutdown))
        await shutdown.wait()
        poll_task.cancel()

    logger.info("Shutting down...")
    if cfg.webhook.enabled:
        try:
            await telegram.delete_webhook()
        except Exception as e:  # noqa: BLE001
            logger.warning("delete_webhook failed error=%s", e)
        if webhook is not None:
            await webhook.stop()
        if tunnel is not None:
            await tunnel.stop()


async def _register_webhook(
    logger: logging.Logger,
    telegram: TelegramClient,
    webhook: WebhookServer,
    public_url: str,
    shutdown: asyncio.Event,
) -> bool:
    """setWebhook, retried while Telegram still can't resolve the tunnel host."""
    delays = [0, 15, 30, 60, 60, 60, 60]
    last_error = ""
    for attempt, delay in enumerate(delays, start=1):
        if delay:
            try:
                await asyncio.wait_for(shutdown.wait(), timeout=delay)
                return False  # shutting down
            except asyncio.TimeoutError:
                pass
        try:
            await telegram.set_webhook(public_url + "/webhook", webhook.secret_token)
            logger.info("Webhook registered url=%s attempts=%d", public_url, attempt)
            return True
        except Exception as e:  # noqa: BLE001
            last_error = str(e)
            logger.warning(
                "set_webhook failed attempt=%d/%d url=%s error=%s", attempt, len(delays), public_url, e
            )
    logger.error("Giving up on webhook url=%s last_error=%s", public_url, last_error)
    return False


async def _fall_back_to_polling(
    logger: logging.Logger,
    telegram: TelegramClient,
    bot: Bot,
    shutdown: asyncio.Event,
    reason: str,
    help_text: str,
) -> None:
    logger.warning("Falling back to long-polling reason=%s", reason)
    try:
        await telegram.delete_webhook()
    except Exception as e:  # noqa: BLE001
        logger.warning("delete_webhook failed error=%s", e)
    await telegram.send_message(
        f"⚠️ {reason.capitalize()} — bot jalan pakai long-polling.\n\n{help_text}"
    )


async def _poll_loop(bot: Bot, shutdown: asyncio.Event) -> None:
    while not shutdown.is_set():
        await bot.poll_once()
