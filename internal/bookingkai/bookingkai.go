// Package bookingkai provides train search functionality by scraping booking.kai.id
package bookingkai

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/stealth"

	"tiket-kereta-notifier/internal/common"
	"tiket-kereta-notifier/internal/history"
)

// Indonesian month names for date formatting
var monthNames = []string{
	"", "Januari", "Februari", "Maret", "April", "Mei", "Juni",
	"Juli", "Agustus", "September", "Oktober", "November", "Desember",
}

// Provider implements common.Provider for booking.kai.id
type Provider struct {
	Logger        *slog.Logger
	Origin        string
	Destination   string
	Date          string        // YYYY-MM-DD format
	TrainName     string        // Optional: specific train to monitor
	CheckInterval time.Duration // Polling interval
	ProxyURL      string        // Optional SOCKS5 proxy
	Index         int           // Global index (1-based)
	Notes         string        // Optional user notes
	history       *history.Store
	status        *common.StatusTracker
	browser       *rod.Browser
}

// NewProvider creates a new BookingKAI provider
func NewProvider(logger *slog.Logger, origin, dest, date, trainName string, interval time.Duration, proxyURL string, index int, notes string) *Provider {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Provider{
		Logger:        logger,
		Origin:        origin,
		Destination:   dest,
		Date:          date,
		TrainName:     trainName,
		CheckInterval: interval,
		ProxyURL:      proxyURL,
		Index:         index,
		Notes:         notes,
		history:       history.NewStore(100),
		status:        common.NewStatusTracker(),
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return fmt.Sprintf("bookingkai:%s:%s→%s", p.TrainName, p.Origin, p.Destination)
}

// formatDateIndo converts "2026-04-02" to "02-April-2026"
func formatDateIndo(date string) (string, error) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", fmt.Errorf("invalid date format %q: %w", date, err)
	}
	month := monthNames[t.Month()]
	return fmt.Sprintf("%02d-%s-%d", t.Day(), month, t.Year()), nil
}

// ensureBrowser launches a shared browser instance (reused across searches)
func (p *Provider) ensureBrowser() error {
	if p.browser != nil {
		return nil
	}

	l := launcher.New().
		Headless(true).
		Set("no-sandbox").
		Set("disable-dev-shm-usage").
		Set("disable-gpu")

	if p.ProxyURL != "" {
		l = l.Proxy(p.ProxyURL)
	}

	u, err := l.Launch()
	if err != nil {
		return fmt.Errorf("failed to launch browser: %w", err)
	}

	browser := rod.New().ControlURL(u)
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("failed to connect to browser: %w", err)
	}

	p.browser = browser
	p.Logger.Info("Browser launched", "proxy", p.ProxyURL)
	return nil
}

