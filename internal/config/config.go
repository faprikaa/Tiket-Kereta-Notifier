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

// ProviderEntry represents a provider in the providers array.
// Supports both simple string ("traveloka") and object ({name: tiketcom, proxy_url: ...}).
type ProviderEntry struct {
	Name     string `yaml:"name"`
	ProxyURL string `yaml:"proxy_url,omitempty"`
}

// UnmarshalYAML handles both string and object forms:
//
//	providers:
//	  - traveloka           # string form
//	  - name: tiketcom      # object form
//	    proxy_url: "..."
func (p *ProviderEntry) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		// Simple string: "traveloka"
		p.Name = value.Value
		return nil
	}
	// Object form
	type plain ProviderEntry
	return value.Decode((*plain)(p))
}

// TrainConfig represents configuration for a single train to monitor
type TrainConfig struct {
	Name        string          `yaml:"name"`
	Origin      string          `yaml:"origin"`
	Destination string          `yaml:"destination"`
	Date        string          `yaml:"date"` // YYYY-MM-DD
	Interval    int             `yaml:"interval"`
	Notes       string          `yaml:"notes,omitempty"`
	Providers   []ProviderEntry `yaml:"providers"`

	// Backward compat: single provider field (deprecated, use providers array)
	Provider string `yaml:"provider,omitempty"`
	ProxyURL string `yaml:"proxy_url,omitempty"`

	// Computed fields (not from YAML)
	IntervalDuration time.Duration `yaml:"-"`
}

// FlatTrainConfig represents a single train × provider combination (internal use)
type FlatTrainConfig struct {
	Name             string
	Origin           string
	Destination      string
	Date             string
	Interval         int
	IntervalDuration time.Duration
	Notes            string
	ProviderName     string
	ProxyURL         string
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

	// FlatTrains is the flattened list: one entry per train × provider
	FlatTrains []FlatTrainConfig `yaml:"-"`
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

// processTrainConfigs computes derived fields and flattens trains
func (c *Config) processTrainConfigs() {
	// Backward compatibility: if old single-provider format, convert to providers array
	for i := range c.Trains {
		if c.Trains[i].Provider != "" && len(c.Trains[i].Providers) == 0 {
			c.Trains[i].Providers = []ProviderEntry{
				{Name: c.Trains[i].Provider, ProxyURL: c.Trains[i].ProxyURL},
			}
		}

		if c.Trains[i].Interval <= 0 {
			c.Trains[i].Interval = 300 // default 5 minutes
		}
		c.Trains[i].IntervalDuration = time.Duration(c.Trains[i].Interval) * time.Second
	}

	// Flatten: one entry per train × provider
	for _, train := range c.Trains {
		for _, prov := range train.Providers {
			c.FlatTrains = append(c.FlatTrains, FlatTrainConfig{
				Name:             train.Name,
				Origin:           train.Origin,
				Destination:      train.Destination,
				Date:             train.Date,
				Interval:         train.Interval,
				IntervalDuration: train.IntervalDuration,
				Notes:            train.Notes,
				ProviderName:     strings.ToLower(prov.Name),
				ProxyURL:         prov.ProxyURL,
			})
		}
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
	for i, train := range c.Trains {
		if err := train.Validate(); err != nil {
			return fmt.Errorf("train #%d: %w", i+1, err)
		}
	}
	if len(c.FlatTrains) == 0 {
		return fmt.Errorf("no provider configured for any train")
	}
	return nil
}

// FlatTrainConfig helper methods

// DateYYYYMMDD returns date in YYYYMMDD format
func (t *FlatTrainConfig) DateYYYYMMDD() string {
	return strings.ReplaceAll(t.Date, "-", "")
}

// DateParts returns day, month, year
func (t *FlatTrainConfig) DateParts() (day, month, year int) {
	parsed, err := time.Parse("2006-01-02", t.Date)
	if err != nil {
		log.Fatalf("Invalid date format for train %s (expected YYYY-MM-DD): %v", t.Name, err)
	}
	return parsed.Day(), int(parsed.Month()), parsed.Year()
}

// TrainConfig validation

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
	if len(t.Providers) == 0 && t.Provider == "" {
		return fmt.Errorf("at least one provider is required for train %s", t.Name)
	}
	return nil
}
