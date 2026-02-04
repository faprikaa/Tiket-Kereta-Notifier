package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

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
		status := provider.GetStatus()

		// Calculate uptime
		uptime := time.Since(status.StartTime)
		uptimeStr := formatDuration(uptime)

		// Format last check
		lastCheckStr := "Never"
		lastCheckResultStr := "N/A"
		if !status.LastCheckTime.IsZero() {
			lastCheckStr = fmt.Sprintf("%s ago", formatDuration(time.Since(status.LastCheckTime)))
			if status.LastCheckError != "" {
				lastCheckResultStr = fmt.Sprintf("❌ Error: %s", status.LastCheckError)
			} else if status.LastCheckFound {
				lastCheckResultStr = "✅ Found available seats!"
			} else {
				lastCheckResultStr = "⛔ No seats available"
			}
		}

		// Format target train
		targetStr := "All trains"
		if status.TrainName != "" {
			targetStr = status.TrainName
		}

		msg := fmt.Sprintf(`🤖 Bot Status

📊 Provider: %s
⏱️ Uptime: %s

📈 Statistics:
• Total Checks: %d
• Successful: %d
• Failed: %d

🔍 Last Check:
• When: %s
• Result: %s

🎯 Target:
• Route: %s → %s
• Date: %s
• Train: %s
• Interval: %s`,
			provider.Name(),
			uptimeStr,
			status.TotalChecks,
			status.SuccessfulChecks,
			status.FailedChecks,
			lastCheckStr,
			lastCheckResultStr,
			status.Origin,
			status.Destination,
			status.Date,
			targetStr,
			status.Interval.String(),
		)

		telegram.SendMessage(msg, chatID)
	})

	// Command: /history [n]
	bot.RegisterCommand("/history", func(ctx context.Context, chatID, args string) {
		// Parse count argument (default 3)
		count := 3
		args = strings.TrimSpace(args)
		if args != "" {
			if n, err := strconv.Atoi(args); err == nil && n > 0 {
				count = n
			}
		}

		results := provider.GetHistory(count)
		if len(results) == 0 {
			telegram.SendMessage("📭 No history available yet.", chatID)
			return
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "📜 Last %d Check Results:\n\n", len(results))

		for i, r := range results {
			timestamp := r.Timestamp.Format("02 Jan 15:04:05")
			if r.Error != "" {
				sb.WriteString(fmt.Sprintf("%d. ❌ [%s] Error: %s\n\n", i+1, timestamp, r.Error))
			} else if len(r.AvailableTrains) > 0 {
				sb.WriteString(fmt.Sprintf("%d. ✅ [%s] %d available\n", i+1, timestamp, len(r.AvailableTrains)))
				for _, t := range r.AvailableTrains {
					sb.WriteString(fmt.Sprintf("   🚂 %s: %s seats\n", t.Name, t.SeatsLeft))
				}
				sb.WriteString("\n")
			} else {
				sb.WriteString(fmt.Sprintf("%d. ⛔ [%s] No seats available (checked %d trains)\n\n", i+1, timestamp, r.TrainsFound))
			}

			// Break message if too long
			if sb.Len() > 3000 {
				telegram.SendMessage(sb.String(), chatID)
				sb.Reset()
			}
		}

		if sb.Len() > 0 {
			telegram.SendMessage(sb.String(), chatID)
		}
	})

	// Command: /help
	bot.RegisterCommand("/help", func(ctx context.Context, chatID, args string) {
		help := fmt.Sprintf(`🚂 Train Notifier (%s)
		
/check - Check availability manual
/list - List all monitored trains
/history [n] - Show last n checks (default 3)
/status - Show detailed bot status
/help - Show this message`, provider.Name())
		telegram.SendMessage(help, chatID)
	})
}

// formatDuration formats a duration into a human readable string
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
