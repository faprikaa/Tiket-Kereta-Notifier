"""Shared httpx.AsyncClient construction with optional SOCKS5/HTTP proxy support."""

from __future__ import annotations

import httpx


def make_client(proxy_url: str, timeout: float) -> httpx.AsyncClient:
    """Build an AsyncClient, optionally routed through proxy_url.

    Supports http(s):// and socks5(h):// proxy URLs (socks5 requires the
    `socksio` package, pulled in via the `httpx[socks]` extra).
    """
    kwargs: dict = {"timeout": timeout}
    if proxy_url:
        kwargs["proxy"] = proxy_url
    return httpx.AsyncClient(**kwargs)
