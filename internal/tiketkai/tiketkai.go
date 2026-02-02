package tiketkai

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tiket-kereta-notifier/internal/common"
)

const (
	APIUrl = "https://sc-microservice-tiketkai.bmsecure.id/train/search"
	Key    = "78455d8581f1fc41"
	IV     = "34f1cdf17d1aacb8"
)

// Provider implements common.Provider for TiketKai
type Provider struct {
	Logger        *slog.Logger
	Origin        string
	Destination   string
	Date          string // YYYY-MM-DD
	TrainName     string // Target train name filter
	CheckInterval time.Duration
}

// NewProvider creates a new TiketKai provider
func NewProvider(logger *slog.Logger, origin, dest, date, trainName string, interval time.Duration) *Provider {
	return &Provider{
		Logger:        logger,
		Origin:        origin,
		Destination:   dest,
		Date:          date,
		TrainName:     trainName,
		CheckInterval: interval,
	}
}

func (p *Provider) Name() string {
	return "TiketKai"
}

func (p *Provider) Search(ctx context.Context) ([]common.Train, error) {
	if p.Origin == "" || p.Destination == "" {
		return nil, fmt.Errorf("origin and destination required")
	}

	p.Logger.Info("Searching TiketKai", "origin", p.Origin, "dest", p.Destination, "date", p.Date)

	payload := map[string]interface{}{
		"app":         "TKAI",
		"via":         "mobile_web",
		"date":        p.Date,
		"destination": p.Destination,
		"origin":      p.Origin,
		"productCode": "WKAI",
		"deviceInfo": map[string]interface{}{
			"model":       "Windows NT 10.0",
			"versionCode": 10037,
			"versionName": "1.3.0",
		},
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	encrypted, err := encryptAESBase64(string(jsonBytes), Key, IV)
	if err != nil {
		return nil, fmt.Errorf("encrypt failed: %w", err)
	}

	// Send encrypted data directly (not wrapped in JSON)
	req, err := http.NewRequestWithContext(ctx, "POST", APIUrl, bytes.NewBufferString(encrypted))
	if err != nil {
		return nil, err
	}
	req.Header.Add("accept", "application/json, text/plain, */*")
	req.Header.Add("accept-language", "en-US,en;q=0.9")
	req.Header.Add("content-type", "text/plain")
	req.Header.Add("origin", "https://m.tiketkai.com")
	req.Header.Add("priority", "u=1, i")
	req.Header.Add("referer", "https://m.tiketkai.com/")
	req.Header.Add("sec-ch-ua", "\"Not(A:Brand\";v=\"8\", \"Chromium\";v=\"144\", \"Microsoft Edge\";v=\"144\"")
	req.Header.Add("sec-ch-ua-mobile", "?0")
	req.Header.Add("sec-ch-ua-platform", "\"Windows\"")
	req.Header.Add("sec-fetch-dest", "empty")
	req.Header.Add("sec-fetch-mode", "cors")
	req.Header.Add("sec-fetch-site", "cross-site")
	req.Header.Add("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36 Edg/144.0.0.0")

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: %d", resp.StatusCode)
	}

	// Read full body for debugging
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result struct {
		RC   string          `json:"rc"`
		Data json.RawMessage `json:"data"` // Can be array or string
	}

	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		p.Logger.Error("Failed to parse response", "body", string(bodyBytes))
		return nil, err
	}

	if result.RC != "00" {
		p.Logger.Error("TiketKai API error", "rc", result.RC, "body", string(bodyBytes))
		return nil, fmt.Errorf("API RC: %s", result.RC)
	}

	// Check if data is a string (error message) or array
	if len(result.Data) > 0 && result.Data[0] == '"' {
		// Data is a string, likely an error message
		var msg string
		json.Unmarshal(result.Data, &msg)
		return nil, fmt.Errorf("API message: %s", msg)
	}

	// Parse data as array
	var trainData []struct {
		TrainName     string `json:"trainName"`
		TrainNumber   string `json:"trainNumber"`
		DepartureTime string `json:"departureTime"`
		ArrivalTime   string `json:"arrivalTime"`
		Seats         []struct {
			Class        string      `json:"class"`
			Availability interface{} `json:"availability"`
			PriceAdult   interface{} `json:"priceAdult"`
		} `json:"seats"`
	}

	if err := json.Unmarshal(result.Data, &trainData); err != nil {
		return nil, fmt.Errorf("failed to parse train data: %w", err)
	}

	var trains []common.Train
	for _, tr := range trainData {
		seatsAvailable := "0"
		availStatus := "FULL"
		minPrice := ""

		for _, s := range tr.Seats {
			// Check availability
			isAvail := false
			seatCount := "0"

			switch v := s.Availability.(type) {
			case float64:
				if v > 0 {
					isAvail = true
					seatCount = fmt.Sprintf("%.0f", v)
				}
			case string:
				if v != "0" && v != "Habis" && v != "" {
					isAvail = true
					seatCount = v
				}
			}

			if isAvail {
				seatsAvailable = seatCount
				availStatus = "AVAILABLE"

				// Price
				price := "0"
				switch v := s.PriceAdult.(type) {
				case float64:
					price = fmt.Sprintf("%.0f", v)
				case string:
					price = v
				}
				minPrice = price // Use first available class price
				break            // Take the first available class
			}
		}

		if availStatus == "AVAILABLE" {
			trains = append(trains, common.Train{
				Name:          tr.TrainName,
				Class:         "ECO", // Simplified
				Price:         minPrice,
				DepartureTime: tr.DepartureTime,
				ArrivalTime:   tr.ArrivalTime,
				Availability:  availStatus,
				SeatsLeft:     seatsAvailable,
			})
		}
	}

	// Filter by TrainName if configured
	if p.TrainName != "" {
		var filtered []common.Train
		target := strings.ToLower(p.TrainName)
		for _, t := range trains {
			if strings.Contains(strings.ToLower(t.Name), target) {
				filtered = append(filtered, t)
			}
		}
		return filtered, nil
	}

	return trains, nil
}

