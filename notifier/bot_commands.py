"""Telegram command handlers: /list /check /all /status /history /toggle /sleep /help."""

from __future__ import annotations

import asyncio
import time

from .config import Config, FlatTrainConfig
from .models import Train
from .providers.base import BaseProvider
from .telegram_client import Bot, TelegramClient
from .utils import format_duration, format_hour_range, format_price, format_rupiah

PROVIDER_EMOJI = {
    "traveloka": "✈️",
    "tiketkai": "🚉",
    "tiketcom": "🎫",
    "bookingkai": "🏛️",
}


class SleepTracker:
    """Tracks per-train (0-indexed) sleep timers so /toggle can cancel them."""

    def __init__(self) -> None:
        self.entries: dict[int, tuple[asyncio.Task, float]] = {}  # index -> (task, wake_at)

    def cancel(self, index: int) -> bool:
        entry = self.entries.pop(index, None)
        if entry is None:
            return False
        entry[0].cancel()
        return True


def register_commands(bot: Bot, telegram: TelegramClient, providers: list[BaseProvider], cfg: Config) -> None:
    sleep = SleepTracker()
    n = len(providers)

    async def send(chat_id: str, text: str) -> None:
        await telegram.send_message(text, chat_id)

    def flat(i: int) -> FlatTrainConfig:
        return cfg.flat_trains[i]

    def _valid_index(args: str) -> int | None:
        try:
            idx = int(args)
        except ValueError:
            return None
        return idx if 1 <= idx <= n else None

    async def cmd_check(chat_id: str, args: str) -> None:
        args = args.strip()
        if args:
            idx = _valid_index(args)
            if idx is not None:
                await send(chat_id, await _check_train_result(providers[idx - 1], flat(idx - 1)))
                return

        await send(chat_id, f"🔍 Checking {n} trains...")

        lines: list[str] = []
        available_count = 0
        for i, provider in enumerate(providers):
            f = flat(i)
            try:
                trains = await provider.search()
            except Exception:
                lines.append(f"❌ #{i + 1} {f.name} [{f.date}] via {f.provider_name}: Error")
                continue

            available = _filter_available(trains, f.max_price)
            if available:
                available_count += 1
                lines.append(f"✅ #{i + 1} {f.name} [{f.date}] via {f.provider_name}: {len(available)} tersedia!")
                for t in available:
                    lines.append(f"   💺 {t.seats_left} seats @ {format_price(t.price)}")
            else:
                lines.append(f"⛔ #{i + 1} {f.name} [{f.date}] via {f.provider_name}: Habis")

        header = f"📊 Hasil Check ({available_count}/{n} tersedia):\n\n"
        await send(chat_id, header + "\n".join(lines))

    bot.register_command("/check", cmd_check)

    async def cmd_all(chat_id: str, args: str) -> None:
        args = args.strip()
        idx = _valid_index(args) if args else None
        if not args:
            await send(chat_id, "❌ Usage: /all <index>\nExample: /all 1")
            return
        if idx is None:
            await send(chat_id, f"❌ Invalid index. Use 1-{n}")
            return

        f = flat(idx - 1)
        provider = providers[idx - 1]
        await send(chat_id, f"📋 Fetching all trains for #{idx} [{f.date}] {f.provider_name}...")

        try:
            trains = await provider.search_all()
        except Exception as e:
            await send(chat_id, f"❌ Error: {e}")
            return

        if not trains:
            await send(chat_id, "❌ No trains found on this route")
            return

        lines = [f"🚂 All Trains: {f.origin} → {f.destination} [{f.date}]\n"]
        for i, t in enumerate(trains):
            status = "✅" if t.is_available else "⛔"
            lines.append(f"{i + 1}. {status} {t.name}")
            lines.append(f"   ⏰ {t.departure_time} → {t.arrival_time}")
            if t.is_available:
                lines.append(f"   💺 {t.seats_left} seats @ {format_price(t.price)}")
            lines.append("")

            if sum(len(l) + 1 for l in lines) > 3500:
                await send(chat_id, "\n".join(lines))
                lines = []

        if lines:
            lines.append(f"Total: {len(trains)} trains")
            await send(chat_id, "\n".join(lines))

    bot.register_command("/all", cmd_all)

    async def cmd_list(chat_id: str, args: str) -> None:
        args = args.strip()
        if args:
            idx = _valid_index(args)
            if idx is not None:
                f = flat(idx - 1)
                provider = providers[idx - 1]
                status = provider.get_status()
                last_check = "Never" if status.last_check_time == 0 else format_duration(time.time() - status.last_check_time) + " ago"
                paused_str = " ⏸️ PAUSED" if provider.is_paused() else ""

                lines = [f"🚂 Train #{idx}: {f.name}{paused_str}\n"]
                lines.append(f"📍 Route: {f.origin} → {f.destination}")
                lines.append(f"📅 Date: {f.date}")
                lines.append(f"🔌 Provider: {f.provider_name}")
                lines.append(f"⏱️ Interval: {int(f.interval)}s")
                lines.append(f"🌐 Proxy: {'Yes' if f.proxy_url else 'No'}")
                if f.max_price:
                    lines.append(f"💰 Max Price: Rp {format_rupiah(f.max_price)}")
                if f.min_departure_hour or f.max_departure_hour:
                    lines.append(f"🕐 Jam Berangkat: {format_hour_range(f.min_departure_hour, f.max_departure_hour)}")
                if f.notes:
                    lines.append(f"📝 Notes: {f.notes}")
                lines.append(f"\n📊 Last check: {last_check}")
                await send(chat_id, "\n".join(lines))
                return

        lines = ["🚂 *Configured Trains*"]
        flat_idx = 0
        for train_cfg in cfg.trains:
            lines.append(f"\n🚂 *{train_cfg.name}* | {train_cfg.origin} → {train_cfg.destination}")
            header = f"📅 {train_cfg.date}"
            if train_cfg.notes:
                header += f" | 📝 {train_cfg.notes}"
            lines.append(header)

            for _ in train_cfg.providers:
                if flat_idx >= len(cfg.flat_trains):
                    break
                provider = providers[flat_idx]
                f = flat(flat_idx)
                status = provider.get_status()

                status_icon = "⬜"
                if status.last_check_time:
                    if status.last_check_error:
                        status_icon = "❌"
                    elif status.last_check_found:
                        status_icon = "✅"
                    else:
                        status_icon = "⛔"
                if provider.is_paused():
                    status_icon = "⏸️"

                last_check = "never" if not status.last_check_time else format_duration(time.time() - status.last_check_time) + " ago"
                emoji = PROVIDER_EMOJI.get(f.provider_name, "🔌")
                label = f.provider_name.upper() + (" (proxy)" if f.proxy_url else "")

                lines.append(f" {status_icon} {emoji} {label} | #{flat_idx + 1} | {int(f.interval)}s | {last_check}")
                filters = []
                if f.max_price:
                    filters.append(f"💰 ≤ Rp {format_rupiah(f.max_price)}")
                if f.min_departure_hour or f.max_departure_hour:
                    filters.append(f"🕐 {format_hour_range(f.min_departure_hour, f.max_departure_hour)}")
                if filters:
                    lines.append("      " + " | ".join(filters))
                flat_idx += 1

        lines.append("\n/list <n> · /check <n> · /toggle <n>")
        await send(chat_id, "\n".join(lines))

    bot.register_command("/list", cmd_list)

    async def cmd_status(chat_id: str, args: str) -> None:
        args = args.strip()
        if args:
            idx = _valid_index(args)
            if idx is not None:
                await send(chat_id, _train_status_text(providers[idx - 1], flat(idx - 1), idx))
                return

        lines = ["🤖 Bot Status Summary"]
        if sleep.entries:
            lines.append("")
            for i, (_task, wake_at) in sleep.entries.items():
                remaining = wake_at - time.time()
                wake_str = time.strftime("%H:%M", time.localtime(wake_at))
                lines.append(f"💤 #{i + 1} {flat(i).name} — aktif pukul {wake_str} ({format_duration(remaining)} lagi)")
        lines.append("")

        total_checks = total_success = total_failed = 0
        for i, provider in enumerate(providers):
            status = provider.get_status()
            f = flat(i)
            total_checks += status.total_checks
            total_success += status.successful_checks
            total_failed += status.failed_checks

            icon = "✅" if status.last_check_found else "⛔"
            if status.last_check_error:
                icon = "❌"
            if provider.is_paused():
                icon = "⏸️"

            lines.append(f"{i + 1}. {icon} {f.name} [{f.date}] via {f.provider_name}")

        lines.append(f"\n📊 Total: {total_checks} checks | ✅ {total_success} | ❌ {total_failed}")
        lines.append("\nUse /status [n] for detailed status")
        await send(chat_id, "\n".join(lines))

    bot.register_command("/status", cmd_status)

    async def cmd_history(chat_id: str, args: str) -> None:
        parts = args.split()
        train_idx = 0
        count = 3
        if len(parts) >= 1:
            idx = _valid_index(parts[0])
            if idx is not None:
                train_idx = idx - 1
        if len(parts) >= 2:
            try:
                c = int(parts[1])
                if c > 0:
                    count = c
            except ValueError:
                pass

        results = providers[train_idx].get_history(count)
        f = flat(train_idx)
        if not results:
            await send(chat_id, f"📭 No history for {f.name} yet.")
            return

        lines = [f"📜 History: {f.name} (last {len(results)})\n"]
        for i, r in enumerate(results):
            ts = time.strftime("%d %b %H:%M", time.localtime(r.timestamp))
            method = f" [{r.method}]" if r.method else ""
            if r.error:
                lines.append(f"{i + 1}. ❌ [{ts}] Error: {r.error}")
            elif r.available_trains:
                lines.append(f"{i + 1}. ✅ [{ts}] {len(r.available_trains)} available{method}")
            else:
                lines.append(f"{i + 1}. ⛔ [{ts}] No seats{method}")

        await send(chat_id, "\n".join(lines))

    bot.register_command("/history", cmd_history)

    async def cmd_toggle(chat_id: str, args: str) -> None:
        args = args.strip()
        idx = _valid_index(args) if args else None
        if not args:
            await send(chat_id, "❌ Usage: /toggle <index>\nExample: /toggle 1")
            return
        if idx is None:
            await send(chat_id, f"❌ Invalid index. Use 1-{n}")
            return

        i = idx - 1
        provider = providers[i]
        f = flat(i)

        sleep_cancelled = sleep.cancel(i)

        new_state = not provider.is_paused()
        provider.set_paused(new_state)

        msg = f"⏸️ Train #{idx} ({f.name}) paused" if new_state else f"▶️ Train #{idx} ({f.name}) resumed"
        if sleep_cancelled:
            msg += "\n⚠️ Sleep timer dibatalkan."
        await send(chat_id, msg)

    bot.register_command("/toggle", cmd_toggle)

    async def _sleep_timer(chat_id: str, i: int, minutes: int) -> None:
        try:
            await asyncio.sleep(minutes * 60)
        except asyncio.CancelledError:
            return  # cancelled externally (e.g. /toggle), don't resume
        sleep.entries.pop(i, None)
        providers[i].set_paused(False)
        now_str = time.strftime("%H:%M", time.localtime())
        await send(chat_id, f"⏰ Sleep selesai pukul {now_str}! Train #{i + 1} ({flat(i).name}) dilanjutkan.")

    async def cmd_sleep(chat_id: str, args: str) -> None:
        parts = args.split()
        usage = f"❌ Usage: /sleep <index> <menit>\nContoh:\n  /sleep 1 30  — pause train #1 selama 30 menit\n  /sleep 1 0   — batalkan sleep train #1 sekarang\nIndex valid: 1–{n}"

        if len(parts) != 2:
            await send(chat_id, usage)
            return

        idx = _valid_index(parts[0])
        if idx is None:
            await send(chat_id, f"❌ Index tidak valid (valid: 1–{n})")
            return
        try:
            minutes = int(parts[1])
        except ValueError:
            minutes = -1
        if minutes < 0:
            await send(chat_id, "❌ Menit harus angka ≥ 0")
            return

        i = idx - 1
        f = flat(i)
        provider = providers[i]

        prev_sleep = sleep.cancel(i)

        if minutes == 0:
            provider.set_paused(False)
            msg = f"⏰ Train #{idx} ({f.name}) dilanjutkan."
            if not prev_sleep:
                msg = "⚠️ Tidak ada sleep aktif untuk train ini.\n" + msg
            await send(chat_id, msg)
            return

        provider.set_paused(True)
        wake_at = time.time() + minutes * 60
        task = asyncio.create_task(_sleep_timer(chat_id, i, minutes))
        sleep.entries[i] = (task, wake_at)

        note = "\n⚠️ Sleep sebelumnya dibatalkan." if prev_sleep else ""
        wake_str = time.strftime("%H:%M", time.localtime(wake_at))
        await send(chat_id, f"💤 Train #{idx} ({f.name}) di-sleep {minutes} menit\nAktif kembali pukul {wake_str}.{note}")

    bot.register_command("/sleep", cmd_sleep)

    async def cmd_help(chat_id: str, args: str) -> None:
        await send(chat_id, _help_text(n))

    bot.register_command("/help", cmd_help)


