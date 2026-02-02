package bot

import (
	"context"
	"fmt"
	"strings"

	"tiket-kereta-notifier/internal/common"
	"tiket-kereta-notifier/internal/telegram"
)

// RegisterCommands registers generic commands for any provider
func RegisterCommands(bot *telegram.Bot, provider common.Provider) {

	// Command: /check
	bot.RegisterCommand("/check", func(ctx context.Context, chatID, args string) {
		telegram.SendMessage(fmt.Sprintf("🔍 %s: Checking availability...", provider.Name()), chatID)

		trains, err := provider.Search(ctx)
		if err != nil {
			telegram.SendMessage(fmt.Sprintf("❌ Error: %v", err), chatID)
			return
		}

		if len(trains) == 0 {
			telegram.SendMessage("❌ No trains found.", chatID)
			return
		}

		// Filter for available trains or show summary
		var available []common.Train
		for _, t := range trains {
			if t.Availability == "AVAILABLE" || t.Availability == "True" || t.SeatsLeft != "0" {
				available = append(available, t)
			}
		}

		if len(available) > 0 {
			msg := fmt.Sprintf("✅ Found %d available trains!\n\n", len(available))
			for _, t := range available {
				msg += fmt.Sprintf("🚂 %s (%s)\n📅 %s -> %s\n💰 %s\n\n",
					t.Name, t.SeatsLeft, t.DepartureTime, t.ArrivalTime, t.Price)
			}
			telegram.SendMessage(msg, chatID)
		} else {
			telegram.SendMessage("❌ All trains are fully booked.", chatID)
		}
	})

	// Command: /list
	bot.RegisterCommand("/list", func(ctx context.Context, chatID, args string) {
		telegram.SendMessage(fmt.Sprintf("📋 %s: Fetching train list...", provider.Name()), chatID)

		trains, err := provider.SearchAll(ctx)
		if err != nil {
			telegram.SendMessage(fmt.Sprintf("❌ Error: %v", err), chatID)
			return
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "🚂 Train List (%d):\n\n", len(trains))

		for i, t := range trains {
			status := "❌ Full"
			if t.Availability == "AVAILABLE" || t.SeatsLeft != "0" {
				status = fmt.Sprintf("✅ %s seats", t.SeatsLeft)
			}

			line := fmt.Sprintf("%d. %s\n   ⏰ %s-%s | 💰 %s | %s\n\n",
				i+1, t.Name, t.DepartureTime, t.ArrivalTime, t.Price, status)

			if sb.Len()+len(line) > 3000 {
				telegram.SendMessage(sb.String(), chatID)
				sb.Reset()
			}
			sb.WriteString(line)
		}

		if sb.Len() > 0 {
			telegram.SendMessage(sb.String(), chatID)
		}
	})

	// Command: /status
	bot.RegisterCommand("/status", func(ctx context.Context, chatID, args string) {
		telegram.SendMessage(fmt.Sprintf("🤖 Provider: %s\n✅ Bot is running properly.", provider.Name()), chatID)
	})

	// Command: /help
	bot.RegisterCommand("/help", func(ctx context.Context, chatID, args string) {
		help := fmt.Sprintf(`🚂 Train Notifier (%s)
		
/check - Check availability manual
/list - List all monitored trains
/status - Check bot status
/help - Show this message`, provider.Name())
		telegram.SendMessage(help, chatID)
	})
}