// SearchAll returns all trains (no TrainName filter)
func (p *Provider) SearchAll(ctx context.Context) ([]common.Train, error) {
	// Temporarily clear TrainName to get all trains
	savedName := p.TrainName
	p.TrainName = ""
	defer func() { p.TrainName = savedName }()
	return p.Search(ctx)
}

func (p *Provider) StartScheduler(ctx context.Context, notifyFunc func(string)) {
	interval := p.CheckInterval
	if interval == 0 {
		interval = 1 * time.Minute
	}

	p.Logger.Info("Starting TiketKai Polling", "interval", interval, "target", p.TrainName)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			trains, err := p.Search(ctx)
			if err != nil {
				p.Logger.Error("Poll failed", "error", err)
				continue
			}

			// Filter for trains with available seats only
			var availableTrains []common.Train
			for _, t := range trains {
				if t.SeatsLeft != "0" && t.SeatsLeft != "" {
					availableTrains = append(availableTrains, t)
				}
			}

			if len(availableTrains) > 0 {
				msg := fmt.Sprintf("🚂 Target Train Available! (%d found)\n", len(availableTrains))
				for _, t := range availableTrains {
					msg += fmt.Sprintf("- %s: %s seats (Rp%s)\n", t.Name, t.SeatsLeft, t.Price)
				}
				notifyFunc(msg)
			}
		}
	}
}

// Init handles legacy boilerplate compatibility (now empty)
func Init(ctx context.Context) error {
	return nil
}

// Helpers
func encryptAESBase64(plaintext string, key, iv string) (string, error) {
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", err
	}

	// PKCS7 Padding
	bs := block.BlockSize()
	padding := bs - len(plaintext)%bs
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	plaintextBytes := append([]byte(plaintext), padtext...)

	mode := cipher.NewCBCEncrypter(block, []byte(iv))
	ciphertext := make([]byte, len(plaintextBytes))
	mode.CryptBlocks(ciphertext, plaintextBytes)

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}
