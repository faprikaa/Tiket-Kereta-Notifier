# 🚂 Train Ticket Notifier

Bot Telegram untuk monitoring ketersediaan tiket kereta api dari TiketKai dan Traveloka.

## Features

- ✅ **Multi-Provider** - Support TiketKai.com dan Traveloka
- ✅ **Telegram Bot** - Notifikasi real-time via Telegram
- ✅ **Webhook Mode** - Menggunakan Cloudflare Tunnel (no polling!)
- ✅ **Auto Check** - Monitoring otomatis dengan interval kustom
- ✅ **Target Train Filter** - Monitor kereta spesifik berdasarkan nama
- ✅ **Smart Notification** - Hanya kirim notifikasi ketika ada kursi tersedia
- ✅ **Startup Notification** - Notifikasi saat bot berhasil start

## Installation

```bash
# Clone repo
git clone https://github.com/yourusername/Tiket-Kereta-Notifier.git
cd Tiket-Kereta-Notifier

# Install dependencies
go mod tidy

# Copy config
cp .env.example .env

# Edit .env dengan konfigurasi kamu
```

### Install Cloudflared (untuk webhook mode)

```bash
# Windows (scoop)
scoop install cloudflared

# Windows (winget)
winget install Cloudflare.cloudflared

# macOS
brew install cloudflared

# Linux
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o cloudflared
chmod +x cloudflared
sudo mv cloudflared /usr/local/bin/
```

## Configuration

Edit file `.env`:

```env
# Provider: tiketkai atau traveloka
PROVIDER=tiketkai

# Telegram Bot
TELEGRAM_BOT_TOKEN=your_bot_token_here
TELEGRAM_CHAT_ID=your_chat_id_here

# Webhook Mode (requires cloudflared)
USE_WEBHOOK=true
WEBHOOK_PORT=8081

# Train Configuration
TRAIN_NAME=JAYAKARTA        # Target kereta (filter untuk /check dan scheduler)
TRAIN_ORIGIN=LPN            # Kode stasiun asal
TRAIN_DESTINATION=CKR       # Kode stasiun tujuan
TRAIN_DATE=2026-02-16       # Tanggal keberangkatan (YYYY-MM-DD)
TRAIN_INTERVAL=60           # Interval check dalam detik
```

## Usage

```bash
# Run dengan provider dari .env
go run ./cmd/main.go

# Atau override provider via command line
go run ./cmd/main.go tiketkai
go run ./cmd/main.go traveloka
go run ./cmd/main.go help
```

## Webhook vs Polling Mode

| Mode | Config | Description |
|------|--------|-------------|
| **Webhook** | `USE_WEBHOOK=true` | Menggunakan Cloudflare Tunnel, lebih responsif |
| **Polling** | `USE_WEBHOOK=false` | Fallback, tidak perlu cloudflared |

Webhook mode akan:
1. Start HTTP server di `localhost:WEBHOOK_PORT`
2. Spawn `cloudflared tunnel` untuk dapatkan URL publik
3. Set Telegram webhook ke URL tersebut
4. Terima updates langsung via HTTP POST
5. Kirim notifikasi startup ke Telegram

## Telegram Commands

| Command | Description |
|---------|-------------|
| `/check` | Check target train (berdasarkan TRAIN_NAME) |
| `/list` | List semua kereta tersedia (tanpa filter) |
| `/status` | Status bot dan provider |
| `/help` | Bantuan |

## How It Works

### Scheduler
- Bot akan check ketersediaan kereta secara berkala sesuai `TRAIN_INTERVAL`
- Hanya kereta dengan nama yang cocok dengan `TRAIN_NAME` yang akan dimonitor
- Notifikasi **hanya dikirim jika ada kursi tersedia** (bukan 0)

### Commands
- `/check` - Menggunakan filter `TRAIN_NAME`, hanya tampilkan target kereta
- `/list` - Tampilkan **semua** kereta tanpa filter

## Project Structure

```
├── cmd/
│   ├── main.go              # Entry point
│   └── test.go              # Test utilities
├── internal/
│   ├── bot/                 # Bot commands registration
│   ├── common/              # Shared interfaces (Provider, Train)
│   ├── config/              # Environment loading
│   ├── telegram/            # Telegram bot + webhook
│   ├── tiketkai/            # TiketKai provider
│   ├── traveloka/           # Traveloka provider
│   └── tunnel/              # Cloudflare tunnel management
├── .env.example             # Template config
├── .gitignore
└── go.mod
```

## Providers

### TiketKai
- API: `https://sc-microservice-tiketkai.bmsecure.id`
- Menggunakan AES encryption untuk payload
- Support filter berdasarkan nama kereta

### Traveloka
- API: `https://www.traveloka.com/api/v2/train`
- Direct JSON API
- Support filter berdasarkan nama kereta

## Troubleshooting

### API Error RC: 89 (TiketKai)
Payload atau headers tidak sesuai. Pastikan menggunakan versi terbaru.

### Context Deadline Exceeded
API timeout. Timeout sudah diset 30 detik, coba lagi.

### Tunnel Not Accessible
Cloudflare tunnel gagal. Pastikan `cloudflared` terinstall dan bisa diakses.

## Credits

🤖 **This project was entirely created by Claude** via Antigravity IDE - from code architecture, implementation, to documentation.

👨‍💻 **Project directed by me** - requirements, design decisions, and quality assurance.

## License

MIT
