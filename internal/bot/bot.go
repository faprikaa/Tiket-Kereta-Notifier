package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tiket-kereta-notifier/internal/common"
	"tiket-kereta-notifier/internal/config"
	"tiket-kereta-notifier/internal/telegram"
)

// RegisterCommands registers commands for multiple providers
func RegisterCommands(bot *telegram.Bot, providers []common.Provider, cfg *config.Config) {

	// Command: /check [index] - Check specific train or all trains
	bot.RegisterCommand("/check", func(ctx context.Context, chatID, args string) {
		args = strings.TrimSpace(args)

		// If index specified, check single train
		if args != "" {
			if idx, err := strconv.Atoi(args); err == nil && idx >= 1 && idx <= len(providers) {
				result := checkTrainResult(ctx, providers[idx-1], cfg.Trains[idx-1])
				telegram.SendMessage(result, chatID)
				return
			}
		}

		// Check all trains - consolidate results
		telegram.SendMessage(fmt.Sprintf("🔍 Checking %d trains...", len(providers)), chatID)

		var sb strings.Builder
		availableCount := 0

		for i, provider := range providers {
			trainCfg := cfg.Trains[i]
			trains, err := provider.Search(ctx)

			if err != nil {
				sb.WriteString(fmt.Sprintf("❌ #%d %s [%s] %s: Error\n", i+1, trainCfg.Name, trainCfg.Date, trainCfg.Provider))
				continue
			}

			// Filter for available
			var available []common.Train
			for _, t := range trains {
				if t.Availability == "AVAILABLE" || (t.SeatsLeft != "0" && t.SeatsLeft != "") {
					available = append(available, t)
				}
			}

			if len(available) > 0 {
				availableCount++
				sb.WriteString(fmt.Sprintf("✅ #%d %s [%s] %s: %d tersedia!\n",
					i+1, trainCfg.Name, trainCfg.Date, trainCfg.Provider, len(available)))
				for _, t := range available {
					sb.WriteString(fmt.Sprintf("   💺 %s seats @ Rp%s\n", t.SeatsLeft, t.Price))
				}
			} else {
				sb.WriteString(fmt.Sprintf("⛔ #%d %s [%s] %s: Habis\n",
					i+1, trainCfg.Name, trainCfg.Date, trainCfg.Provider))
			}
		}

		header := fmt.Sprintf("📊 Hasil Check (%d/%d tersedia):\n\n", availableCount, len(providers))
		telegram.SendMessage(header+sb.String(), chatID)
	})

	// Command: /all <index> - Get all trains on route (no name filter)
	bot.RegisterCommand("/all", func(ctx context.Context, chatID, args string) {
		args = strings.TrimSpace(args)

		if args == "" {
			telegram.SendMessage("❌ Usage: /all <index>\nExample: /all 1", chatID)
			return
		}

		idx, err := strconv.Atoi(args)
		if err != nil || idx < 1 || idx > len(providers) {
			telegram.SendMessage(fmt.Sprintf("❌ Invalid index. Use 1-%d", len(providers)), chatID)
			return
		}

		trainCfg := cfg.Trains[idx-1]
		provider := providers[idx-1]

		telegram.SendMessage(fmt.Sprintf("📋 Fetching all trains for #%d [%s] %s...", idx, trainCfg.Date, trainCfg.Provider), chatID)

		trains, err := provider.SearchAll(ctx)
		if err != nil {
			telegram.SendMessage(fmt.Sprintf("❌ Error: %v", err), chatID)
			return
		}

		if len(trains) == 0 {
			telegram.SendMessage("❌ No trains found on this route", chatID)
			return
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🚂 All Trains: %s → %s [%s]\n\n", trainCfg.Origin, trainCfg.Destination, trainCfg.Date))

		for i, t := range trains {
			status := "⛔"
			if t.Availability == "AVAILABLE" || (t.SeatsLeft != "0" && t.SeatsLeft != "") {
				status = "✅"
			}
			sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, status, t.Name))
			sb.WriteString(fmt.Sprintf("   ⏰ %s → %s\n", t.DepartureTime, t.ArrivalTime))
			if t.SeatsLeft != "0" && t.SeatsLeft != "" {
				sb.WriteString(fmt.Sprintf("   💺 %s seats @ Rp%s\n", t.SeatsLeft, t.Price))
			}
			sb.WriteString("\n")

			// Break message if too long
			if sb.Len() > 3500 {
				telegram.SendMessage(sb.String(), chatID)
				sb.Reset()
			}
		}

		if sb.Len() > 0 {
			sb.WriteString(fmt.Sprintf("Total: %d trains", len(trains)))
			telegram.SendMessage(sb.String(), chatID)
		}
	})

	// Command: /list [index] - List all configured trains or show specific train
	bot.RegisterCommand("/list", func(ctx context.Context, chatID, args string) {
		args = strings.TrimSpace(args)

		// If index specified, show single train details
		if args != "" {
			if idx, err := strconv.Atoi(args); err == nil && idx >= 1 && idx <= len(providers) {
				trainCfg := cfg.Trains[idx-1]
				status := providers[idx-1].GetStatus()
				lastCheck := "Never"
				if !status.LastCheckTime.IsZero() {
					lastCheck = formatDuration(time.Since(status.LastCheckTime)) + " ago"
				}

				msg := fmt.Sprintf("🚂 Train #%d: %s\n\n", idx, trainCfg.Name)
				msg += fmt.Sprintf("📍 Route: %s → %s\n", trainCfg.Origin, trainCfg.Destination)
				msg += fmt.Sprintf("📅 Date: %s\n", trainCfg.Date)
				msg += fmt.Sprintf("🔌 Provider: %s\n", trainCfg.Provider)
				msg += fmt.Sprintf("⏱️ Interval: %s\n", trainCfg.IntervalDuration)
				msg += fmt.Sprintf("🌐 Proxy: %s\n", func() string {
					if trainCfg.ProxyURL != "" {
						return "Yes"
					} else {
						return "No"
					}
				}())
				msg += fmt.Sprintf("\n📊 Last check: %s", lastCheck)

				telegram.SendMessage(msg, chatID)
				return
			}
		}

		// List all trains
		var sb strings.Builder
		sb.WriteString("🚂 Configured Trains:\n\n")

		for i, trainCfg := range cfg.Trains {
			status := providers[i].GetStatus()
			lastCheck := "Never"
			if !status.LastCheckTime.IsZero() {
				lastCheck = formatDuration(time.Since(status.LastCheckTime)) + " ago"
			}

			sb.WriteString(fmt.Sprintf("%d. %s [%s] %s\n", i+1, trainCfg.Name, trainCfg.Date, trainCfg.Provider))
			sb.WriteString(fmt.Sprintf("   📍 %s → %s | ⏱️ %s\n\n", trainCfg.Origin, trainCfg.Destination, lastCheck))
		}

		sb.WriteString("Use /list [n] for train details")

		telegram.SendMessage(sb.String(), chatID)
	})

	// Command: /status [index] - Show status of specific train or summary
	bot.RegisterCommand("/status", func(ctx context.Context, chatID, args string) {
		args = strings.TrimSpace(args)

		// If index specified, show single train status
		if args != "" {
			if idx, err := strconv.Atoi(args); err == nil && idx >= 1 && idx <= len(providers) {
				showTrainStatus(chatID, providers[idx-1], cfg.Trains[idx-1], idx)
				return
			}
		}

		// Show summary of all trains
		var sb strings.Builder
		sb.WriteString("🤖 Bot Status Summary\n\n")

		totalChecks := 0
		totalSuccess := 0
		totalFailed := 0

		for i, provider := range providers {
			status := provider.GetStatus()
			trainCfg := cfg.Trains[i]

			totalChecks += status.TotalChecks
			totalSuccess += status.SuccessfulChecks
			totalFailed += status.FailedChecks

			icon := "⛔"
			if status.LastCheckFound {
				icon = "✅"
			}
			if status.LastCheckError != "" {
				icon = "❌"
			}

			sb.WriteString(fmt.Sprintf("%d. %s %s (%s)\n", i+1, icon, trainCfg.Name, trainCfg.Provider))
		}

		sb.WriteString(fmt.Sprintf("\n📊 Total: %d checks | ✅ %d | ❌ %d\n", totalChecks, totalSuccess, totalFailed))
		sb.WriteString("\nUse /status [n] for detailed status")

		telegram.SendMessage(sb.String(), chatID)
	})

	// Command: /history [index] [count] - Show history for specific train
	bot.RegisterCommand("/history", func(ctx context.Context, chatID, args string) {
		parts := strings.Fields(args)

		// Default: first train, 3 entries
		trainIdx := 0
		count := 3

		if len(parts) >= 1 {
			if idx, err := strconv.Atoi(parts[0]); err == nil && idx >= 1 && idx <= len(providers) {
				trainIdx = idx - 1
			}
		}
		if len(parts) >= 2 {
			if n, err := strconv.Atoi(parts[1]); err == nil && n > 0 {
				count = n
			}
		}

		results := providers[trainIdx].GetHistory(count)
		trainCfg := cfg.Trains[trainIdx]

		if len(results) == 0 {
			telegram.SendMessage(fmt.Sprintf("📭 No history for %s yet.", trainCfg.Name), chatID)
			return
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📜 History: %s (last %d)\n\n", trainCfg.Name, len(results)))

		for i, r := range results {
			timestamp := r.Timestamp.Format("02 Jan 15:04")
			if r.Error != "" {
				sb.WriteString(fmt.Sprintf("%d. ❌ [%s] Error\n", i+1, timestamp))
			} else if len(r.AvailableTrains) > 0 {
				sb.WriteString(fmt.Sprintf("%d. ✅ [%s] %d available\n", i+1, timestamp, len(r.AvailableTrains)))
			} else {
				sb.WriteString(fmt.Sprintf("%d. ⛔ [%s] No seats\n", i+1, timestamp))
			}
		}

		telegram.SendMessage(sb.String(), chatID)
	})

	// Command: /help
	bot.RegisterCommand("/help", func(ctx context.Context, chatID, args string) {
		help := fmt.Sprintf(`🚂 Train Notifier (Monitoring %d trains)

/list - List all configured trains
/check [n] - Check train #n (or all)
/status [n] - Status of train #n (or summary)
/history [n] [count] - History of train #n

Examples:
/check 1 - Check first train only
/check - Check all trains
/status 2 - Detailed status of train #2`, len(providers))

		telegram.SendMessage(help, chatID)
	})
}