// Search performs a search and returns trains matching TrainName
func (p *Provider) Search(ctx context.Context) ([]common.Train, error) {
	allTrains, err := p.fetchTrains(ctx)
	if err != nil {
		return nil, err
	}

	if p.TrainName == "" {
		return allTrains, nil
	}

	// Filter by train name
	target := strings.ToLower(p.TrainName)
	var filtered []common.Train
	for _, t := range allTrains {
		if strings.Contains(strings.ToLower(t.Name), target) {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

// SearchAll returns all trains on the route without filtering
func (p *Provider) SearchAll(ctx context.Context) ([]common.Train, error) {
	return p.fetchTrains(ctx)
}

// fetchTrains navigates to booking.kai.id and parses the HTML result
func (p *Provider) fetchTrains(ctx context.Context) ([]common.Train, error) {
	if err := p.ensureBrowser(); err != nil {
		return nil, err
	}

	// Format date for URL
	dateIndo, err := formatDateIndo(p.Date)
	if err != nil {
		return nil, err
	}

	// Build search URL
	searchURL := fmt.Sprintf(
		"https://booking.kai.id/?origination=%s&destination=%s&tanggal=%s&adult=1&infant=0&submit=Cari+%%26+Pesan+Tiket",
		p.Origin, p.Destination, strings.ReplaceAll(dateIndo, " ", "+"),
	)

	p.Logger.Debug("Fetching booking.kai.id", "url", searchURL)

	// Open new page with stealth
	page, err := stealth.Page(p.browser)
	if err != nil {
		// Browser may have crashed, reset and retry
		p.browser = nil
		return nil, fmt.Errorf("failed to create stealth page: %w", err)
	}
	defer page.Close()

	// Set timeout for the entire operation
	page = page.Timeout(90 * time.Second)

	// Navigate
	if err := page.Navigate(searchURL); err != nil {
		return nil, fmt.Errorf("navigate failed: %w", err)
	}

	// Wait for train results to appear (instead of fixed 15s sleep)
	// The page has .data-wrapper elements when train data loads
	p.Logger.Debug("Waiting for search results...")
	_, err = page.Element(".data-wrapper")
	if err != nil {
		// Fallback: maybe Cloudflare is blocking, check page content
		htmlContent, _ := page.HTML()
		if strings.Contains(htmlContent, "Just a moment") || strings.Contains(htmlContent, "cf-browser-verification") {
			return nil, fmt.Errorf("blocked by Cloudflare challenge")
		}
		return nil, fmt.Errorf("timeout waiting for results: %w", err)
	}

	// Small extra wait for all elements to finish rendering
	time.Sleep(1 * time.Second)

	// Get the rendered HTML
	htmlContent, err := page.HTML()
	if err != nil {
		return nil, fmt.Errorf("failed to get HTML: %w", err)
	}

	// Parse trains from HTML
	trains, err := parseHTML(htmlContent)
	if err != nil {
		return nil, fmt.Errorf("HTML parsing failed: %w", err)
	}

	p.Logger.Info("BookingKAI search complete",
		"route", fmt.Sprintf("%s→%s", p.Origin, p.Destination),
		"date", p.Date,
		"total", len(trains))

	return trains, nil
}

// parseHTML extracts train information from the booking.kai.id search results page
func parseHTML(rawHTML string) ([]common.Train, error) {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return nil, err
	}

	var trains []common.Train

	// Find all div.data-block.list-kereta elements
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if isDataBlock(n) {
			train := extractTrainFromBlock(n)
			if train.Name != "" {
				trains = append(trains, train)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	return trains, nil
}

// isDataBlock checks if node is a div with classes "data-block list-kereta"
func isDataBlock(n *html.Node) bool {
	if n.Type != html.ElementNode || n.Data != "div" {
		return false
	}
	for _, attr := range n.Attr {
		if attr.Key == "class" && strings.Contains(attr.Val, "data-block") && strings.Contains(attr.Val, "list-kereta") {
			return true
		}
	}
	return false
}

// extractTrainFromBlock extracts train data from a data-block div
func extractTrainFromBlock(block *html.Node) common.Train {
	// Collect all hidden input values
	inputs := make(map[string]string)
	var collectInputs func(*html.Node)
	collectInputs = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			name, value := "", ""
			isHidden := false
			for _, attr := range n.Attr {
				switch attr.Key {
				case "type":
					if attr.Val == "hidden" {
						isHidden = true
					}
				case "name":
					name = attr.Val
				case "value":
					value = attr.Val
				}
			}
			if isHidden && name != "" {
				inputs[name] = value
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collectInputs(c)
		}
	}
	collectInputs(block)

	// Determine availability: look for <a class="habis"> or <a class="card-schedule">
	availability := "AVAILABLE"
	seatsLeft := "1"
	var checkAvail func(*html.Node)
	checkAvail = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "class" {
					if strings.Contains(attr.Val, "habis") {
						availability = "FULL"
						seatsLeft = "0"
					}
				}
			}
		}
		// Also check for "Tersedia" / "Habis" text in sisa-kursi spans
		if n.Type == html.ElementNode && n.Data == "small" {
			for _, attr := range n.Attr {
				if attr.Key == "class" && strings.Contains(attr.Val, "sisa-kursi") {
					text := getTextContent(n)
					text = strings.TrimSpace(text)
					if text == "Habis" {
						availability = "FULL"
						seatsLeft = "0"
					} else if text == "Tersedia" {
						availability = "AVAILABLE"
						seatsLeft = "1" // KAI doesn't show exact count
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			checkAvail(c)
		}
	}
	checkAvail(block)

	// Build class string from kelas_gerbong + subkelas
	classStr := inputs["kelas_gerbong"]
	if sub := inputs["subkelas"]; sub != "" {
		classStr += " (" + sub + ")"
	}

	// Format price
	price := inputs["harga"]
	if price != "" {
		price = "Rp" + formatNumber(price)
	}

	return common.Train{
		Name:          inputs["kereta"],
		Class:         classStr,
		Price:         price,
		DepartureTime: inputs["timestart"],
		ArrivalTime:   inputs["timeend"],
		Availability:  availability,
		SeatsLeft:     seatsLeft,
	}
}

// getTextContent returns the concatenated text content of a node
func getTextContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(getTextContent(c))
	}
	return sb.String()
}

// formatNumber adds dots to a number string: "385000" -> "385.000"
func formatNumber(s string) string {
	// Remove any non-digit chars first
	var digits []byte
	for _, c := range s {
		if c >= '0' && c <= '9' {
			digits = append(digits, byte(c))
		}
	}
	n := len(digits)
	if n <= 3 {
		return string(digits)
	}
	var result strings.Builder
	for i, d := range digits {
		if i > 0 && (n-i)%3 == 0 {
			result.WriteByte('.')
		}
		result.WriteByte(d)
	}
	return result.String()
}

// StartScheduler starts the polling loop
func (p *Provider) StartScheduler(ctx context.Context, notifyFunc func(message string)) {
	interval := p.CheckInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	// jitteredInterval adds ±10% random jitter
	jitteredInterval := func() time.Duration {
		jitter := float64(interval) * 0.1
		return interval + time.Duration(rand.Float64()*2*jitter-jitter)
	}

	timer := time.NewTimer(jitteredInterval())
	defer timer.Stop()

	p.Logger.Info("BookingKAI scheduler started", "interval", interval, "target", p.TrainName)

	for {
		select {
		case <-ctx.Done():
			// Cleanup browser on shutdown
			if p.browser != nil {
				p.browser.Close()
			}
			return
		case <-timer.C:
			timer.Reset(jitteredInterval())

			if p.status.IsPaused() {
				continue
			}

			p.status.RecordCheckStart()

			p.Logger.Debug("Scheduler checking BookingKAI...")
			trains, err := p.Search(ctx)
			if err != nil {
				p.Logger.Error("Poll failed", "error", err)
				p.status.RecordCheckError(err.Error())
				p.history.Add(common.CheckResult{
					Timestamp: time.Now(),
					Error:     err.Error(),
				})
				continue
			}

			// Filter for AVAILABLE trains only
			var availableTrains []common.Train
			for _, t := range trains {
				if t.SeatsLeft != "0" && t.SeatsLeft != "" {
					availableTrains = append(availableTrains, t)
				}
			}

			p.status.RecordCheckSuccess(len(availableTrains) > 0)

			p.history.Add(common.CheckResult{
				Timestamp:       time.Now(),
				TrainsFound:     len(trains),
				AvailableTrains: availableTrains,
			})

			if len(availableTrains) > 0 {
				msg := fmt.Sprintf("🏛️ #%d BOOKINGKAI [%s] %s→%s\n✅ %s tersedia! (%d found)\n",
					p.Index, p.Date, p.Origin, p.Destination, p.TrainName, len(availableTrains))
				if p.Notes != "" {
					msg += fmt.Sprintf("📝 %s\n", p.Notes)
				}
				msg += "\n"
				for _, t := range availableTrains {
					msg += fmt.Sprintf("• %s [%s]\n  💺 @ %s\n", t.Name, t.Class, t.Price)
				}
				notifyFunc(msg)
			}
		}
	}
}

// GetHistory returns the last N check results
func (p *Provider) GetHistory(n int) []common.CheckResult {
	return p.history.GetLast(n)
}

// GetStatus returns the current status of the provider
func (p *Provider) GetStatus() common.ProviderStatus {
	startTime, total, success, failed, lastTime, lastFound, lastErr := p.status.GetStats()
	return common.ProviderStatus{
		StartTime:        startTime,
		TotalChecks:      total,
		SuccessfulChecks: success,
		FailedChecks:     failed,
		LastCheckTime:    lastTime,
		LastCheckFound:   lastFound,
		LastCheckError:   lastErr,
		Origin:           p.Origin,
		Destination:      p.Destination,
		Date:             p.Date,
		TrainName:        p.TrainName,
		Interval:         p.CheckInterval,
	}
}

// SetPaused sets the paused state
func (p *Provider) SetPaused(paused bool) {
	p.status.SetPaused(paused)
}

// IsPaused returns whether the provider is paused
func (p *Provider) IsPaused() bool {
	return p.status.IsPaused()
}
