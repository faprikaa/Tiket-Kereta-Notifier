package config

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// TrainConfig represents configuration for a single train to monitor
type TrainConfig struct {
	Name        string `yaml:"name"`
	Origin      string `yaml:"origin"`
	Destination string `yaml:"destination"`
	Date        string `yaml:"date"` // YYYY-MM-DD
	Provider    string `yaml:"provider"`
	Interval    int    `yaml:"interval"` // seconds
	ProxyURL    string `yaml:"proxy_url,omitempty"`

	// Computed fields (not from YAML)
	IntervalDuration time.Duration `yaml:"-"`
}

// TelegramConfig holds Telegram bot settings
type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	ChatID   string `yaml:"chat_id"`
}

// WebhookConfig holds webhook settings
type WebhookConfig struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

// Config represents the full application configuration
type Config struct {
	Telegram TelegramConfig `yaml:"telegram"`
	Webhook  WebhookConfig  `yaml:"webhook"`
	Trains   []TrainConfig  `yaml:"trains"`
}

var configPath string

func init() {
	// Define -config / -c flag
	flag.StringVar(&configPath, "config", "config.yml", "Path to YAML config file")
	flag.StringVar(&configPath, "c", "config.yml", "Path to YAML config file (shorthand)")
}

// Load returns the application configuration from YAML file
func Load() *Config {
	// Parse flags if not already parsed
	if !flag.Parsed() {
		flag.Parse()
	}

	cfg := &Config{}

	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Failed to read config file %s: %v", configPath, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		log.Fatalf("Failed to parse YAML config: %v", err)
	}

	// Process train configs
	cfg.processTrainConfigs()

	return cfg
}

// processTrainConfigs computes derived fields for train configs
func (c *Config) processTrainConfigs() {
	for i := range c.Trains {
		if c.Trains[i].Interval <= 0 {
			c.Trains[i].Interval = 300 // default 5 minutes
		}
		c.Trains[i].IntervalDuration = time.Duration(c.Trains[i].Interval) * time.Second
	}

	// Set default webhook port
	if c.Webhook.Port == 0 {
		c.Webhook.Port = 8080
	}
}

// Validate checks required configuration fields
func (c *Config) Validate() error {
	if c.Telegram.BotToken == "" {
		return fmt.Errorf("telegram.bot_token is required in config.yml")
	}
	if c.Telegram.ChatID == "" {
		return fmt.Errorf("telegram.chat_id is required in config.yml")
	}
	if len(c.Trains) == 0 {
		return fmt.Errorf("at least one train configuration is required")
	}
	return nil
}

// TrainConfig helper methods

// DateYYYYMMDD returns date in YYYYMMDD format
func (t *TrainConfig) DateYYYYMMDD() string {
	return strings.ReplaceAll(t.Date, "-", "")
}

// DateParts returns day, month, year
func (t *TrainConfig) DateParts() (day, month, year int) {
	parsed, err := time.Parse("2006-01-02", t.Date)
	if err != nil {
		log.Fatalf("Invalid date format for train %s (expected YYYY-MM-DD): %v", t.Name, err)
	}
	return parsed.Day(), int(parsed.Month()), parsed.Year()
}

// Validate checks if train config is valid
func (t *TrainConfig) Validate() error {
	if t.Origin == "" {
		return fmt.Errorf("origin is required for train %s", t.Name)
	}
	if t.Destination == "" {
		return fmt.Errorf("destination is required for train %s", t.Name)
	}
	if t.Date == "" {
		return fmt.Errorf("date is required for train %s", t.Name)
	}
	if t.Provider == "" {
		return fmt.Errorf("provider is required for train %s", t.Name)
	}
	return nil
}
