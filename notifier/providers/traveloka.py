"""Traveloka.com provider — direct JSON train search API."""

from __future__ import annotations

import logging
from datetime import datetime

from ..config import FlatTrainConfig
from ..models import Train
from .base import BaseProvider
from .http_util import make_client

API_URL = "https://www.traveloka.com/api/v2/train/search/inventoryv2"

HEADERS = {
    "accept": "*/*",
    "accept-language": "en-US,en;q=0.9",
    "content-type": "application/json",
    "origin": "https://www.traveloka.com",
    "priority": "u=1, i",
    "sec-ch-ua": '"Not(A:Brand";v="8", "Chromium";v="144", "Microsoft Edge";v="144"',
    "sec-ch-ua-mobile": "?0",
    "sec-ch-ua-platform": '"Windows"',
    "sec-fetch-dest": "empty",
    "sec-fetch-mode": "cors",
    "sec-fetch-site": "same-origin",
    "t-a-v": "262360",
    "tv-clientsessionid": "T1-web.01KGE6X3HP3X6MVCNR2AMJEF6N",
    "tv-country": "ID",
    "tv-currency": "IDR",
    "tv-language": "en_ID",
    "user-agent": (
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
        "(KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36 Edg/144.0.0.0"
    ),
    "www-app-version": "release_webgtr_desktop_20251222-2dc8e9ba35",
    "x-client-interface": "desktop",
    "x-did": "MDFLR0U2WDUxN0IyVEFaMTEzS1ZXNjE0M0U=",
    "x-domain": "train",
    "x-route-prefix": "en-id",
    "Cookie": (
        "tvl=/EIrVOerOa3DtJX7TcoqVVwPlJH9eW394k6fpaWLslcRunZ6tdOB0t8G9Mc9M1KOayJocKqhwWyUybA+siomf6Z1NIRkl4+zP9m6QLveVWNSBmE6j7N/f5Ak4a0XPxvp5PPK4CfwSL4WnqZ1SZwpIMrI9rbU3DW4937voR2AAOcDDXyp2/aJ3Q79M0+frVADhokUVa9kILrNKLkScFJy7VIttFPPtON02+yXg5HdMc5J65CukxBe9VbpglxHeDegHRPmjv9YxYhy2+PoUiJXdqontxt+emW4gz5FzyM/R1zafJn/ZFOiPxS94eecEXjf9n4iQiGRhpUisToKdn4FLFURPlNBkscyIExnIU35ktdTpzHmYVbwE06WNijs6/J8ZbwtkShBaXTYXwtgjc0zvlObFjXolfzyOjDbeYbO15zeJGT7NghchNsIQD1JL0gzbIZOz6PrmBQ4EVS4lKAqmYw1z/6N7h2/MntnNCQsSYwuuhUBoT29+UZP/+sTLAu0ot7l9fGx+2q29idcK4AUqYeCt2LPYnS+drO4p0qmyrkeXdLywrmm4xzyf8xYzPvifhU=~djAy; "
        "tvo=L2FwaS92Mi90cmFpbi9zZWFyY2gvaW52ZW50b3J5djI=; "
        "tvs=J8BxatqpFpVo6+xwWoQP8nMW1TKfNx8Y4EVxwZ6Svdrac+vGcw9dHsFj4lHJJZ0BsImyaom969VcPcRgIVAvDkHyFRC7llLBL0UVuX3wq5ANJ34+E2o20rQoKYTRJlsRUSbiEWViA/uwFBF8hL187HCJfeg4bXr9R+LhK1fbhMIolE8y1gsO/d/ugwZmj93fn8N5QVHooriCrYe9oPgudN1w1LBtX5LpNH17VQyvhUVA8KnRmyX4hjkcYuZebPk+eCDfATTrXEK682ho6uZrJUh2TKkUtU5JoL+Bhpg7Q3zWqQJJmG1NcOugyQ6BTCayl3flbVa4t/EXMM85wB7LEcZ6EKziLNO5/X2E4GHLkQcZ9pazc9RrVDtxvpbdB9HXbggGw0VEsrcTAoucgufy8X1BMesK+y6EKPECThRu3mKwL8EheU96pjk4W8sVPEFpljXj1S+ajnhHxxbxHFC3gnfe80/DCtlkEh+I7msTTaNE/z2ZbqZRzeqnGAkT05JgrkQnM52dylVRTCe+BSWUobx+ScewH5Wkjm2yyicYp+B+/M0vSU6gwXSNjr+AavguyYOYYZY6wQ==~djAy"
    ),
}


def _get(d: dict, key: str) -> str:
    v = d.get(key)
    return "" if v is None else str(v)


def _pad_zero(s: str) -> str:
    return s.zfill(2) if len(s) == 1 else s


def _parse_trains(data: dict) -> list[Train]:
    trains: list[Train] = []
    inventories = (data.get("data") or {}).get("departTrainInventories") or []
    for inv in inventories:
        price = 0
        fare = inv.get("fare") or {}
        cv = fare.get("currencyValue") or {}
        amount = cv.get("amount")
        try:
            price = int(float(amount))
        except (TypeError, ValueError):
            price = 0

        dep_time = ""
        dt = (inv.get("departureTime") or {}).get("hourMinute")
        if dt:
            dep_time = f"{_get(dt, 'hour')}:{_pad_zero(_get(dt, 'minute'))}"

        arr_time = ""
        at = (inv.get("arrivalTime") or {}).get("hourMinute")
        if at:
            arr_time = f"{_get(at, 'hour')}:{_pad_zero(_get(at, 'minute'))}"

        trains.append(
            Train(
                name=_get(inv, "trainBrandLabel"),
                train_class=_get(inv, "ticketLabel"),
                availability=_get(inv, "availability"),
                seats_left=_get(inv, "numSeatsAvailable"),
                price=price,
                departure_time=dep_time,
                arrival_time=arr_time,
            )
        )
    return trains


class TravelokaProvider(BaseProvider):
    provider_key = "traveloka"
    include_class_in_notify = False
    default_interval = 300.0

    def __init__(self, logger: logging.Logger, flat: FlatTrainConfig, index: int) -> None:
        super().__init__(logger, flat, index)
        d = datetime.strptime(self.date, "%Y-%m-%d")
        self.day, self.month, self.year = d.day, d.month, d.year

    async def _fetch(self) -> tuple[list[Train], str]:
        payload = {
            "fields": [],
            "data": {
                "departureDate": {"day": self.day, "month": self.month, "year": self.year},
                "returnDate": None,
                "destination": self.destination,
                "origin": self.origin,
                "numOfAdult": 1,
                "numOfInfant": 0,
                "providerType": "KAI",
                "currency": "IDR",
                "trackingMap": {"utmId": None, "utmEntryTimeMillis": 0},
            },
            "clientInterface": "desktop",
        }

        async with make_client(self.proxy_url, 60.0) as client:
            resp = await client.post(API_URL, json=payload, headers=HEADERS)

        if resp.status_code != 200:
            raise RuntimeError(f"API returned status {resp.status_code}: {resp.text[:300]}")

        return _parse_trains(resp.json()), "edge144"  # ponytail: keep in sync with HEADERS above
