#!/usr/bin/env python3
"""Self-check for the queue's two-stage order: curl_cffi first, camoufox only
when stage 1 fails. Needs the app deps installed — run it from the venv:

    .venv/bin/python scripts/test_bookingkai_fallback.py
"""
import asyncio, logging, pathlib, sys, time

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))
from notifier.models import Train
from notifier.providers import browser_queue as bq
from notifier.providers.browser_queue import IMPERSONATE_POOL, BrowserQueue

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
    # edge101 and safari15_5 both get a hard 403 from booking.kai.id — keep
    # them out of the rotation.
    assert IMPERSONATE_POOL, "need at least one impersonation target"
    assert not {"edge101", "safari15_5"} & set(IMPERSONATE_POOL), IMPERSONATE_POOL

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

    # stage 1 walks the whole pool before giving up: one bad target must not
    # cost a browser launch.
    q = BrowserQueue(logging.getLogger("t"))
    tried = []

    def _once(url, target):
        tried.append(target)
        if target != "chrome124":
            raise RuntimeError("HTTP 403")
        return [TRAIN]

    q._curl_once = _once
    assert q._fetch_via_curl(URL) == [TRAIN]
    assert tried[-1] == "chrome124", tried
    assert set(tried) <= set(IMPERSONATE_POOL), tried

    # every target failing is what escalates to stage 2
    q._curl_once = lambda url, target: (_ for _ in ()).throw(RuntimeError("HTTP 403"))
    try:
        q._fetch_via_curl(URL)
    except RuntimeError as e:
        assert "all impersonation targets failed" in str(e), e
    else:
        raise AssertionError("expected an all-targets-failed error")

    # a resident camoufox is torn down once stage 1 works again, but not while
    # it is still being used
    class _CM:
        closed = False

        async def __aexit__(self, *a):
            _CM.closed = True

    q, calls = _queue(lambda: [TRAIN], lambda: [])
    q._camoufox_cm = _CM()
    q._browser_last_used = time.time()
    await q._do_fetch(URL)
    assert not _CM.closed, "browser closed while still recently used"

    q._browser_last_used = time.time() - bq.BROWSER_IDLE_TIMEOUT - 1
    await q._do_fetch(URL)
    assert _CM.closed, "idle browser was not shut down"
    assert q._camoufox_cm is None and q._page is None

    print("ok")


asyncio.run(main())
