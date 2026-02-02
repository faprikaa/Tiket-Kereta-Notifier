// Package telegram - webhook.go provides Telegram webhook functionality
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WebhookServer represents a webhook HTTP server
type WebhookServer struct {
	server *http.Server
	bot    *Bot
	port   int
}

// NewWebhookServer creates a new webhook server
func NewWebhookServer(port int, bot *Bot) *WebhookServer {
	return &WebhookServer{
		port: port,
		bot:  bot,
	}
}

// Start starts the webhook HTTP server
func (w *WebhookServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", w.handleWebhook)
	mux.HandleFunc("/health", w.handleHealth)
	mux.HandleFunc("/health/", w.handleHealth)

	w.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", w.port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	Logger.Info("Starting webhook server", "port", w.port)

	go func() {
		if err := w.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			Logger.Error("Webhook server error", "error", err)
		}
	}()

	return nil
}

// Stop stops the webhook server gracefully
func (w *WebhookServer) Stop(ctx context.Context) error {
	if w.server == nil {
		return nil
	}
	Logger.Info("Stopping webhook server...")
	return w.server.Shutdown(ctx)
}

// handleWebhook processes incoming webhook updates from Telegram
func (w *WebhookServer) handleWebhook(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		Logger.Error("Failed to read webhook body", "error", err)
		http.Error(rw, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	Logger.Debug("Received webhook update", "body", string(body))

	var update map[string]interface{}
	if err := json.Unmarshal(body, &update); err != nil {
		Logger.Error("Failed to parse webhook update", "error", err)
		http.Error(rw, "Bad request", http.StatusBadRequest)
		return
	}

	// Process the update
	w.processUpdate(r.Context(), update)

	rw.WriteHeader(http.StatusOK)
}

// handleHealth returns health status
func (w *WebhookServer) handleHealth(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(map[string]string{"status": "ok"})
}

// processUpdate routes the update to appropriate command handler
func (w *WebhookServer) processUpdate(ctx context.Context, update map[string]interface{}) {
	message, ok := update["message"].(map[string]interface{})
	if !ok {
		return
	}

	chat, ok := message["chat"].(map[string]interface{})
	if !ok {
		return
	}

	chatID := fmt.Sprintf("%.0f", chat["id"].(float64))
	text, ok := message["text"].(string)
	if !ok {
		return
	}

	Logger.Info("Processing webhook message", "chat_id", chatID, "text", text)

	// Parse command
	cmd := text
	args := ""
	if idx := len(text); idx > 0 {
		for i, c := range text {
			if c == ' ' {
				cmd = text[:i]
				if i+1 < len(text) {
					args = text[i+1:]
				}
				break
			}
			if c == '@' {
				cmd = text[:i]
				break
			}
		}
	}

	// Execute handler if exists
	if handler, exists := w.bot.Commands[cmd]; exists {
		handler(ctx, chatID, args)
	}
}

// SetWebhook sets the webhook URL with Telegram API
func SetWebhook(webhookURL string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", Token)

	payload := map[string]interface{}{
		"url":             webhookURL,
		"allowed_updates": []string{"message"},
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to set webhook: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !result.Ok {
		return fmt.Errorf("telegram API error: %s", result.Description)
	}

	Logger.Info("Webhook set successfully", "url", webhookURL)
	return nil
}

// DeleteWebhook removes the webhook from Telegram
func DeleteWebhook() error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/deleteWebhook", Token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to delete webhook: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !result.Ok {
		return fmt.Errorf("telegram API error: %s", result.Description)
	}

	Logger.Info("Webhook deleted successfully")
	return nil
}

// GetWebhookInfo returns current webhook info
func GetWebhookInfo() (map[string]interface{}, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getWebhookInfo", Token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get webhook info: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Ok     bool                   `json:"ok"`
		Result map[string]interface{} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.Result, nil
}
