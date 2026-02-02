# 🚂 Train Ticket Notifier

Bot Telegram untuk monitoring ketersediaan tiket kereta api dari TiketKai dan Traveloka.

## Features

- ✅ **Multi-Provider** - Support TiketKai.com dan Traveloka
- ✅ **Telegram Bot** - Notifikasi real-time via Telegram
- ✅ **Webhook Mode** - Menggunakan Cloudflare Tunnel (no polling!)
- ✅ **Auto Check** - Monitoring otomatis dengan interval kustom
- ✅ **Filter** - Filter berdasarkan kelas dan harga maksimal

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
WEBHOOK_PORT=8080

# Train Configuration
TRAIN_NAME=BENGAWAN
TRAIN_ORIGIN=LPN
TRAIN_DESTINATION=CKR
TRAIN_DATE=2026-02-16
TRAIN_INTERVAL=180
TRAIN_MAX_PRICE=300000
```

## Usage

```bash
# Run dengan provider dari .env
go run ./cmd/...

# Atau override via command line
go run ./cmd/... tiketkai
go run ./cmd/... traveloka
go run ./cmd/... help
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

## Telegram Commands

### TiketKai
| Command | Description |
|---------|-------------|
| `/check` | Check semua kereta sekarang |
| `/list [index]` | List kereta tersedia |
| `/status` | Status bot |
| `/interval <index> <minutes>` | Set interval check |
| `/toggle <index>` | Enable/disable train |
| `/filter <index> <classes>` | Set filter kelas |
| `/maxprice <index> <price>` | Set harga maksimal |
| `/stats` | Statistik |
| `/help` | Bantuan |

### Traveloka
| Command | Description |
|---------|-------------|
| `/check` | Search tiket |
| `/status` | Status bot |
| `/help` | Bantuan |

## Project Structure

```
├── cmd/
│   └── main.go              # Entry point
├── internal/
│   ├── config/              # Environment loading
│   ├── telegram/            # Shared Telegram bot + webhook
│   ├── tiketkai/            # TiketKai provider
│   ├── traveloka/           # Traveloka provider
│   └── tunnel/              # Cloudflare tunnel
├── .env.example             # Template config
└── go.mod
```

## Credits

🤖 **This project was entirely created by Claude** via Antigravity IDE - from code architecture, implementation, to documentation.

👨‍💻 **Project directed by me** - requirements, design decisions, and quality assurance.

## License

MIT
