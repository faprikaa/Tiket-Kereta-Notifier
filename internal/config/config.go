package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Provider string

	// Train Config
	TrainName   string
	Origin      string
	Destination string
	Date        string        // YYYY-MM-DD
	Interval    time.Duration // Polling interval

	// Telegram
	TelegramToken   string
	TelegramChatIDs []string

	// Webhook
	UseWebhook  bool
	WebhookPort int
}

func init() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}
}

// Load returns the application configuration
func Load() *Config {
	intervalStr := GetEnv("TRAIN_INTERVAL", "300") // default 5m
	intervalSec, _ := strconv.Atoi(intervalStr)
	if intervalSec <= 0 {
		intervalSec = 300
	}

	webhookPortStr := GetEnv("WEBHOOK_PORT", "8080")
	webhookPort, _ := strconv.Atoi(webhookPortStr)

	chatIDsStr := GetEnv("TELEGRAM_CHAT_ID", "")
	var chatIDs []string
	if chatIDsStr != "" {
		for _, id := range strings.Split(chatIDsStr, ",") {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				chatIDs = append(chatIDs, trimmed)
			}
		}
	}

	return &Config{
		Provider:        GetEnv("PROVIDER", "traveloka"),
		TrainName:       GetEnv("TRAIN_NAME", ""),
		Origin:          GetEnv("TRAIN_ORIGIN", GetEnv("TIKETKAI_ORIGIN", "")),
		Destination:     GetEnv("TRAIN_DESTINATION", GetEnv("TIKETKAI_DESTINATION", "")),
		Date:            GetEnv("TRAIN_DATE", ""),
		Interval:        time.Duration(intervalSec) * time.Second,
		TelegramToken:   GetEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatIDs: chatIDs,
		UseWebhook:      strings.ToLower(GetEnv("USE_WEBHOOK", "false")) == "true",
		WebhookPort:     webhookPort,
	}
}

// GetEnv returns value or default
func GetEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// Validate checks required configuration fields
func (c *Config) Validate() error {
	if c.TelegramToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	if len(c.TelegramChatIDs) == 0 {
		return fmt.Errorf("TELEGRAM_CHAT_ID is required")
	}
	return nil
}

// ValidateTrainConfig checks train-specific configuration
func (c *Config) ValidateTrainConfig() error {
	if c.Origin == "" {
		return fmt.Errorf("TRAIN_ORIGIN is required")
	}
	if c.Destination == "" {
		return fmt.Errorf("TRAIN_DESTINATION is required")
	}
	return nil
}

// DateYYYYMMDD returns date in YYYYMMDD format for Tiket.com
// Exits if TRAIN_DATE is not set
func (c *Config) DateYYYYMMDD() string {
	if c.Date == "" {
		log.Fatal("TRAIN_DATE is required")
	}
	return strings.ReplaceAll(c.Date, "-", "")
}

// DateParts returns day, month, year for Traveloka
// Exits if TRAIN_DATE is not set or invalid
func (c *Config) DateParts() (day, month, year int) {
	if c.Date == "" {
		log.Fatal("TRAIN_DATE is required")
	}
	t, err := time.Parse("2006-01-02", c.Date)
	if err != nil {
		log.Fatalf("Invalid TRAIN_DATE format (expected YYYY-MM-DD): %v", err)
	}
	return t.Day(), int(t.Month()), t.Year()
}
