"""Small standalone helpers shared across the codebase."""

from __future__ import annotations


def is_wildcard(name: str) -> bool:
    """Report whether name is a wildcard train name ("any" or "*")."""
    n = name.strip().lower()
    return n in ("any", "*")


def parse_price(s: str) -> int:
    """Parse a price string (e.g. "Rp 350.000", "350000") into an int Rupiah amount.

    Returns 0 if the value cannot be parsed.
    """
    s = s.strip()
    for prefix in ("Rp ", "Rp"):
        if s.startswith(prefix):
            s = s[len(prefix):]
            break
    s = s.strip().replace(".", "").replace(",", "")
    if "." in s:
        s = s.split(".", 1)[0]
    try:
        return int(s)
    except ValueError:
        return 0


def format_rupiah(amount: int) -> str:
    """Format an integer as Indonesian Rupiah digits with dot separators, e.g. 350000 -> "350.000"."""
    return f"{amount:,}".replace(",", ".")


def format_price(amount: int) -> str:
    """Format an integer amount as a full "Rp 350.000" style string. 0/unknown -> "?"."""
    if amount <= 0:
        return "?"
    return f"Rp{format_rupiah(amount)}"


def format_hour_range(min_h: int, max_h: int) -> str:
    """Return a human-readable departure hour range string."""
    if min_h > 0 and max_h > 0:
        return f"{min_h:02d}:00 – {max_h:02d}:59"
    if min_h > 0:
        return f"≥ {min_h:02d}:00"
    return f"≤ {max_h:02d}:59"


def format_duration(seconds: float) -> str:
    """Format a duration (in seconds) into a human readable "1h 2m" / "3m 4s" / "5s" string."""
    total = int(round(seconds))
    hours, rem = divmod(total, 3600)
    minutes, secs = divmod(rem, 60)
    if hours > 0:
        return f"{hours}h {minutes}m"
    if minutes > 0:
        return f"{minutes}m {secs}s"
    return f"{secs}s"


def truncate(s: str, max_len: int) -> str:
    """Limit a string to max_len characters, appending "..." if truncated."""
    if len(s) <= max_len:
        return s
    return s[:max_len] + "..."
