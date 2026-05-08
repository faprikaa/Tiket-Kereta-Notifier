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
				result := checkTrainResult(ctx, providers[idx-1], cfg.FlatTrains[idx-1])
				telegram.SendMessage(result, chatID)
				return
			}
		}

		// Check all trains - consolidate results
		telegram.SendMessage(fmt.Sprintf("🔍 Checking %d trains...", len(providers)), chatID)

		var sb strings.Builder
		availableCount := 0

		for i, provider := range providers {
			flat := cfg.FlatTrains[i]
			trains, err := provider.Search(ctx)

			if err != nil {
				sb.WriteString(fmt.Sprintf("❌ #%d %s [%s] via %s: Error\n", i+1, flat.Name, flat.Date, flat.ProviderName))
				continue
			}

			// Filter for available
			var available []common.Train
			for _, t := range trains {
				if t.Availability == "AVAILABLE" || (t.SeatsLeft != "0" && t.SeatsLeft != "") {
					// Apply max price filter
					if flat.MaxPrice > 0 {
						price := common.ParsePrice(t.Price)
						if price > 0 && price > flat.MaxPrice {
							continue
						}
					}
					available = append(available, t)
				}
			}

			if len(available) > 0 {
				availableCount++
				sb.WriteString(fmt.Sprintf("✅ #%d %s [%s] via %s: %d tersedia!\n",
					i+1, flat.Name, flat.Date, flat.ProviderName, len(available)))
				for _, t := range available {
					sb.WriteString(fmt.Sprintf("   💺 %s seats @ Rp%s\n", t.SeatsLeft, t.Price))
				}
			} else {
				sb.WriteString(fmt.Sprintf("⛔ #%d %s [%s] via %s: Habis\n",
					i+1, flat.Name, flat.Date, flat.ProviderName))
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

		flat := cfg.FlatTrains[idx-1]
		provider := providers[idx-1]

		telegram.SendMessage(fmt.Sprintf("📋 Fetching all trains for #%d [%s] %s...", idx, flat.Date, flat.ProviderName), chatID)

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
		sb.WriteString(fmt.Sprintf("🚂 All Trains: %s → %s [%s]\n\n", flat.Origin, flat.Destination, flat.Date))

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
				flat := cfg.FlatTrains[idx-1]
				status := providers[idx-1].GetStatus()
				lastCheck := "Never"
				if !status.LastCheckTime.IsZero() {
					lastCheck = formatDuration(time.Since(status.LastCheckTime)) + " ago"
				}

				pausedStr := ""
				if providers[idx-1].IsPaused() {
					pausedStr = " ⏸️ PAUSED"
				}

				msg := fmt.Sprintf("🚂 Train #%d: %s%s\n\n", idx, flat.Name, pausedStr)
				msg += fmt.Sprintf("📍 Route: %s → %s\n", flat.Origin, flat.Destination)
				msg += fmt.Sprintf("📅 Date: %s\n", flat.Date)
				msg += fmt.Sprintf("🔌 Provider: %s\n", flat.ProviderName)
				msg += fmt.Sprintf("⏱️ Interval: %s\n", flat.IntervalDuration)
				msg += fmt.Sprintf("🌐 Proxy: %s\n", func() string {
					if flat.ProxyURL != "" {
						return "Yes"
					} else {
						return "No"
					}
				}())
				if flat.MaxPrice > 0 {
					msg += fmt.Sprintf("💰 Max Price: Rp %s\n", formatRupiah(flat.MaxPrice))
				}
				if flat.Notes != "" {
					msg += fmt.Sprintf("📝 Notes: %s\n", flat.Notes)
				}
				msg += fmt.Sprintf("\n📊 Last check: %s", lastCheck)

				telegram.SendMessage(msg, chatID)
				return
			}
		}

		// List all trains, grouped by train identity
		var sb strings.Builder
		sb.WriteString("🚂 *Configured Trains*\n")

		providerEmoji := map[string]string{
			"traveloka":  "✈️",
			"tiketkai":   "🚉",
			"tiketcom":   "🎫",
			"bookingkai": "🏛️",
		}

		flatIdx := 0
		for _, trainCfg := range cfg.Trains {
			// Train header
			sb.WriteString(fmt.Sprintf("\n🚂 *%s* | %s → %s\n", trainCfg.Name, trainCfg.Origin, trainCfg.Destination))
			sb.WriteString(fmt.Sprintf("📅 %s", trainCfg.Date))
			if trainCfg.Notes != "" {
				sb.WriteString(fmt.Sprintf(" | 📝 %s", trainCfg.Notes))
			}
			sb.WriteString("\n")

			for i := 0; i < len(trainCfg.Providers); i++ {
				if flatIdx >= len(cfg.FlatTrains) {
					break
				}
				provider := providers[flatIdx]
				flat := cfg.FlatTrains[flatIdx]
				status := provider.GetStatus()

				// Status icon from last check
				statusIcon := "⬜"
				if !status.LastCheckTime.IsZero() {
					if status.LastCheckError != "" {
						statusIcon = "❌"
					} else if status.LastCheckFound {
						statusIcon = "✅"
					} else {
						statusIcon = "⛔"
					}
				}

				// Paused overrides status
				if provider.IsPaused() {
					statusIcon = "⏸️"
				}

				lastCheck := "never"
				if !status.LastCheckTime.IsZero() {
					lastCheck = formatDuration(time.Since(status.LastCheckTime)) + " ago"
				}

				emoji := providerEmoji[flat.ProviderName]
				if emoji == "" {
					emoji = "🔌"
				}

				providerLabel := strings.ToUpper(flat.ProviderName)
				if flat.ProxyURL != "" {
					providerLabel += " (proxy)"
				}

				sb.WriteString(fmt.Sprintf(" %s %s %s | #%d | %s\n",
					statusIcon, emoji, providerLabel, flatIdx+1, lastCheck))
				flatIdx++
			}
		}

		sb.WriteString("\n/list <n> · /check <n> · /toggle <n>")

		telegram.SendMessage(sb.String(), chatID)
	})

	// Command: /status [index] - Show status of specific train or summary
	bot.RegisterCommand("/status", func(ctx context.Context, chatID, args string) {
		args = strings.TrimSpace(args)

		// If index specified, show single train status
		if args != "" {
			if idx, err := strconv.Atoi(args); err == nil && idx >= 1 && idx <= len(providers) {
				showTrainStatus(chatID, providers[idx-1], cfg.FlatTrains[idx-1], idx)
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
			flat := cfg.FlatTrains[i]

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

			sb.WriteString(fmt.Sprintf("%d. %s %s [%s] via %s\n", i+1, icon, flat.Name, flat.Date, flat.ProviderName))
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
		flat := cfg.FlatTrains[trainIdx]

		if len(results) == 0 {
			telegram.SendMessage(fmt.Sprintf("📭 No history for %s yet.", flat.Name), chatID)
			return
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📜 History: %s (last %d)\n\n", flat.Name, len(results)))

		for i, r := range results {
			timestamp := r.Timestamp.Format("02 Jan 15:04")
			method := ""
			if r.Method != "" {
				method = " [" + r.Method + "]"
			}
			if r.Error != "" {
				sb.WriteString(fmt.Sprintf("%d. ❌ [%s] Error: %s\n", i+1, timestamp, r.Error))
			} else if len(r.AvailableTrains) > 0 {
				sb.WriteString(fmt.Sprintf("%d. ✅ [%s] %d available%s\n", i+1, timestamp, len(r.AvailableTrains), method))
			} else {
				sb.WriteString(fmt.Sprintf("%d. ⛔ [%s] No seats%s\n", i+1, timestamp, method))
			}
		}

		telegram.SendMessage(sb.String(), chatID)
	})

	// Command: /toggle [index] - Toggle pause/resume for a specific train monitor
	bot.RegisterCommand("/toggle", func(ctx context.Context, chatID, args string) {
		args = strings.TrimSpace(args)

		if args == "" {
			telegram.SendMessage("❌ Usage: /toggle <index>\nExample: /toggle 1", chatID)
			return
		}

		idx, err := strconv.Atoi(args)
		if err != nil || idx < 1 || idx > len(providers) {
			telegram.SendMessage(fmt.Sprintf("❌ Invalid index. Use 1-%d", len(providers)), chatID)
			return
		}

		provider := providers[idx-1]
		flat := cfg.FlatTrains[idx-1]
		newState := !provider.IsPaused()
		provider.SetPaused(newState)

		if newState {
			telegram.SendMessage(fmt.Sprintf("⏸️ Train #%d (%s) paused", idx, flat.Name), chatID)
		} else {
			telegram.SendMessage(fmt.Sprintf("▶️ Train #%d (%s) resumed", idx, flat.Name), chatID)
		}
	})

	// Command: /help
	bot.RegisterCommand("/help", func(ctx context.Context, chatID, args string) {
		help := fmt.Sprintf(`🚂 Train Notifier (Monitoring %d trains)

/list - List all configured trains
/list [n] - Show train #n details
/check [n] - Check train #n (or all)
/all [n] - Show all trains on route #n
/status [n] - Status of train #n (or summary)
/history [n] [count] - History of train #n
/toggle [n] - Pause/resume train #n

Examples:
/check 1 - Check first train only
/check - Check all trains
/all 3 - All trains on route #3
/toggle 5 - Pause/resume train #5`, len(providers))

		telegram.SendMessage(help, chatID)
	})
}

// checkTrainResult checks availability and returns formatted result string
func checkTrainResult(ctx context.Context, provider common.Provider, flat config.FlatTrainConfig) string {
	trains, err := provider.Search(ctx)
	if err != nil {
		return fmt.Sprintf("❌ %s [%s] via %s\n   Error: %v", flat.Name, flat.Date, flat.ProviderName, err)
	}

	if len(trains) == 0 {
		return fmt.Sprintf("❌ %s [%s] via %s\n   No trains found", flat.Name, flat.Date, flat.ProviderName)
	}

	// Filter for available trains
	var available []common.Train
	for _, t := range trains {
		if t.Availability == "AVAILABLE" || (t.SeatsLeft != "0" && t.SeatsLeft != "") {
			// Apply max price filter
			if flat.MaxPrice > 0 {
				price := common.ParsePrice(t.Price)
				if price > 0 && price > flat.MaxPrice {
					continue
				}
			}
			available = append(available, t)
		}
	}

	if len(available) > 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("✅ %s [%s] via %s: %d tersedia!\n", flat.Name, flat.Date, flat.ProviderName, len(available)))
		for _, t := range available {
			sb.WriteString(fmt.Sprintf("   🚂 %s\n   ⏰ %s → %s\n   💺 %s seats @ Rp%s\n",
				t.Name, t.DepartureTime, t.ArrivalTime, t.SeatsLeft, t.Price))
		}
		return sb.String()
	}

	return fmt.Sprintf("⛔ %s [%s] via %s: Habis (%d kereta full)", flat.Name, flat.Date, flat.ProviderName, len(trains))
}

