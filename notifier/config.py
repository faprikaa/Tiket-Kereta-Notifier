"""YAML configuration loading, validation and flattening."""

from __future__ import annotations

import sys
from dataclasses import dataclass, field
from datetime import datetime

import yaml

VALID_PROVIDERS = {"tiketkai", "traveloka", "tiketcom", "bookingkai"}


class ConfigError(Exception):
    pass


@dataclass
class ProviderEntry:
    name: str
    proxy_url: str = ""

    @staticmethod
    def parse(raw) -> "ProviderEntry":
        if isinstance(raw, str):
            return ProviderEntry(name=raw)
        if isinstance(raw, dict):
            return ProviderEntry(name=raw.get("name", ""), proxy_url=raw.get("proxy_url", "") or "")
        raise ConfigError(f"invalid provider entry: {raw!r}")


@dataclass
class TrainConfig:
    name: str
    origin: str
    destination: str
    date: str
    interval: int = 300
    notes: str = ""
    max_price: int = 0
    min_departure_hour: int = 0
    max_departure_hour: int = 0
    providers: list[ProviderEntry] = field(default_factory=list)

    def validate(self) -> None:
        if not self.origin:
            raise ConfigError(f"origin is required for train {self.name}")
        if not self.destination:
            raise ConfigError(f"destination is required for train {self.name}")
        if not self.date:
            raise ConfigError(f"date is required for train {self.name}")
        try:
            datetime.strptime(self.date, "%Y-%m-%d")
        except ValueError as e:
            raise ConfigError(f"invalid date format for train {self.name} (expected YYYY-MM-DD): {e}") from e
        if not self.providers:
            raise ConfigError(f"at least one provider is required for train {self.name}")
        for p in self.providers:
            if p.name.lower() not in VALID_PROVIDERS:
                raise ConfigError(
                    f"unknown provider '{p.name}' for train {self.name} "
                    f"(use: {', '.join(sorted(VALID_PROVIDERS))})"
                )


@dataclass
class FlatTrainConfig:
    """One flattened train × provider combination."""

    name: str
    origin: str
    destination: str
    date: str  # YYYY-MM-DD
    interval: int
    notes: str
    max_price: int
    min_departure_hour: int
    max_departure_hour: int
    provider_name: str
    proxy_url: str

    def date_yyyymmdd(self) -> str:
        return self.date.replace("-", "")

    def date_parts(self) -> tuple[int, int, int]:
        """Returns (day, month, year)."""
        d = datetime.strptime(self.date, "%Y-%m-%d")
        return d.day, d.month, d.year


@dataclass
class TelegramConfig:
    bot_token: str = ""
    chat_id: str = ""


@dataclass
class WebhookConfig:
    enabled: bool = False
    port: int = 8080


@dataclass
class BrowserConfig:
    headless: bool = True


@dataclass
class Config:
    telegram: TelegramConfig
    webhook: WebhookConfig
    browser: BrowserConfig
    trains: list[TrainConfig]
    flat_trains: list[FlatTrainConfig] = field(default_factory=list)

    def validate(self) -> None:
        if not self.telegram.bot_token:
            raise ConfigError("telegram.bot_token is required in config.yml")
        if not self.telegram.chat_id:
            raise ConfigError("telegram.chat_id is required in config.yml")
        if not self.trains:
            raise ConfigError("at least one train configuration is required")
        for i, train in enumerate(self.trains):
            try:
                train.validate()
            except ConfigError as e:
                raise ConfigError(f"train #{i + 1}: {e}") from e
        if not self.flat_trains:
            raise ConfigError("no provider configured for any train")


def _process(cfg: Config) -> None:
    for train in cfg.trains:
        if train.interval <= 0:
            train.interval = 300
        for prov in train.providers:
            cfg.flat_trains.append(
                FlatTrainConfig(
                    name=train.name,
                    origin=train.origin,
                    destination=train.destination,
                    date=train.date,
                    interval=train.interval,
                    notes=train.notes,
                    max_price=train.max_price,
                    min_departure_hour=train.min_departure_hour,
                    max_departure_hour=train.max_departure_hour,
                    provider_name=prov.name.lower(),
                    proxy_url=prov.proxy_url,
                )
            )
    if not cfg.webhook.port:
        cfg.webhook.port = 8080


def load(path: str) -> Config:
    try:
        with open(path, "r", encoding="utf-8") as f:
            raw = yaml.safe_load(f) or {}
    except OSError as e:
        print(f"Failed to read config file {path}: {e}", file=sys.stderr)
        sys.exit(1)
    except yaml.YAMLError as e:
        print(f"Failed to parse YAML config: {e}", file=sys.stderr)
        sys.exit(1)

    telegram_raw = raw.get("telegram") or {}
    webhook_raw = raw.get("webhook") or {}
    browser_raw = raw.get("browser") or {}

    trains: list[TrainConfig] = []
    for t in raw.get("trains") or []:
        providers_raw = t.get("providers") or []
        # Backward compat: single `provider` (+ optional `proxy_url`) field.
        if not providers_raw and t.get("provider"):
            providers_raw = [{"name": t["provider"], "proxy_url": t.get("proxy_url", "")}]

        trains.append(
            TrainConfig(
                name=str(t.get("name", "")),
                origin=str(t.get("origin", "")),
                destination=str(t.get("destination", "")),
                date=str(t.get("date", "")),
                interval=int(t.get("interval") or 300),
                notes=str(t.get("notes", "") or ""),
                max_price=int(t.get("max_price") or 0),
                min_departure_hour=int(t.get("min_departure_hour") or 0),
                max_departure_hour=int(t.get("max_departure_hour") or 0),
                providers=[ProviderEntry.parse(p) for p in providers_raw],
            )
        )

    cfg = Config(
        telegram=TelegramConfig(
            bot_token=str(telegram_raw.get("bot_token", "")),
            chat_id=str(telegram_raw.get("chat_id", "")),
        ),
        webhook=WebhookConfig(
            enabled=bool(webhook_raw.get("enabled", False)),
            port=int(webhook_raw.get("port") or 8080),
        ),
        # ponytail: extra keys in `browser:` (chromium_path, user_data_dir from
        # the pre-Camoufox era) are silently ignored, so old configs still load.
        browser=BrowserConfig(headless=bool(browser_raw.get("headless", True))),
        trains=trains,
    )
    _process(cfg)
    return cfg
