#!/usr/bin/env python3
"""Train Ticket Notifier — entry point.

Usage: python main.py -c config.yml
"""

from __future__ import annotations

import argparse
import asyncio
import logging
import signal
import sys

from notifier import app, bot_commands, config as config_module, preflight
from notifier.telegram_client import Bot, TelegramClient


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Train Ticket Notifier")
    parser.add_argument("-c", "--config", default="config.yml", help="Path to YAML config file")
    return parser.parse_args()


async def main_async() -> int:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    logger = logging.getLogger("notifier")

    args = parse_args()
    cfg = config_module.load(args.config)

    try:
        cfg.validate()
    except config_module.ConfigError as e:
        logger.error("Config validation failed: %s", e)
        return 1

    try:
        preflight.check(cfg)
    except preflight.PreflightError as e:
        logger.error("Runtime dependency check failed: %s", e)
        return 1

    telegram = TelegramClient(cfg.telegram.bot_token, cfg.telegram.chat_id, logger)
    bot = Bot(telegram, logger)

    providers, bk_queue = await app.build_providers(logger, cfg)
    if not providers:
        logger.error("No train monitors configured")
        if bk_queue is not None:
            await bk_queue.close()
        await telegram.close()
        return 1

    try:
        # Test Tiket.com connections (Turnstile/captcha can only be detected live).
        for provider, flat in zip(providers, cfg.flat_trains):
            if flat.provider_name == "tiketcom":
                await app.test_tiketcom_connection(logger, provider)

        bot_commands.register_commands(bot, telegram, providers, cfg)

        # Start schedulers with a per-provider-type stagger so trains sharing
        # a provider don't all hit the API in the same instant.
        scheduler_tasks: list[asyncio.Task] = []
        provider_position: dict[str, int] = {}
        for provider, flat in zip(providers, cfg.flat_trains):
            position = provider_position.get(flat.provider_name, 0)
            provider_position[flat.provider_name] = position + 1
            delay = position * 15.0
            scheduler_tasks.append(asyncio.create_task(_run_scheduler(provider, telegram, delay, logger)))

        logger.info("Started train monitors count=%d", len(providers))

        logger.info("Validating configured trains...")
        try:
            await app.validate_trains_exist(logger, providers, cfg)
        except app.StartupError as e:
            logger.error("Train validation failed: %s", e)
            await telegram.send_message(f"❌ Bot failed to start!\n{e}")
            return 1
        logger.info("✅ All configured trains validated successfully")

        shutdown = asyncio.Event()
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            loop.add_signal_handler(sig, shutdown.set)

        await app.run_bot(logger, cfg, telegram, bot, len(providers), shutdown)

        for task in scheduler_tasks:
            task.cancel()
        await asyncio.gather(*scheduler_tasks, return_exceptions=True)

        await asyncio.sleep(1)
        logger.info("Shutdown complete")
        return 0
    except app.StartupError as e:
        logger.error("Failed to initialize providers: %s", e)
        return 1
    finally:
        if bk_queue is not None:
            await bk_queue.close()
        await telegram.close()


async def _run_scheduler(provider, telegram: TelegramClient, initial_delay: float, logger: logging.Logger) -> None:
    if initial_delay > 0:
        logger.info("Staggering scheduler start provider=%s delay=%ss", provider.name, initial_delay)
        try:
            await asyncio.sleep(initial_delay)
        except asyncio.CancelledError:
            return

    async def notify(message: str) -> None:
        await telegram.send_message(message)

    await provider.start_scheduler(notify)


def main() -> None:
    sys.exit(asyncio.run(main_async()))


if __name__ == "__main__":
    main()
