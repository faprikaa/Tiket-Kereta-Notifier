# 🚂 Train Ticket Notifier

Bot Telegram untuk monitoring ketersediaan tiket kereta api dari TiketKai, Traveloka, Tiket.com, dan BookingKAI (official).

## Features

- ✅ **Multi-Train Monitoring** - Monitor banyak kereta sekaligus
- ✅ **Per-Train Provider** - Setiap kereta bisa pakai provider berbeda
- ✅ **Per-Train Proxy** - Setiap kereta (tiketcom/bookingkai) bisa pakai proxy berbeda
- ✅ **Browser Queue** - BookingKAI requests diproses serial via shared queue (anti rate-limit)
- ✅ **Wildcard Train Name** - Pakai `"any"` / `"*"` untuk monitor semua kereta di rute
- ✅ **Filter Harga** - Filter tiket berdasarkan harga maksimal (Rupiah)
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

> **Note:** BookingKAI provider menggunakan headless Chrome (go-rod) untuk bypass Cloudflare. Chrome/Chromium akan otomatis di-download jika belum ada.

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
| `notes` | No | Catatan opsional, muncul di `/list` dan notifikasi |

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
| `/list [n]` | List semua kereta, atau detail kereta #n |
| `/check [n]` | Check kereta #n (atau semua) |
| `/all <n>` | Tampilkan semua kereta pada route #n (tanpa filter nama) |
| `/toggle <n>` | Pause/resume monitoring kereta #n |
| `/status [n]` | Status detail kereta #n (atau summary) |
| `/history <n> [count]` | Riwayat check kereta #n |
| `/help` | Bantuan |

**Contoh:**
```
/list              # Lihat semua kereta
/list 1            # Detail kereta #1 (termasuk max_price jika diset)
/check             # Check semua kereta
/check 1           # Check kereta pertama saja
/all 1             # Tampilkan semua kereta pada route kereta #1
/toggle 3          # Pause/resume kereta #3
/status 2          # Status detail kereta kedua
/history 1 5       # 5 history terakhir kereta pertama
```

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
| **tiketcom** | Tiket.com | Butuh curl_chrome110, support proxy |
| **bookingkai** | booking.kai.id | Official KAI, headless Chrome (go-rod), Cloudflare bypass, shared queue (serial), support proxy |

## Troubleshooting

### Train not found on startup
Pastikan nama kereta sesuai dengan yang tampil di provider. Atau gunakan `name: "any"` sementara untuk melihat daftar kereta yang tersedia di rute tersebut.

### Tiket.com blocked by Turnstile
Gunakan proxy via `proxy_url` atau pastikan `curl_chrome110` terinstall.

### Tunnel not accessible
Pastikan `cloudflared` terinstall dan `webhook.enabled: true`.

## License

MIT