// checkTrainResult checks availability and returns formatted result string
func checkTrainResult(ctx context.Context, provider common.Provider, trainCfg config.TrainConfig) string {
	trains, err := provider.Search(ctx)
	if err != nil {
		return fmt.Sprintf("❌ %s [%s] %s\n   Error: %v", trainCfg.Name, trainCfg.Date, trainCfg.Provider, err)
	}

	if len(trains) == 0 {
		return fmt.Sprintf("❌ %s [%s] %s\n   No trains found", trainCfg.Name, trainCfg.Date, trainCfg.Provider)
	}

	// Filter for available trains
	var available []common.Train
	for _, t := range trains {
		if t.Availability == "AVAILABLE" || (t.SeatsLeft != "0" && t.SeatsLeft != "") {
			available = append(available, t)
		}
	}

	if len(available) > 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("✅ %s [%s] %s: %d tersedia!\n", trainCfg.Name, trainCfg.Date, trainCfg.Provider, len(available)))
		for _, t := range available {
			sb.WriteString(fmt.Sprintf("   🚂 %s\n   ⏰ %s → %s\n   💺 %s seats @ Rp%s\n",
				t.Name, t.DepartureTime, t.ArrivalTime, t.SeatsLeft, t.Price))
		}
		return sb.String()
	}

	return fmt.Sprintf("⛔ %s [%s] %s: Habis (%d kereta full)", trainCfg.Name, trainCfg.Date, trainCfg.Provider, len(trains))
}

