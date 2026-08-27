#!/usr/bin/env python3
"""Self-check for the queue's two-stage order: curl_cffi first, camoufox only
when stage 1 fails. Needs the app deps installed — run it from the venv:

    .venv/bin/python scripts/test_bookingkai_fallback.py
"""
import asyncio, logging, pathlib, sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))
from notifier.models import Train
from notifier.providers.browser_queue import BrowserQueue

URL = "https://booking.kai.id/?origination=LPN"
TRAIN = Train(
    name="BOGOWONTO", train_class="EKO (CC)", departure_time="08:15", arrival_time="15:16",
    seats_left="1", price=360000, availability="AVAILABLE",
)


def _queue(curl, browser):
    q = BrowserQueue(logging.getLogger("t"))
    calls = []
    q._fetch_via_curl = lambda u: (calls.append("curl"), curl())[1]

    async def _b(u):
        calls.append("browser")
        return browser()
    q._fetch_via_browser = _b
    return q, calls


def _boom():
    raise RuntimeError("blocked by Cloudflare challenge or CAPTCHA")


async def main():
    # stage 1 succeeds -> browser never launched
    q, calls = _queue(lambda: [TRAIN], lambda: [])
    trains, method = await q._do_fetch(URL)
    assert method == "curl_cffi", method
    assert calls == ["curl"], calls
    assert trains == [TRAIN]

    # stage 1 fails -> falls back, and a good stage 2 clears the backoff
    q, calls = _queue(_boom, lambda: [TRAIN])
    trains, method = await q._do_fetch(URL)
    assert method == "camoufox", method
    assert calls == ["curl", "browser"], calls
    assert q._next_challenge_retry == 0.0

    # both fail -> raises, and a Cloudflare failure arms the backoff
    q, calls = _queue(_boom, _boom)
    try:
        await q._do_fetch(URL)
    except RuntimeError:
        pass
    else:
        raise AssertionError("expected both-stages-failed to raise")
    assert calls == ["curl", "browser"], calls
    assert q._challenge_failures == 1
    assert q._next_challenge_retry > 0

    # backoff is honoured: no stage runs at all while it's active
    calls.clear()
    try:
        await q._do_fetch(URL)
    except RuntimeError as e:
        assert "backoff active" in str(e), e
    else:
        raise AssertionError("expected backoff to block the fetch")
    assert calls == [], calls

    print("ok")


asyncio.run(main())