def _filter_available(trains: list[Train], max_price: int) -> list[Train]:
    result = []
    for t in trains:
        if not t.is_available:
            continue
        if max_price and t.price and t.price > max_price:
            continue
        result.append(t)
    return result


async def _check_train_result(provider: BaseProvider, f: FlatTrainConfig) -> str:
    try:
        trains = await provider.search()
    except Exception as e:
        return f"❌ {f.name} [{f.date}] via {f.provider_name}\n   Error: {e}"

    if not trains:
        return f"❌ {f.name} [{f.date}] via {f.provider_name}\n   No trains found"

    available = _filter_available(trains, f.max_price)
    if available:
        lines = [f"✅ {f.name} [{f.date}] via {f.provider_name}: {len(available)} tersedia!"]
        for t in available:
            lines.append(f"   🚂 {t.name}\n   ⏰ {t.departure_time} → {t.arrival_time}\n   💺 {t.seats_left} seats @ {format_price(t.price)}")
        return "\n".join(lines)

    return f"⛔ {f.name} [{f.date}] via {f.provider_name}: Habis ({len(trains)} kereta full)"


def _train_status_text(provider: BaseProvider, f: FlatTrainConfig, index: int) -> str:
    status = provider.get_status()
    uptime = format_duration(time.time() - status.start_time)

    last_check = "Never"
    last_result = "N/A"
    if status.last_check_time:
        last_check = format_duration(time.time() - status.last_check_time) + " ago"
        if status.last_check_error:
            last_result = f"❌ Error: {status.last_check_error}"
        elif status.last_check_found:
            last_result = "✅ Found seats!"
        else:
            last_result = "⛔ No seats"

    paused_str = " ⏸️ PAUSED" if provider.is_paused() else ""

    lines = [f"🚂 Train #{index}: {f.name}{paused_str}\n"]
    lines.append(f"📍 Route: {f.origin} → {f.destination}")
    lines.append(f"📅 Date: {f.date}")
    lines.append(f"🔌 Provider: {f.provider_name}")
    lines.append(f"⏱️ Interval: {int(f.interval)}s")
    if f.max_price:
        lines.append(f"💰 Max Price: Rp {format_rupiah(f.max_price)}")
    if f.min_departure_hour or f.max_departure_hour:
        lines.append(f"🕐 Jam Berangkat: {format_hour_range(f.min_departure_hour, f.max_departure_hour)}")
    if f.notes:
        lines.append(f"📝 Notes: {f.notes}")

    lines.append("\n📊 Statistics:")
    lines.append(f"• Uptime: {uptime}")
    lines.append(f"• Checks: {status.total_checks} (✅ {status.successful_checks} | ❌ {status.failed_checks})")
    lines.append(f"• Last: {last_check} — {last_result}")
    return "\n".join(lines)


def _help_text(train_count: int) -> str:
    return f"""🚂 Train Notifier (Monitoring {train_count} trains)

/list - List all configured trains
/list [n] - Show train #n details
/check [n] - Check train #n (or all)
/all [n] - Show all trains on route #n
/status [n] - Status & settings train #n (or summary)
/history [n] [count] - History of train #n
/toggle [n] - Pause/resume train #n
/sleep <index> <menit> - Pause train #n selama N menit, lalu auto-resume
/sleep <index> 0 - Batalkan sleep train #n sekarang

Examples:
/check 1 - Check first train only
/check - Check all trains
/all 3 - All trains on route #3
/toggle 5 - Pause/resume train #5
/sleep 1 30 - Pause train #1 selama 30 menit
/sleep 1 0  - Batalkan sleep train #1"""