// showTrainStatus shows detailed status for a single train
func showTrainStatus(chatID string, provider common.Provider, flat config.FlatTrainConfig, index int) {
	status := provider.GetStatus()

	uptime := formatDuration(time.Since(status.StartTime))

	lastCheck := "Never"
	lastResult := "N/A"
	if !status.LastCheckTime.IsZero() {
		lastCheck = formatDuration(time.Since(status.LastCheckTime)) + " ago"
		if status.LastCheckError != "" {
			lastResult = "❌ Error: " + status.LastCheckError
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
		index, flat.Name,
		flat.Origin, flat.Destination,
		flat.Date,
		flat.ProviderName,
		flat.IntervalDuration.String(),
		uptime,
		status.TotalChecks, status.SuccessfulChecks, status.FailedChecks,
		lastCheck, lastResult,
	)

	telegram.SendMessage(msg, chatID)
}

// formatRupiah formats an integer as an Indonesian Rupiah string with dot separators
// e.g. 350000 -> "350.000"
func formatRupiah(amount int) string {
	s := strconv.Itoa(amount)
	n := len(s)
	if n <= 3 {
		return s
	}
	var result strings.Builder
	for i, c := range s {
		if i > 0 && (n-i)%3 == 0 {
			result.WriteByte('.')
		}
		result.WriteRune(c)
	}
	return result.String()
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
