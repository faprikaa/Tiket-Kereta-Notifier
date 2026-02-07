# 🚂 Train Ticket Notifier

Bot Telegram untuk monitoring ketersediaan tiket kereta api dari TiketKai, Traveloka, dan Tiket.com.

## Features

- ✅ **Multi-Train Monitoring** - Monitor banyak kereta sekaligus
- ✅ **Per-Train Provider** - Setiap kereta bisa pakai provider berbeda
- ✅ **Per-Train Proxy** - Setiap kereta (tiketcom) bisa pakai proxy berbeda
- ✅ **YAML Config** - Konfigurasi mudah via file YAML
- ✅ **Startup Validation** - Verifikasi kereta ada sebelum monitoring
- ✅ **Telegram Bot** - Notifikasi real-time via Telegram
- ✅ **Webhook Mode** - Menggunakan Cloudflare Tunnel
- ✅ **Smart Notification** - Kirim notifikasi hanya jika ada kursi tersedia

## Installation

```bash
git clone https://github.com/yourusername/Tiket-Kereta-Notifier.git
cd Tiket-Kereta-Notifier
go mod tidy
```

### Dependencies

**Cloudflared** (untuk webhook mode):
```bash
# Windows
scoop install cloudflared
# atau
winget install Cloudflare.cloudflared

# macOS
brew install cloudflared

# Linux / Manual
# Download dari https://github.com/cloudflare/cloudflared/releases
```

**curl-impersonate** (untuk Tiket.com provider):
```bash
# Download dari https://github.com/lwthiker/curl-impersonate/releases
# Pastikan curl_chrome110 ada di PATH
```

## Configuration

Edit `config.yml`:

```yaml
telegram:
  bot_token: "YOUR_BOT_TOKEN"
  chat_id: "YOUR_CHAT_ID"

webhook:
  enabled: false
  port: 8080

trains:
  # Monitor BENGAWAN via TiketKai
  - name: BENGAWAN
    origin: LPN
    destination: CKR
    date: "2026-02-16"
    provider: tiketkai
    interval: 180

  # Monitor ARGO via Traveloka
  - name: ARGO DWIPANGGA
    origin: GMR
    destination: YK
    date: "2026-02-17"
    provider: traveloka
    interval: 300

  # Monitor via Tiket.com dengan proxy
  - name: GAJAYANA
    origin: BD
    destination: SGU
    date: "2026-02-18"
    provider: tiketcom
    proxy_url: "socks5h://127.0.0.1:40000"
    interval: 120
```

### Train Config Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Nama kereta (filter/target) |
| `origin` | Yes | Kode stasiun asal |
| `destination` | Yes | Kode stasiun tujuan |
| `date` | Yes | Tanggal (YYYY-MM-DD) |
| `provider` | Yes | `tiketkai`, `traveloka`, atau `tiketcom` |
| `interval` | No | Interval check dalam detik (default: 300) |
| `proxy_url` | No | SOCKS5 proxy untuk tiketcom |

## Usage

```bash
# Pakai config.yml default
go run cmd/main.go

# Pakai custom config file
go run cmd/main.go -config production.yml
go run cmd/main.go -c myconfig.yml
```

## Telegram Commands

| Command | Description |
|---------|-------------|
| `/list` | List semua kereta yang dikonfigurasi |
| `/check [n]` | Check kereta #n (atau semua jika tanpa arg) |
| `/status [n]` | Status detail kereta #n (atau summary) |
| `/history [n] [count]` | Riwayat check kereta #n |
| `/help` | Bantuan |

**Contoh:**
```
/list              # Lihat semua kereta
/check             # Check semua kereta
/check 1           # Check kereta pertama saja
/status 2          # Status detail kereta kedua
/history 1 5       # 5 history terakhir kereta pertama
```

## Providers

| Provider | API | Notes |
|----------|-----|-------|
| **tiketkai** | TiketKai.com | AES encrypted |
| **traveloka** | Traveloka.com | Direct JSON |
| **tiketcom** | Tiket.com | Butuh curl_chrome110, support proxy |

## Troubleshooting

### Train not found on startup
Pastikan nama kereta sesuai dengan yang tampil di provider. Jalankan tanpa filter `name` dulu untuk lihat kereta yang tersedia.

### Tiket.com blocked by Turnstile
Gunakan proxy via `proxy_url` atau pastikan `curl_chrome110` terinstall.

### Tunnel not accessible
Pastikan `cloudflared` terinstall dan webhook.enabled = true.

## License

MIT
