package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tiket-kereta-notifier/internal/bot"
	"tiket-kereta-notifier/internal/common"
	"tiket-kereta-notifier/internal/config"
	"tiket-kereta-notifier/internal/telegram"
	"tiket-kereta-notifier/internal/tiketkai"
	"tiket-kereta-notifier/internal/traveloka"
	"tiket-kereta-notifier/internal/tunnel"
)

func main() {
	// Load config
	cfg := config.Load()

	// Setup logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if cfg.TelegramToken == "" || len(cfg.TelegramChatIDs) == 0 {
		logger.Error("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required")
		os.Exit(1)
	}

	telegram.Init(cfg.TelegramToken, cfg.TelegramChatIDs, logger)

	// Check webhook mode
	useWebhook := cfg.UseWebhook
	webhookPort := cfg.WebhookPort
	chatIDs := cfg.TelegramChatIDs

	// Create telegram bot with commands
	tgBot := telegram.NewBot(logger)

	// Setup context for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Init Provider Logic
	providerName := cfg.Provider
	if len(os.Args) > 1 {
		providerName = os.Args[1]
	}

	var appProvider common.Provider

	switch strings.ToLower(providerName) {
	case "tiketkai":
		if err := tiketkai.Init(ctx); err != nil {
			logger.Error("Failed to init TiketKai", "error", err)
			os.Exit(1)
		}
		appProvider = tiketkai.NewProvider(logger, cfg.Origin, cfg.Destination, cfg.Date, cfg.TrainName, cfg.Interval)

	case "traveloka":
		if cfg.Origin == "" || cfg.Destination == "" {
			logger.Error("TRAIN_ORIGIN and TRAIN_DESTINATION are required")
			os.Exit(1)
		}

		// Parse date
		day, month, year := 16, 2, 2026 // Fallback
		if cfg.Date != "" {
			if t, err := time.Parse("2006-01-02", cfg.Date); err == nil {
				day, month, year = t.Day(), int(t.Month()), t.Year()
			}
		}

		appProvider = traveloka.NewProvider(logger, cfg.Origin, cfg.Destination, day, month, year, cfg.TrainName, cfg.Interval)

	case "help":
		printHelp()
		return
	default:
		logger.Error("PROVIDER must be 'tiketkai' or 'traveloka'")
		os.Exit(1)
	}

	// Register Generic Bot Commands
	bot.RegisterCommands(tgBot, appProvider)

	// Start Provider Scheduler in Background
	go appProvider.StartScheduler(ctx, func(msg string) {
		// Iterate chatIDs
		for _, chatID := range chatIDs {
			telegram.SendMessage(msg, chatID)
		}
	})

	var t *tunnel.Tunnel
	if useWebhook {
		// Start tunnel in background
		t = tunnel.New(logger)
		go func() {
			publicURL, err := t.Start(ctx, fmt.Sprintf("http://localhost:%d", webhookPort))
			if err != nil {
				logger.Error("Failed to start tunnel", "error", err)
				return
			}
			tgBot.SetWebhook(publicURL + "/webhook")
			// Send startup notification
			telegram.SendMessage(fmt.Sprintf("🚀 Bot started!\nProvider: %s\nWebhook: %s", appProvider.Name(), publicURL))
		}()

		// Start server (non-blocking, runs in goroutine)
		if err := tgBot.StartWebhook(webhookPort, chatIDs); err != nil {
			logger.Error("Webhook server failed", "error", err)
		}

		// Wait for shutdown signal
		<-ctx.Done()
	} else {
		logger.Info("Bot running in long-polling/manual mode. Press Ctrl+C to exit.")
		<-ctx.Done()
	}

	logger.Info("Shutting down...")

	// Cleanup
	if useWebhook && t != nil {
		tgBot.DeleteWebhook()
		t.Stop()
	}

	time.Sleep(1 * time.Second)
	logger.Info("Shutdown complete")
}

func printHelp() {
	fmt.Println(`Tiket Kereta Notifier

Usage:
  go run cmd/main.go [provider]

Providers:
  tiketkai    Monitor TiketKai API (Default)
  traveloka   Monitor Traveloka API

Environment Variables (via .env):
  PROVIDER            Default provider to run
  TRAIN_ORIGIN        Origin station code (e.g., GMR)
  TRAIN_DESTINATION   Destination station code (e.g., YK)
  TRAIN_DATE          Date (YYYY-MM-DD)
  TRAIN_NAME          (Optional) Filter by train name
  TRAIN_INTERVAL      Polling interval in seconds (default 300)
  TELEGRAM_BOT_TOKEN  Telegram Bot Token
  TELEGRAM_CHAT_ID    Telegram Chat ID(s), comma separated
  USE_WEBHOOK         Enable webhook & cloudflared (true/false)
  WEBHOOK_PORT        Port for webhook (default 8080)`)
}
