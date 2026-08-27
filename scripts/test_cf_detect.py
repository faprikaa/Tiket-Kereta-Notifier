#!/usr/bin/env python3
"""Self-check for is_cloudflare_challenge: real challenge/block pages must be
caught, but a normal result page carrying Cloudflare's passive jsd script must
not be — that false positive hid a working curl_cffi fetch behind "BLOCKED"."""
import importlib.util, pathlib, sys

# Load by path: importing notifier.providers.* pulls in the browser deps via the
# package __init__, which this check doesn't need.
_p = pathlib.Path(__file__).resolve().parent.parent / "notifier/providers/bookingkai_parse.py"
_spec = importlib.util.spec_from_file_location("bookingkai_parse", _p)
_m = importlib.util.module_from_spec(_spec)
sys.modules["bookingkai_parse"] = _m
_spec.loader.exec_module(_m)
is_cloudflare_challenge = _m.is_cloudflare_challenge

REAL_PAGE_WITH_JSD = (
    '<html lang="id"><head><title>PT Kereta Api Indonesia -\n Reservasi Tiket\n</title>'
    '<script src="/cdn-cgi/challenge-platform/h/b/scripts/jsd/main.js"></script></head>'
    '<body><div class="data-block list-kereta">BOGOWONTO</div></body></html>'
)
assert not is_cloudflare_challenge(REAL_PAGE_WITH_JSD), "jsd script on a real page must not count as blocked"
assert is_cloudflare_challenge('<html><title>Just a moment...</title>')
assert is_cloudflare_challenge('<html>window._cf_chl_opt={}</html>')
assert is_cloudflare_challenge('<html class="cf-browser-verification">')
assert is_cloudflare_challenge('<html><title>Attention Required! | Cloudflare</title>')
assert is_cloudflare_challenge('<div class="cf-error-details">Sorry, you have been blocked</div>')
print("ok")
