"""Validates external runtime dependencies before the bot starts provider schedulers.

Never installs or changes anything on the host — only reports what's missing.
"""

from __future__ import annotations

import os
import shutil

from .config import Config


class PreflightError(Exception):
    pass


def check(cfg: Config) -> None:
    missing: list[str] = []
    required_providers = {t.provider_name for t in cfg.flat_trains}

    if "bookingkai" in required_providers and not _chromium_available(cfg.browser.chromium_path):
        missing.append("chromium (run ./scripts/setup-ubuntu.sh or set browser.chromium_path)")
    if cfg.webhook.enabled and not _command_available("cloudflared"):
        missing.append("cloudflared (run ./scripts/setup-ubuntu.sh)")

    if missing:
        details = "\n- ".join(sorted(missing))
        raise PreflightError(f"missing runtime dependencies:\n- {details}")


def _command_available(name: str) -> bool:
    return shutil.which(name) is not None


def _chromium_available(configured_path: str) -> bool:
    if configured_path:
        return os.path.isfile(configured_path) and os.access(configured_path, os.X_OK)
    return any(
        _command_available(name)
        for name in ("chromium-browser", "chromium", "google-chrome-stable", "google-chrome")
    )
