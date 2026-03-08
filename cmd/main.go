package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
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
	// Load config (parses -config flag internally)
	cfg := config.Load()

	// Setup logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Validate config
	if err := cfg.Validate(); err != nil {
		logger.Error("Config validation failed", "error", err)
		os.Exit(1)
	}

	telegram.Init(cfg.Telegram.BotToken, cfg.Telegram.ChatID, logger)

	// Create telegram bot with commands
	tgBot := telegram.NewBot(logger)

	// Setup context for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Initialize and start all train monitors
	providers, err := initAllProviders(ctx, logger, cfg)
	if err != nil {
		logger.Error("Failed to initialize providers", "error", err)
		os.Exit(1)
	}

	if len(providers) == 0 {
		logger.Error("No train monitors configured")
		os.Exit(1)
	}

	// Register Bot Commands for all providers
	bot.RegisterCommands(tgBot, providers, cfg)

	// Start all provider schedulers in background with staggered delays per provider
	// to avoid hitting the same API with multiple requests simultaneously
	var wg sync.WaitGroup
	providerCount := make(map[string]int) // track position per provider type
	staggerDelay := 15 * time.Second      // delay between trains on same provider

	for i, p := range providers {
		wg.Add(1)
		providerType := strings.ToLower(cfg.Trains[i].Provider)
		delay := time.Duration(providerCount[providerType]) * staggerDelay
		providerCount[providerType]++

		go func(provider common.Provider, initialDelay time.Duration) {
			defer wg.Done()
			if initialDelay > 0 {
				logger.Info("Staggering scheduler start",
					"provider", provider.Name(), "delay", initialDelay)
				select {
				case <-time.After(initialDelay):
				case <-ctx.Done():
					return
				}
			}
			provider.StartScheduler(ctx, func(msg string) {
				telegram.SendMessage(msg, cfg.Telegram.ChatID)
			})
		}(p, delay)
	}

	logger.Info("Started train monitors", "count", len(providers))

	// Initial validation: check if all configured trains actually exist
	logger.Info("Validating configured trains...")
	if err := validateTrainsExist(ctx, logger, providers, cfg); err != nil {
		logger.Error("Train validation failed", "error", err)
		os.Exit(1)
	}
	logger.Info("✅ All configured trains validated successfully")

	runBot(ctx, logger, cfg, tgBot)

	// Wait for all schedulers to finish
	wg.Wait()
}

// initAllProviders creates providers for each train config
func initAllProviders(ctx context.Context, logger *slog.Logger, cfg *config.Config) ([]common.Provider, error) {
	var providers []common.Provider

	for i, trainCfg := range cfg.Trains {
		// Validate train config
		if err := trainCfg.Validate(); err != nil {
			return nil, fmt.Errorf("train #%d: %w", i+1, err)
		}

		provider, err := initProviderForTrain(ctx, logger, &trainCfg, i+1)
		if err != nil {
			return nil, fmt.Errorf("train %s: %w", trainCfg.Name, err)
		}

		providers = append(providers, provider)
		logger.Info("Initialized train monitor",
			"train", trainCfg.Name,
			"provider", trainCfg.Provider,
			"route", fmt.Sprintf("%s → %s", trainCfg.Origin, trainCfg.Destination),
			"date", trainCfg.Date,
			"interval", trainCfg.IntervalDuration,
		)
	}

	return providers, nil
}

// initProviderForTrain creates a provider for a single train config
func initProviderForTrain(ctx context.Context, logger *slog.Logger, trainCfg *config.TrainConfig, index int) (common.Provider, error) {
	switch strings.ToLower(trainCfg.Provider) {
	case "tiketkai":
		if err := tiketkai.Init(ctx); err != nil {
			return nil, fmt.Errorf("TiketKai init failed: %w", err)
		}
		return tiketkai.NewProvider(
			logger,
			trainCfg.Origin,
			trainCfg.Destination,
			trainCfg.Date,
			trainCfg.Name,
			trainCfg.IntervalDuration,
			trainCfg.ProxyURL,
			index,
			trainCfg.Notes,
		), nil

	case "traveloka":
		day, month, year := trainCfg.DateParts()
		return traveloka.NewProvider(
			logger,
			trainCfg.Origin,
			trainCfg.Destination,
			day, month, year,
			trainCfg.Name,
			trainCfg.IntervalDuration,
			trainCfg.ProxyURL,
			index,
			trainCfg.Notes,
		), nil

	case "tiketcom":
		provider := tiketcom.NewProvider(
			logger,
			trainCfg.Origin,
			trainCfg.Destination,
			trainCfg.DateYYYYMMDD(),
			trainCfg.Name,
			trainCfg.IntervalDuration,
			trainCfg.ProxyURL,
			index,
			trainCfg.Notes,
		)

		// Test connection
		logger.Info("Testing Tiket.com connection...", "train", trainCfg.Name)
		if err := testTiketcomConnection(ctx, provider, logger); err != nil {
			return nil, err
		}
		return provider, nil

	default:
		return nil, fmt.Errorf("unknown provider '%s' (use: tiketkai, traveloka, tiketcom)", trainCfg.Provider)
	}
}

