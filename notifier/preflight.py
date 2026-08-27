"""Validates external runtime dependencies before the bot starts provider schedulers.

Never installs or changes anything on the host — only reports what's missing.
"""

from __future__ import annotations

import shutil

from .config import Config


class PreflightError(Exception):
    pass


def check(cfg: Config) -> None:
    missing: list[str] = []

    if cfg.webhook.enabled and not _command_available("cloudflared"):
        missing.append("cloudflared (run ./scripts/setup-ubuntu.sh)")

    if missing:
        details = "\n- ".join(sorted(missing))
        raise PreflightError(f"missing runtime dependencies:\n- {details}")


def _command_available(name: str) -> bool:
    return shutil.which(name) is not None
