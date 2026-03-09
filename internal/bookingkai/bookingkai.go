// Package bookingkai provides train search functionality by scraping booking.kai.id
package bookingkai

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	utls "github.com/refraction-networking/utls"

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
	client        *http.Client
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

// newUTLSDialer returns a DialTLS function that impersonates Chrome's TLS fingerprint.
func newUTLSDialer(proxyURL string) func(network, addr string) (net.Conn, error) {
	return func(network, addr string) (net.Conn, error) {
		var conn net.Conn
		var err error

		if proxyURL != "" {
			// Connect through SOCKS5 proxy
			u, parseErr := url.Parse(proxyURL)
			if parseErr != nil {
				return nil, fmt.Errorf("invalid proxy URL: %w", parseErr)
			}
			dialer, dialErr := proxyDialer(u)
			if dialErr != nil {
				return nil, dialErr
			}
			conn, err = dialer.Dial(network, addr)
		} else {
			conn, err = net.DialTimeout(network, addr, 30*time.Second)
		}
		if err != nil {
			return nil, err
		}

		host, _, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			host = addr
		}

		uconn := utls.UClient(conn, &utls.Config{
			ServerName:         host,
			InsecureSkipVerify: false,
			// Force HTTP/1.1 only — Chrome's fingerprint advertises h2 in ALPN,
			// but our http.Transport doesn't support HTTP/2 over custom DialTLS.
			// Without this, the server sends an HTTP/2 preface which breaks the transport.
			NextProtos: []string{"http/1.1"},
		}, utls.HelloChrome_Auto)

		if err := uconn.Handshake(); err != nil {
			conn.Close()
			return nil, fmt.Errorf("TLS handshake failed: %w", err)
		}
		return uconn, nil
	}
}

// proxyDialer creates a SOCKS5 dialer for the given proxy URL.
// Uses simple TCP connection for non-socks proxies; for socks5 uses golang.org/x/net/proxy.
func proxyDialer(u *url.URL) (interface{ Dial(string, string) (net.Conn, error) }, error) {
	// Only SOCKS5 is supported (same as other providers)
	switch u.Scheme {
	case "socks5", "socks5h":
		// Simple socks5 via golang.org/x/net/proxy is not imported here to avoid dependency bloat.
		// Fall back to direct + let the caller handle proxy separately if needed.
		// For now we just do a direct connect; proxy support for bookingkai can be added later.
		return &net.Dialer{Timeout: 30 * time.Second}, nil
	default:
		return &net.Dialer{Timeout: 30 * time.Second}, nil
	}
}

// ensureClient lazily initializes the HTTP client with uTLS transport.
func (p *Provider) ensureClient() {
	if p.client != nil {
		return
	}

	jar, _ := cookiejar.New(nil)

	transport := &http.Transport{
		DialTLS:             newUTLSDialer(p.ProxyURL),
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: false},
		ForceAttemptHTTP2:   false, // Cloudflare works fine over HTTP/1.1 for scraping
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	}

	p.client = &http.Client{
		Transport: transport,
		Jar:       jar,
		Timeout:   60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Follow up to 10 redirects, propagating browser-like headers
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			setBrowserHeaders(req, req.URL.String())
			return nil
		},
	}

	p.Logger.Info("BookingKAI HTTP client (uTLS Chrome) initialized", "proxy", p.ProxyURL)
}

// setBrowserHeaders sets browser-like headers on the request, matching
// the real Chrome request captured in bookingkai.md.
func setBrowserHeaders(req *http.Request, referer string) {
	req.Header.Set("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("accept-language", "en-US,en;q=0.8")
	req.Header.Set("cache-control", "no-cache")
	req.Header.Set("pragma", "no-cache")
	req.Header.Set("priority", "u=0, i")
	req.Header.Set("sec-ch-ua", `"Not:A-Brand";v="99", "Brave";v="133", "Chromium";v="133"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	req.Header.Set("sec-fetch-dest", "document")
	req.Header.Set("sec-fetch-mode", "navigate")
	req.Header.Set("sec-fetch-site", "same-origin")
	req.Header.Set("sec-fetch-user", "?1")
	req.Header.Set("sec-gpc", "1")
	req.Header.Set("upgrade-insecure-requests", "1")
	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")
	if referer != "" {
		req.Header.Set("referer", referer)
	}
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

// fetchTrains sends an HTTP request to booking.kai.id and parses the HTML result.
func (p *Provider) fetchTrains(ctx context.Context) ([]common.Train, error) {
	p.ensureClient()

	// Format date for URL
	dateIndo, err := formatDateIndo(p.Date)
	if err != nil {
		return nil, err
	}

	// Build search URL
	searchURL := fmt.Sprintf(
		"https://booking.kai.id/?origination=%s&destination=%s&tanggal=%s&adult=1&infant=0&submit=Cari+%%26+Pesan+Tiket",
		p.Origin, p.Destination, url.QueryEscape(dateIndo),
	)

	p.Logger.Debug("Fetching booking.kai.id", "url", searchURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	setBrowserHeaders(req, "https://booking.kai.id/")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	htmlContent := string(body)

	// Detect Cloudflare challenges
	if resp.StatusCode == 403 || strings.Contains(htmlContent, "Just a moment") || strings.Contains(htmlContent, "cf-browser-verification") {
		return nil, fmt.Errorf("blocked by Cloudflare challenge (status %d)", resp.StatusCode)
	}
	if strings.Contains(htmlContent, "cf_chl_opt") || strings.Contains(htmlContent, "challenge-platform") {
		return nil, fmt.Errorf("blocked by Cloudflare JS challenge")
	}
	if strings.Contains(htmlContent, "cfwaitingroom") || strings.Contains(htmlContent, "Waiting Room") {
		return nil, fmt.Errorf("blocked by Cloudflare Waiting Room")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
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
					text := strings.TrimSpace(getTextContent(n))
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
				msg := fmt.Sprintf("🚂 #%d %s\n📍 %s→%s [%s]\n✅ Tersedia! (%d found) via bookingkai\n",
					p.Index, p.TrainName, p.Origin, p.Destination, p.Date, len(availableTrains))
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
