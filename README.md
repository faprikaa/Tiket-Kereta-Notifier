# 🚂 Train Ticket Notifier

Bot Telegram untuk monitoring ketersediaan tiket kereta api dari TiketKai, Traveloka, Tiket.com, dan BookingKAI (official).

## Features

- ✅ **Multi-Train Monitoring** - Monitor banyak kereta sekaligus
- ✅ **Per-Train Provider** - Setiap kereta bisa pakai provider berbeda
- ✅ **Per-Train Proxy** - Setiap kereta (tiketcom/bookingkai) bisa pakai proxy berbeda
- ✅ **Browser Queue** - BookingKAI requests diproses serial via shared queue (anti rate-limit)
- ✅ **Wildcard Train Name** - Pakai `"any"` / `"*"` untuk monitor semua kereta di rute
- ✅ **Filter Harga** - Filter tiket berdasarkan harga maksimal (Rupiah)
- ✅ **Filter Jam Berangkat** - Filter kereta berdasarkan jam keberangkatan (bookingkai only)
- ✅ **Sleep Mode** - Pause semua monitoring sementara dengan auto-resume terjadwal
- ✅ **YAML Config** - Konfigurasi mudah via file YAML
- ✅ **Startup Validation** - Verifikasi kereta ada sebelum monitoring
- ✅ **Telegram Bot** - Notifikasi real-time via Telegram
- ✅ **Webhook Mode** - Menggunakan Cloudflare Tunnel
- ✅ **Smart Notification** - Kirim notifikasi hanya jika ada kursi tersedia

## Installation

Target yang didukung oleh installer otomatis adalah Ubuntu `amd64`.

```bash
git clone https://github.com/faprikaa/Tiket-Kereta-Notifier.git
cd Tiket-Kereta-Notifier
chmod +x scripts/setup-ubuntu.sh
./scripts/setup-ubuntu.sh
cp config.yml.example config.yml
```

Installer bersifat idempotent: dependency yang sudah kompatibel akan dipakai
kembali. Untuk memeriksa kesiapan host tanpa mengubah apa pun:

```bash
./scripts/setup-ubuntu.sh --check
```

Setup memasang Chromium, `cloudflared`, Python 3.10+, library sistem, font,
dan `tmux`, lalu membuat virtualenv di `.venv/` dan menginstall dependency
Python dari `requirements.txt`. Setup tidak memasang WARP, Docker, atau
systemd.

Tanpa installer (misal di macOS/dev machine), cukup:

```bash
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
cp config.yml.example config.yml
```