// testTiketcomConnection tests Tiket.com API and checks for captcha blocks
func testTiketcomConnection(ctx context.Context, provider *tiketcom.Provider, logger *slog.Logger) error {
	trains, err := provider.Search(ctx)
	if err != nil {
		errStr := strings.ToLower(err.Error())
		if containsAny(errStr, "turnstile", "captcha", "challenge", "ray-id", "cloudflare") {
			return fmt.Errorf("⚠️ Tiket.com is blocked by Turnstile/Captcha! Try using proxy_url in config")
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

// validateTrainsExist performs initial search for each configured train to verify it exists.
// Trains are grouped by (name, origin, destination, date) so each unique route is only
// validated once. If one provider errors, another provider in the same group is tried.
func validateTrainsExist(ctx context.Context, logger *slog.Logger, providers []common.Provider, cfg *config.Config) error {
	// Group key: name|origin|destination|date
	type trainKey struct {
		Name, Origin, Destination, Date string
	}

	// Collect provider indices per group
	groups := make(map[trainKey][]int)
	var groupOrder []trainKey // preserve order
	for i, trainCfg := range cfg.Trains {
		if trainCfg.Name == "" {
			logger.Info("No train name filter, skipping validation", "route", fmt.Sprintf("%s → %s", trainCfg.Origin, trainCfg.Destination))
			continue
		}
		key := trainKey{
			Name:        strings.ToLower(trainCfg.Name),
			Origin:      trainCfg.Origin,
			Destination: trainCfg.Destination,
			Date:        trainCfg.Date,
		}
		if _, exists := groups[key]; !exists {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], i)
	}

	// Validate each group once
	for _, key := range groupOrder {
		indices := groups[key]
		trainCfg := cfg.Trains[indices[0]] // representative config

		validated := false
		var lastErr error

		for _, idx := range indices {
			provider := providers[idx]
			providerName := cfg.Trains[idx].Provider

			logger.Info("Validating train...", "train", trainCfg.Name, "provider", providerName)

			trains, err := provider.Search(ctx)
			if err != nil {
				logger.Warn("Validation failed, trying next provider",
					"train", trainCfg.Name, "provider", providerName, "error", err)
				lastErr = err
				continue
			}

			// Check if any result matches the configured train name
			target := strings.ToLower(trainCfg.Name)
			for _, t := range trains {
				if strings.Contains(strings.ToLower(t.Name), target) {
					logger.Info("✓ Train found", "train", trainCfg.Name, "matched", t.Name,
						"availability", t.Availability, "via", providerName)
					validated = true
					break
				}
			}

			if validated {
				break
			}

			// Train not found in results
			var availableNames []string
			for _, t := range trains {
				availableNames = append(availableNames, t.Name)
			}
			lastErr = fmt.Errorf("train '%s' not found on route %s → %s (date: %s). Available: %v",
				trainCfg.Name, trainCfg.Origin, trainCfg.Destination, trainCfg.Date, availableNames)
		}

		if !validated {
			return fmt.Errorf("failed to validate train %s: %w", trainCfg.Name, lastErr)
		}
	}
	return nil
}

// runBot starts the bot in webhook or polling mode
func runBot(ctx context.Context, logger *slog.Logger, cfg *config.Config, tgBot *telegram.Bot) {
	var t *tunnel.Tunnel

	if cfg.Webhook.Enabled {
		t = tunnel.New(logger)
		go func() {
			publicURL, err := t.Start(ctx, fmt.Sprintf("http://localhost:%d", cfg.Webhook.Port))
			if err != nil {
				logger.Error("Failed to start tunnel", "error", err)
				return
			}
			tgBot.SetWebhook(publicURL + "/webhook")
			telegram.SendMessage(fmt.Sprintf("🚀 Bot started!\nMonitoring %d trains\nWebhook: %s", len(cfg.Trains), publicURL), cfg.Telegram.ChatID)
		}()

		if err := tgBot.StartWebhook(cfg.Webhook.Port, []string{cfg.Telegram.ChatID}); err != nil {
			logger.Error("Webhook server failed", "error", err)
		}
		<-ctx.Done()
	} else {
		telegram.SendMessage(fmt.Sprintf("🚀 Bot started!\nMonitoring %d trains", len(cfg.Trains)), cfg.Telegram.ChatID)
		logger.Info("Bot running in long-polling/manual mode. Press Ctrl+C to exit.")
		<-ctx.Done()
	}

	logger.Info("Shutting down...")

	if cfg.Webhook.Enabled && t != nil {
		tgBot.DeleteWebhook()
		t.Stop()
	}

	time.Sleep(1 * time.Second)
	logger.Info("Shutdown complete")
}