// showTrainStatus shows detailed status for a single train
func showTrainStatus(chatID string, provider common.Provider, trainCfg config.TrainConfig, index int) {
	status := provider.GetStatus()

	uptime := formatDuration(time.Since(status.StartTime))

	lastCheck := "Never"
	lastResult := "N/A"
	if !status.LastCheckTime.IsZero() {
		lastCheck = formatDuration(time.Since(status.LastCheckTime)) + " ago"
		if status.LastCheckError != "" {
			lastResult = "❌ Error"
		} else if status.LastCheckFound {
			lastResult = "✅ Found seats!"
		} else {
			lastResult = "⛔ No seats"
		}
	}

	msg := fmt.Sprintf(`🚂 Train #%d: %s

📍 Route: %s → %s
📅 Date: %s
🔌 Provider: %s
⏱️ Interval: %s

📊 Statistics:
• Uptime: %s
• Checks: %d (✅ %d | ❌ %d)
• Last: %s - %s`,
		index, trainCfg.Name,
		trainCfg.Origin, trainCfg.Destination,
		trainCfg.Date,
		trainCfg.Provider,
		trainCfg.IntervalDuration.String(),
		uptime,
		status.TotalChecks, status.SuccessfulChecks, status.FailedChecks,
		lastCheck, lastResult,
	)

	telegram.SendMessage(msg, chatID)
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
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
