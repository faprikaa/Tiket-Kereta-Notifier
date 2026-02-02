// Package telegram provides shared Telegram bot functionality
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

var (
	Token        string
	ChatIDs      []string
	Limiter      *rate.Limiter
	Logger       *slog.Logger
	LastUpdateID int64
)

// Init initializes the telegram bot with token and chat IDs
func Init(token string, chatIDs []string, logger *slog.Logger) {
	Token = token
	ChatIDs = chatIDs
	Logger = logger
	Limiter = rate.NewLimiter(rate.Limit(2), 4) // 2 requests per second
}

// SendMessage sends a message to all configured chat IDs
func SendMessage(text string, chatIDs ...string) bool {
	targetChatIDs := ChatIDs
	if len(chatIDs) > 0 && chatIDs[0] != "" {
		targetChatIDs = chatIDs
	}
	if len(targetChatIDs) == 0 {
		return false
	}

	currentTime := time.Now().Format("2006-01-02 15:04:05 WIB")
	fullMessage := fmt.Sprintf("[%s] %s", currentTime, text)

	const maxLength = 4096
	if len(fullMessage) > maxLength {
		fullMessage = fullMessage[:maxLength-25] + "\n\n[Message truncated]"
	}

	success := false
	for _, chatID := range targetChatIDs {
		if sendToSingleChat(fullMessage, chatID) {
			success = true
		}
	}
	return success
}

func sendToSingleChat(message, chatID string) bool {
	Limiter.Wait(context.Background())

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", Token)
	body, _ := json.Marshal(map[string]interface{}{
		"chat_id": chatID,
		"text":    message,
	})

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		Logger.Error("Failed to send telegram message", "chat_id", chatID, "error", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		Logger.Debug("Telegram message sent", "chat_id", chatID)
		return true
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
		if params, ok := result["parameters"].(map[string]interface{}); ok {
			if newID, exists := params["migrate_to_chat_id"]; exists {
				newChatID := fmt.Sprintf("%.0f", newID.(float64))
				Logger.Info("Chat migrated", "old_id", chatID, "new_id", newChatID)
				for i, id := range ChatIDs {
					if id == chatID {
						ChatIDs[i] = newChatID
						break
					}
				}
				return sendToSingleChat(message, newChatID)
			}
		}
	}
	return false
}

// GetUpdates fetches new messages/commands from Telegram
func GetUpdates(ctx context.Context) ([]map[string]interface{}, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=5", Token, LastUpdateID+1)
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Ok     bool                     `json:"ok"`
		Result []map[string]interface{} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Result, nil
}

// CommandHandler is a function that handles a specific command
type CommandHandler func(ctx context.Context, chatID string, args string)

// Bot represents a telegram bot with command handlers
type Bot struct {
	Commands map[string]CommandHandler
	Logger   *slog.Logger
}

// NewBot creates a new bot instance
func NewBot(logger *slog.Logger) *Bot {
	return &Bot{
		Commands: make(map[string]CommandHandler),
		Logger:   logger,
	}
}

// RegisterCommand registers a command handler
func (b *Bot) RegisterCommand(cmd string, handler CommandHandler) {
	b.Commands[cmd] = handler
}

// HandleUpdates processes incoming updates and routes to handlers
func (b *Bot) HandleUpdates(ctx context.Context) {
	updates, err := GetUpdates(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			b.Logger.Error("Error getting updates", "error", err)
		}
		return
	}

	for _, update := range updates {
		updateID := int64(update["update_id"].(float64))
		if updateID > LastUpdateID {
			LastUpdateID = updateID
		}

		message, ok := update["message"].(map[string]interface{})
		if !ok {
			continue
		}

		chat := message["chat"].(map[string]interface{})
		chatID := fmt.Sprintf("%.0f", chat["id"].(float64))

		text, ok := message["text"].(string)
		if !ok {
			continue
		}

		b.Logger.Info("Received command", "chat_id", chatID, "text", text)

		// Strip @botname suffix for group chat commands
		cmdText := text
		if idx := strings.Index(text, "@"); idx != -1 {
			cmdText = text[:idx]
		}

		// Parse command and args
		parts := strings.SplitN(cmdText, " ", 2)
		cmd := parts[0]
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}

		// Find and execute handler
		if handler, exists := b.Commands[cmd]; exists {
			handler(ctx, chatID, args)
		}
	}
}
