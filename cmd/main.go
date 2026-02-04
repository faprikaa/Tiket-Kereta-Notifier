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
	"tiket-kereta-notifier/internal/tiketcom"
	"tiket-kereta-notifier/internal/tiketkai"
	"tiket-kereta-notifier/internal/traveloka"
	"tiket-kereta-notifier/internal/tunnel"
)

func main() {
	// Load config
	cfg := config.Load()

	// Setup logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Validate config
	if err := cfg.Validate(); err != nil {
		logger.Error("Config validation failed", "error", err)
		os.Exit(1)
	}

	telegram.Init(cfg.TelegramToken, cfg.TelegramChatIDs, logger)

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

	appProvider, err := initProvider(ctx, logger, cfg, providerName)
	if err != nil {
		logger.Error("Failed to init provider", "provider", providerName, "error", err)
		os.Exit(1)
	}

	// Register Generic Bot Commands
	bot.RegisterCommands(tgBot, appProvider)

	// Start Provider Scheduler in Background
	go appProvider.StartScheduler(ctx, func(msg string) {
		for _, chatID := range cfg.TelegramChatIDs {
			telegram.SendMessage(msg, chatID)
		}
	})

	runBot(ctx, logger, cfg, tgBot, appProvider)
}

// initProvider creates and initializes the appropriate provider
func initProvider(ctx context.Context, logger *slog.Logger, cfg *config.Config, providerName string) (common.Provider, error) {
	switch strings.ToLower(providerName) {
	case "tiketkai":
		if err := tiketkai.Init(ctx); err != nil {
			return nil, fmt.Errorf("TiketKai init failed: %w", err)
		}
		return tiketkai.NewProvider(logger, cfg.Origin, cfg.Destination, cfg.Date, cfg.TrainName, cfg.Interval), nil

	case "traveloka":
		if err := cfg.ValidateTrainConfig(); err != nil {
			return nil, err
		}
		day, month, year := cfg.DateParts()
		return traveloka.NewProvider(logger, cfg.Origin, cfg.Destination, day, month, year, cfg.TrainName, cfg.Interval), nil

	case "tiketcom":
		if err := cfg.ValidateTrainConfig(); err != nil {
			return nil, err
		}
		provider := tiketcom.NewProvider(logger, cfg.Origin, cfg.Destination, cfg.DateYYYYMMDD(), cfg.TrainName, cfg.Interval)

		// Test connection and check for Turnstile/Captcha
		logger.Info("Testing Tiket.com connection...")
		if err := testTiketcomConnection(ctx, provider, logger); err != nil {
			return nil, err
		}
		return provider, nil

	case "help":
		printHelp()
		os.Exit(0)
		return nil, nil

	default:
		return nil, fmt.Errorf("PROVIDER must be 'tiketkai', 'traveloka', or 'tiketcom'")
	}
}

// testTiketcomConnection tests Tiket.com API and checks for captcha blocks
func testTiketcomConnection(ctx context.Context, provider *tiketcom.Provider, logger *slog.Logger) error {
	trains, err := provider.Search(ctx)
	if err != nil {
		errStr := strings.ToLower(err.Error())
		if containsAny(errStr, "turnstile", "captcha", "challenge", "ray-id", "cloudflare") {
			return fmt.Errorf("⚠️ Tiket.com is blocked by Turnstile/Captcha! Try using a proxy via TIKETCOM_PROXY_URL")
		}
		return fmt.Errorf("connection test failed: %w", err)
	}
	logger.Info("✅ Tiket.com connection OK", "trains_found", len(trains))
	return nil
}

// containsAny checks if s contains any of the substrings
func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// runBot starts the bot in webhook or polling mode
func runBot(ctx context.Context, logger *slog.Logger, cfg *config.Config, tgBot *telegram.Bot, appProvider common.Provider) {
	var t *tunnel.Tunnel

	if cfg.UseWebhook {
		t = tunnel.New(logger)
		go func() {
			publicURL, err := t.Start(ctx, fmt.Sprintf("http://localhost:%d", cfg.WebhookPort))
			if err != nil {
				logger.Error("Failed to start tunnel", "error", err)
				return
			}
			tgBot.SetWebhook(publicURL + "/webhook")
			telegram.SendMessage(fmt.Sprintf("🚀 Bot started!\nProvider: %s\nWebhook: %s", appProvider.Name(), publicURL))
		}()

		if err := tgBot.StartWebhook(cfg.WebhookPort, cfg.TelegramChatIDs); err != nil {
			logger.Error("Webhook server failed", "error", err)
		}
		<-ctx.Done()
	} else {
		logger.Info("Bot running in long-polling/manual mode. Press Ctrl+C to exit.")
		<-ctx.Done()
	}

	logger.Info("Shutting down...")

	if cfg.UseWebhook && t != nil {
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
  tiketcom    Monitor Tiket.com API (uses curl_chrome110)

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
  WEBHOOK_PORT        Port for webhook (default 8080)
  TIKETCOM_PROXY_URL  (Optional) SOCKS5 proxy for Tiket.com`)
}
