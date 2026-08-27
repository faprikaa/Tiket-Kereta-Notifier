"""TiketKai.com provider — AES-CBC encrypted JSON API."""

from __future__ import annotations

import base64
import json
import logging

from cryptography.hazmat.primitives import padding
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

from ..config import FlatTrainConfig
from ..models import Train
from .base import BaseProvider
from .http_util import make_client

API_URL = "https://sc-microservice-tiketkai.bmsecure.id/train/search"
AES_KEY = b"78455d8581f1fc41"
AES_IV = b"34f1cdf17d1aacb8"

HEADERS = {
    "accept": "application/json, text/plain, */*",
    "accept-language": "en-US,en;q=0.9",
    "content-type": "text/plain",
    "origin": "https://m.tiketkai.com",
    "priority": "u=1, i",
    "referer": "https://m.tiketkai.com/",
    "sec-ch-ua": '"Not:A-Brand";v="99", "Microsoft Edge";v="145", "Chromium";v="145"',
    "sec-ch-ua-mobile": "?0",
    "sec-ch-ua-platform": '"Windows"',
    "sec-fetch-dest": "empty",
    "sec-fetch-mode": "cors",
    "sec-fetch-site": "cross-site",
    "user-agent": (
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
        "(KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36 Edg/145.0.0.0"
    ),
}


def _encrypt_aes_base64(plaintext: str) -> str:
    padder = padding.PKCS7(algorithms.AES.block_size).padder()
    padded = padder.update(plaintext.encode("utf-8")) + padder.finalize()
    encryptor = Cipher(algorithms.AES(AES_KEY), modes.CBC(AES_IV)).encryptor()
    ciphertext = encryptor.update(padded) + encryptor.finalize()
    return base64.b64encode(ciphertext).decode("ascii")


class TiketKaiProvider(BaseProvider):
    provider_key = "tiketkai"
    include_class_in_notify = False
    default_interval = 60.0

    def __init__(self, logger: logging.Logger, flat: FlatTrainConfig, index: int) -> None:
        super().__init__(logger, flat, index)

    async def _fetch(self) -> tuple[list[Train], str]:
        if not self.origin or not self.destination:
            raise ValueError("origin and destination required")

        self.logger.info("Searching TiketKai origin=%s dest=%s date=%s", self.origin, self.destination, self.date)

        payload = {
            "app": "TKAI",
            "via": "mobile_web",
            "date": self.date,
            "destination": self.destination,
            "origin": self.origin,
            "productCode": "WKAI",
            "deviceInfo": {
                "model": "Windows NT 10.0",
                "versionCode": 10037,
                "versionName": "1.3.0",
            },
        }
        encrypted = _encrypt_aes_base64(json.dumps(payload))

        async with make_client(self.proxy_url, 120.0) as client:
            resp = await client.post(API_URL, content=encrypted, headers=HEADERS)

        if resp.status_code != 200:
            raise RuntimeError(f"TiketKai API HTTP {resp.status_code}: {resp.text[:200]}")

        body = resp.json()
        rc = body.get("rc")
        if rc != "00":
            raise RuntimeError(f"API RC: {rc}")

        data = body.get("data")
        if isinstance(data, str):
            raise RuntimeError(f"API message: {data}")

        trains: list[Train] = []
        for tr in data or []:
            seats_available = "0"
            avail_status = "FULL"
            min_price = 0
            for seat in tr.get("seats") or []:
                availability = seat.get("availability")
                is_avail = False
                seat_count = "0"
                if isinstance(availability, (int, float)):
                    if availability > 0:
                        is_avail = True
                        seat_count = str(int(availability))
                elif isinstance(availability, str) and availability not in ("0", "Habis", ""):
                    is_avail = True
                    seat_count = availability

                if is_avail:
                    seats_available = seat_count
                    avail_status = "AVAILABLE"
                    price = seat.get("priceAdult")
                    try:
                        min_price = int(float(price))
                    except (TypeError, ValueError):
                        min_price = 0
                    break

            trains.append(
                Train(
                    name=tr.get("trainName", ""),
                    train_class="ECO",
                    price=min_price,
                    departure_time=tr.get("departureTime", ""),
                    arrival_time=tr.get("arrivalTime", ""),
                    availability=avail_status,
                    seats_left=seats_available,
                )
            )

        return trains, "edge145"  # ponytail: keep in sync with HEADERS above