> **Note:** BookingKAI memakai dua tahap: `curl_cffi` (impersonate acak dari
> `chrome124`/`chrome120`/`safari17_2_ios`, tanpa browser sama sekali) lebih dulu, lalu
> [Camoufox](https://github.com/daijro/camoufox) — Firefox anti-fingerprint —
> hanya kalau tahap pertama gagal. Tahap 1 tidak menjalankan browser sama
> sekali (cepat, hemat RAM); Camoufox baru diluncurkan saat benar-benar
> dibutuhkan dan menambal fingerprint di level C++, bukan lewat JS injection.
> CAPTCHA interaktif tetap tidak dipecahkan otomatis; bot melakukan backoff,
> mengirim notifikasi Telegram, dan mungkin membutuhkan intervensi manual.

## Configuration

Edit `config.yml`:

```yaml
telegram:
  bot_token: "YOUR_BOT_TOKEN"
  chat_id: "YOUR_CHAT_ID"

webhook:
  enabled: false
  port: 8080

# Konfigurasi browser untuk BookingKAI provider (opsional)
# Semua field opsional. chromium_path/user_data_dir kini tidak dipakai
# (Camoufox berbasis Firefox); dipertahankan agar config lama tetap valid.
browser:
  chromium_path: ""   # Kosong = auto-detect dari PATH sistem
  user_data_dir: ".cache/tiket-kereta-notifier/chromium"
  headless: true

trains:
  # Monitor kereta spesifik via banyak provider
  - name: BENGAWAN
    origin: LPN
    destination: CKR
    date: "2026-04-02"
    interval: 300
    notes: "Pulang kampung"
    providers:
      - traveloka
      - tiketkai

  # Wildcard: monitor SEMUA kereta di rute ini (tidak filter nama)
  # Tanda petik tidak wajib di YAML: `name: any` dan `name: "any"` identik
  - name: any           # atau: name: "any" atau name: "*"
    origin: GMR
    destination: YK
    date: "2026-04-05"
    interval: 300
    providers:
      - traveloka

  # Filter harga: hanya notif jika tersedia dengan harga <= Rp 350.000
  - name: BOGOWONTO
    origin: LPN
    destination: GMR
    date: "2026-04-05"
    interval: 300
    max_price: 350000
    providers:
      - tiketkai

  # Filter jam keberangkatan: hanya notif jika kereta berangkat jam 06:00–14:59
  # Hanya berlaku untuk provider bookingkai
  - name: BENGAWAN
    origin: LPN
    destination: CKR
    date: "2026-04-02"
    interval: 300
    min_departure_hour: 6   # berangkat >= jam 06:00
    max_departure_hour: 14  # berangkat <= jam 14:59
    providers:
      - bookingkai

  # Kombinasi: semua kereta di rute, asalkan harganya <= Rp 500.000
  - name: "*"
    origin: GMR
    destination: YK
    date: "2026-04-05"
    interval: 300
    max_price: 500000
    providers:
      - traveloka

  # Kereta dengan provider yang butuh proxy
  - name: ARGO DWIPANGGA
    origin: GMR
    destination: YK
    date: "2026-02-17"
    interval: 300
    providers:
      - traveloka
      - name: tiketcom
        proxy_url: "socks5h://127.0.0.1:40000"
      - name: bookingkai
        proxy_url: "socks5://127.0.0.1:40000"
```

> **Note:** Format lama (single `provider` + `proxy_url`) masih didukung untuk backward compatibility.

### Train Config Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Nama kereta. Gunakan `any` atau `*` untuk semua kereta di rute. Tanda petik opsional (`name: any` = `name: "any"`) |
| `origin` | Yes | Kode stasiun asal |
| `destination` | Yes | Kode stasiun tujuan |
| `date` | Yes | Tanggal (YYYY-MM-DD) |
| `providers` | Yes | Array provider (string atau object dengan `proxy_url`) |
| `interval` | No | Interval check dalam detik (default: 300) |
| `max_price` | No | Harga maksimal dalam Rupiah — hanya notif jika harga ≤ nilai ini (0 = tanpa filter) |
| `min_departure_hour` | No | Jam keberangkatan minimum 0–23 (0 = tanpa filter). **Hanya bookingkai.** |
| `max_departure_hour` | No | Jam keberangkatan maksimum 0–23 (0 = tanpa filter). **Hanya bookingkai.** |
| `notes` | No | Catatan opsional, muncul di `/list` dan notifikasi |

## Usage

```bash
# Jalankan dalam tmux
tmux new -s tiket-bot
.venv/bin/python main.py -c config.yml

# Detach: Ctrl-b lalu d
# Pasang kembali sesi:
tmux attach -t tiket-bot

# Hentikan dari dalam sesi: Ctrl-c
# Atau dari terminal lain:
tmux kill-session -t tiket-bot
```

## Telegram Commands

| Command | Description |
|---------|-------------|
| `/list [n]` | List semua kereta, atau detail kereta #n |
| `/check [n]` | Check kereta #n (atau semua) |
| `/all <n>` | Tampilkan semua kereta pada route #n (tanpa filter nama/harga/jam) |
| `/toggle <n>` | Pause/resume monitoring kereta #n |
| `/status [n]` | Status dan settings kereta #n (atau summary semua) |
| `/history <n> [count]` | Riwayat check kereta #n |
| `/sleep <n> <menit>` | Pause train #n selama N menit, lalu auto-resume |
| `/sleep <n> 0` | Batalkan sleep train #n, resume sekarang |
| `/help` | Bantuan |

**Contoh:**
```
/list              # Lihat semua kereta
/list 1            # Detail kereta #1 (termasuk max_price, jam berangkat, dll)
/check             # Check semua kereta
/check 1           # Check kereta pertama saja
/all 1             # Tampilkan semua kereta pada route kereta #1
/toggle 3          # Pause/resume kereta #3
/status            # Summary semua kereta (tampilkan jika sedang sleep)
/status 2          # Status detail kereta #2 + semua settings
/history 1 5       # 5 history terakhir kereta pertama
/sleep 1 30        # Pause train #1 selama 30 menit, lalu auto-resume
/sleep 1 0         # Batalkan sleep train #1, resume sekarang
```

### Sleep Mode

`/sleep <index> <menit>` mem-pause train #n selama N menit. Keduanya wajib diisi.

- `/sleep 2 60` → pause train #2 selama 60 menit
- `/sleep 2 0` → batalkan sleep train #2, resume sekarang
- Setiap train punya sleep timer independen — bisa sleep beberapa train sekaligus dengan timer berbeda
- Setelah waktu habis, bot otomatis mengirim pesan konfirmasi dan melanjutkan train tersebut
- `/status` menampilkan train mana saja yang sedang sleep beserta sisa waktu masing-masing

## Notification Format

```
🚂 #3 any
📍 GMR→YK [2026-04-05]
✅ Tersedia! (3 found) via traveloka

• ARGO DWIPANGGA
  💺 120 seats @ Rp 285.000
• BENGAWAN
  💺 45 seats @ Rp 195.000
• BOGOWONTO
  💺 8 seats @ Rp 310.000
```

## Providers

| Provider | API | Notes |
|----------|-----|-------|
| **tiketkai** | TiketKai.com | AES encrypted |
| **traveloka** | Traveloka.com | Direct JSON |
| **tiketcom** | Tiket.com | TLS/JA3 impersonation via [curl_cffi](https://github.com/lexiforest/curl_cffi) (pip package, tanpa binary eksternal), support proxy |
| **bookingkai** | booking.kai.id | Official KAI, dua tahap: `curl_cffi` impersonate acak (tanpa browser) lalu fallback [Camoufox](https://github.com/daijro/camoufox) (Firefox anti-fingerprint, lazy-launch), shared queue (serial), support proxy, filter jam berangkat |

## Troubleshooting

### Train not found on startup
Pastikan nama kereta sesuai dengan yang tampil di provider. Atau gunakan `name: "any"` sementara untuk melihat daftar kereta yang tersedia di rute tersebut.

### Tiket.com blocked by Turnstile
Gunakan proxy via `proxy_url`. `curl_cffi` (dipasang otomatis lewat
`requirements.txt`) sudah meniru TLS/JA3 fingerprint Chrome, jadi biasanya
lolos tanpa proxy — proxy baru dibutuhkan kalau IP-nya sendiri sudah ke-flag.

### BookingKAI: Cloudflare challenge atau CAPTCHA
BookingKAI berjalan headless lewat Camoufox dan tidak memecahkan CAPTCHA
interaktif secara otomatis. Bot akan mengirim notifikasi Telegram dan menunda
retry dengan exponential backoff. Instance Camoufox dipertahankan hidup
selama proses berjalan agar cookie `cf_clearance` tidak hilang antar request.

### Tunnel not accessible
Pastikan `cloudflared` terinstall dan `webhook.enabled: true`.

## License

MIT
