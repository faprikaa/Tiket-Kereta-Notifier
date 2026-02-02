package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"tiket-kereta-notifier/internal/telegram"
	"tiket-kereta-notifier/internal/tiketkai"
	"tiket-kereta-notifier/internal/traveloka"
	"tiket-kereta-notifier/internal/tunnel"

	"github.com/joho/godotenv"
)

func main() {
	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	// Load .env file
	_ = godotenv.Load()

	// Initialize Telegram
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatIDStr := os.Getenv("TELEGRAM_CHAT_ID")
	var chatIDs []string
	if chatIDStr != "" {
		for _, id := range strings.Split(chatIDStr, ",") {
			trimmed := strings.TrimSpace(id)
			if trimmed != "" {
				chatIDs = append(chatIDs, trimmed)
			}
		}
	}

	if token == "" {
		logger.Error("TELEGRAM_BOT_TOKEN not set")
		os.Exit(1)
	}

	// Initialize telegram package
	telegram.Init(token, chatIDs, logger)

	// Get provider from env or command line args
	provider := os.Getenv("PROVIDER")
	if len(os.Args) > 1 {
		provider = os.Args[1]
	}

	switch provider {
	case "traveloka":
		runTraveloka(logger)
	case "tiketkai":
		tiketkai.Run()
	case "help":
		printHelp()
	default:
		if provider == "" {
			logger.Error("PROVIDER environment variable is required (tiketkai or traveloka)")
			os.Exit(1)
		}
		logger.Error("Unknown provider", "provider", provider)
		os.Exit(1)
	}
}

func runTraveloka(logger *slog.Logger) {
	origin := os.Getenv("TRAIN_ORIGIN")
	destination := os.Getenv("TRAIN_DESTINATION")
	if origin == "" || destination == "" {
		logger.Error("TRAIN_ORIGIN and TRAIN_DESTINATION environment variables are required")
		os.Exit(1)
	}

	// Check webhook mode
	useWebhook := strings.ToLower(os.Getenv("USE_WEBHOOK")) == "true"
	webhookPort := 8080
	if portStr := os.Getenv("WEBHOOK_PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			webhookPort = p
		}
	}

	// Create telegram bot with commands
	bot := telegram.NewBot(logger)

	// Register global commands
	bot.RegisterCommand("/check", func(ctx context.Context, chatID, args string) {
		telegram.SendMessage("🔍 Checking Traveloka for available trains...", chatID)
		traveloka.Search(origin, destination, 16, 2, 2026)
		telegram.SendMessage("✅ Search completed. Check console for results.", chatID)
	})

	bot.RegisterCommand("/status", func(ctx context.Context, chatID, args string) {
		mode := "Polling"
		if useWebhook {
			mode = "Webhook"
		}
		msg := fmt.Sprintf("🚂 Traveloka Provider\n📍 Route: %s → %s\n📡 Mode: %s\n✅ Bot is running", origin, destination, mode)
		telegram.SendMessage(msg, chatID)
	})

	bot.RegisterCommand("/help", func(ctx context.Context, chatID, args string) {
		help := `🚂 Traveloka Bot Commands

/check - Search for available trains
/status - Show bot status
/help - Show this help`
		telegram.SendMessage(help, chatID)
	})

	// Setup context for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if useWebhook {
		runWithWebhook(ctx, logger, bot, webhookPort, origin, destination)
	} else {
		runWithPolling(ctx, logger, bot, origin, destination)
	}
}

func runWithWebhook(ctx context.Context, logger *slog.Logger, bot *telegram.Bot, port int, origin, destination string) {
	// Start webhook server
	webhookServer := telegram.NewWebhookServer(port, bot)
	if err := webhookServer.Start(); err != nil {
		logger.Error("Failed to start webhook server", "error", err)
		os.Exit(1)
	}

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	// Start cloudflared tunnel
	tun := tunnel.New(logger)
	localURL := fmt.Sprintf("http://localhost:%d", port)
	publicURL, err := tun.Start(ctx, localURL)
	if err != nil {
		logger.Error("Failed to start tunnel", "error", err)
		logger.Info("Make sure cloudflared is installed. Falling back to polling mode...")
		runWithPolling(ctx, logger, bot, origin, destination)
		return
	}

	// Set webhook with Telegram
	if err := telegram.SetWebhook(publicURL); err != nil {
		logger.Error("Failed to set webhook", "error", err)
		tun.Stop()
		os.Exit(1)
	}

	// Send startup message
	telegram.SendMessage(fmt.Sprintf("🚂 Traveloka Bot Started!\n📍 Route: %s → %s\n📡 Mode: Webhook\n🌐 URL: %s", origin, destination, publicURL))

	logger.Info("Bot running with webhook", "url", publicURL)

	// Wait for shutdown
	<-ctx.Done()

	logger.Info("Shutting down...")

	// Cleanup
	telegram.SendMessage("🛑 Bot shutting down...")
	telegram.DeleteWebhook()
	tun.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	webhookServer.Stop(shutdownCtx)

	logger.Info("Shutdown complete")
}

func runWithPolling(ctx context.Context, logger *slog.Logger, bot *telegram.Bot, origin, destination string) {
	// Send startup message
	telegram.SendMessage(fmt.Sprintf("🚂 Traveloka Bot Started!\n📍 Route: %s → %s\n📡 Mode: Polling", origin, destination))

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	logger.Info("Traveloka bot started with polling", "origin", origin, "destination", destination)

	for {
		select {
		case <-ctx.Done():
			logger.Info("Shutting down...")
			telegram.SendMessage("🛑 Bot shutting down...")
			return
		case <-ticker.C:
			bot.HandleUpdates(ctx)
		}
	}
}

func printHelp() {
	fmt.Println(`
Train Ticket Notifier

Usage:
  Set PROVIDER in .env file, or run with:
  program [provider]

Providers:
  tiketkai   - Run TiketKai.com train checker with Telegram bot
  traveloka  - Run Traveloka train search with Telegram bot
  help       - Show this help message

Environment Variables:
  PROVIDER           - Provider to use (tiketkai or traveloka)
  TELEGRAM_BOT_TOKEN - Telegram bot token
  TELEGRAM_CHAT_ID   - Telegram chat ID(s), comma-separated
  TRAIN_ORIGIN       - Origin station code
  TRAIN_DESTINATION  - Destination station code
  TRAIN_DATE         - Travel date (YYYY-MM-DD)
  USE_WEBHOOK        - Use webhook mode (true/false, default: false)
  WEBHOOK_PORT       - Local webhook server port (default: 8080)

Webhook Mode:
  When USE_WEBHOOK=true, the bot will:
  1. Start a local HTTP server
  2. Create a Cloudflare tunnel (requires cloudflared installed)
  3. Set Telegram webhook to the tunnel URL
`)
}
