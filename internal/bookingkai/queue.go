// Package bookingkai provides train search functionality by scraping booking.kai.id
package bookingkai

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/RomainMichau/CycleTLS/cycletls"
	"github.com/RomainMichau/cloudscraper_go/cloudscraper"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/stealth"

	"golang.org/x/net/html"
	"golang.org/x/net/proxy"

	"tiket-kereta-notifier/internal/common"
)

// Job represents a single search request to be processed by the queue.
type Job struct {
	Ctx       context.Context
	SearchURL string
	Result    chan<- JobResult
}

// JobResult holds the result of a search job.
type JobResult struct {
	Trains []common.Train
	Err    error
	Method string
}

// BrowserQueue serializes all booking.kai.id requests through one persistent
// Chromium page.
type BrowserQueue struct {
	logger             *slog.Logger
	proxyURL           string
	browser            *rod.Browser
	page               *rod.Page
	scraper            *cloudscraper.CloudScrapper
	browserFetch       func(context.Context, string) ([]common.Train, error)
	jobs               chan Job
	done               chan struct{}
	notifyFunc         func(string)
	challengeFailures  int
	nextChallengeRetry time.Time
	lastChallengeNotif time.Time
}

// SetNotifyFunc sets the Telegram notification callback.
func (q *BrowserQueue) SetNotifyFunc(fn func(string)) {
	q.notifyFunc = fn
}

func (q *BrowserQueue) notify(msg string) {
	if q.notifyFunc != nil {
		q.notifyFunc(msg)
	}
}

// NewBrowserQueue creates a shared persistent Chromium queue.
// All bookingkai providers should share the same queue so requests are serialized.
// display: X display to use (e.g. ":10"). xauthority: path to Xauthority file (optional).
func NewBrowserQueue(logger *slog.Logger, proxyURL, display, xauthority, chromiumPath, userDataDir string, headless bool) (*BrowserQueue, error) {
	// Only override DISPLAY/XAUTHORITY when explicitly configured; otherwise
	// let go-rod (and the OS environment) handle display/auth automatically.
	if display != "" {
		os.Setenv("DISPLAY", display)
	}
	if xauthority != "" {
		os.Setenv("XAUTHORITY", xauthority)
	}

	logger.Info("Launching browser",
		"display", display,
		"xauthority", xauthority,
		"headless", headless)

	chromiumBin := chromiumPath
	if chromiumBin == "" {
		chromiumBin = findChromiumBin()
	}
	if chromiumBin == "" {
		return nil, fmt.Errorf("Chromium binary not found")
	}
	logger.Info("Using chromium binary", "path", chromiumBin)

	l := launcher.New().
		Headless(headless).
		Bin(chromiumBin).
		Set("disable-blink-features", "AutomationControlled").
		Set("disable-dev-shm-usage").
		Set("window-size", "1920,1080").
		Set("lang", "id-ID,id,en-US,en")
	if userDataDir != "" {
		if err := os.MkdirAll(userDataDir, 0o700); err != nil {
			return nil, fmt.Errorf("create Chromium user data directory: %w", err)
		}
		l = l.UserDataDir(userDataDir)
	}

	if os.Getuid() == 0 {
		l = l.Set("no-sandbox")
	}

	if proxyURL != "" {
		chromeProxy := proxyURL
		chromeProxy = strings.Replace(chromeProxy, "socks5h://", "socks5://", 1)
		l = l.Set("proxy-server", chromeProxy)
		logger.Info("BookingKAI browser using proxy", "proxy", chromeProxy)
	}

	var browser *rod.Browser
	var persistentPage *rod.Page
	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("launch Chromium: %w", err)
	}
	browser = rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("connect to Chromium: %w", err)
	}
	browser.IgnoreCertErrors(true)

	// Create a persistent page to retain cookies across requests.
	persistentPage, err = stealth.Page(browser)
	if err != nil {
		_ = browser.Close()
		return nil, fmt.Errorf("create Chromium page: %w", err)
	}
	// Warm up the persistent session before accepting queue jobs.
	logger.Info("Warming up browser — visiting booking.kai.id homepage...")
	warmCtx, warmCancel := context.WithTimeout(context.Background(), 60*time.Second)
	warmPage := persistentPage.Context(warmCtx)
	if navErr := warmPage.Navigate("https://booking.kai.id/"); navErr != nil {
		logger.Warn("Warmup navigation failed", "error", navErr)
	} else {
		_ = warmPage.WaitLoad()
		for i := 0; i < 15; i++ {
			time.Sleep(2 * time.Second)
			htmlContent, _ := warmPage.HTML()
			if !isCloudflareChallenge(htmlContent) {
				logger.Info("✅ Browser warmup complete")
				break
			}
			logger.Debug("Warmup: waiting for challenge...", "attempt", i+1)
		}
	}
	warmCancel()

	q := &BrowserQueue{
		logger:   logger,
		proxyURL: proxyURL,
		browser:  browser,
		page:     persistentPage,
		jobs:     make(chan Job, 64),
		done:     make(chan struct{}),
	}
	go q.worker()
	logger.Info("BookingKAI queue started",
		"headless", headless,
		"user_data_dir", userDataDir,
		"proxy", proxyURL)

	// Verify proxy IP if proxy is configured
	if proxyURL != "" {
		checkProxyIP(logger, proxyURL)
	}

	return q, nil
}

func findChromiumBin() string {
	candidates := []string{
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// checkProxyIP verifies the proxy is working by fetching the public IP
// from ifconfig.me through both direct and proxied connections for comparison.

func checkProxyIP(logger *slog.Logger, proxyURL string) {
	const ipCheckURL = "https://ifconfig.me/ip"
	httpTimeout := 15 * time.Second

	// 1. Get direct IP (no proxy)
	directClient := &http.Client{Timeout: httpTimeout}
	directIP := fetchIP(directClient, ipCheckURL)

	// 2. Get proxied IP
	proxyParsed, err := url.Parse(proxyURL)
	if err != nil {
		logger.Warn("Failed to parse proxy URL for IP check", "proxy", proxyURL, "error", err)
		return
	}

	proxyTransport := &http.Transport{
		Proxy: http.ProxyURL(proxyParsed),
	}
	proxyClient := &http.Client{
		Timeout:   httpTimeout,
		Transport: proxyTransport,
	}
	proxyIP := fetchIP(proxyClient, ipCheckURL)

	if directIP != "" && proxyIP != "" {
		if directIP == proxyIP {
			logger.Warn("⚠️ Proxy IP same as direct IP! Proxy may not be working",
				"direct_ip", directIP, "proxy_ip", proxyIP, "proxy", proxyURL)
		} else {
			logger.Info("✅ Proxy IP verified",
				"direct_ip", directIP, "proxy_ip", proxyIP, "proxy", proxyURL)
		}
	} else if proxyIP != "" {
		logger.Info("✅ Proxy IP check OK", "proxy_ip", proxyIP, "proxy", proxyURL)
	} else if directIP != "" {
		logger.Warn("⚠️ Failed to get IP via proxy, proxy may be down",
			"direct_ip", directIP, "proxy", proxyURL)
	} else {
		logger.Warn("⚠️ Could not check IP (both direct and proxy failed)")
	}
}

// fetchIP fetches the public IP from the given URL using the provided HTTP client.
// Returns the IP string or empty string on error.
func fetchIP(client *http.Client, checkURL string) string {
	resp, err := client.Get(checkURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// worker processes jobs one at a time from the queue channel.
func (q *BrowserQueue) worker() {
	defer close(q.done)
	for job := range q.jobs {
		trains, method, err := q.doFetch(job.Ctx, job.SearchURL)
		job.Result <- JobResult{Trains: trains, Err: err, Method: method}
	}
}

// doFetch uses Chromium as BookingKAI's only fetch adapter.
func (q *BrowserQueue) doFetch(ctx context.Context, searchURL string) ([]common.Train, string, error) {
	if time.Now().Before(q.nextChallengeRetry) {
		return nil, "browser", fmt.Errorf("BookingKAI challenge backoff active until %s", q.nextChallengeRetry.Format(time.RFC3339))
	}

	fetch := q.browserFetch
	if fetch == nil {
		fetch = q.fetchViaBrowser
	}
	trains, err := fetch(ctx, searchURL)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cloudflare") || strings.Contains(strings.ToLower(err.Error()), "captcha") {
			q.recordChallenge(err)
		}
		return nil, "browser", err
	}
	q.challengeFailures = 0
	q.nextChallengeRetry = time.Time{}
	return trains, "browser", nil
}

func (q *BrowserQueue) recordChallenge(cause error) {
	q.challengeFailures++
	q.nextChallengeRetry = time.Now().Add(challengeBackoff(q.challengeFailures))
	if time.Since(q.lastChallengeNotif) >= 15*time.Minute {
		q.notify(fmt.Sprintf("⚠️ BookingKAI terkena challenge. Intervensi manual mungkin diperlukan. Retry setelah %s. Error: %v", q.nextChallengeRetry.Format(time.RFC3339), cause))
		q.lastChallengeNotif = time.Now()
	}
}

func challengeBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 30 * time.Second
	for i := 1; i < attempt && delay < 15*time.Minute; i++ {
		delay *= 2
	}
	if delay > 15*time.Minute {
		return 15 * time.Minute
	}
	return delay
}

// fetchViaCurl uses curl_chrome116 to fetch the page with a real TLS fingerprint.
// This is the fastest and most reliable method for bypassing Cloudflare.
func (q *BrowserQueue) fetchViaCurl(ctx context.Context, searchURL string) ([]common.Train, error) {
	q.logger.Debug("Curl fetching", "url", searchURL)

	args := []string{
		"-s",       // Silent
		"-L",       // Follow redirects
		"-m", "60", // Timeout 60s
	}

	// Add proxy if configured
	if q.proxyURL != "" {
		args = append(args, "-x", q.proxyURL)
	}

	args = append(args,
		searchURL,
	)

	// Try curl_chrome116 first, fall back to regular curl
	curlBin := "curl"
	if _, err := exec.LookPath(curlBin); err != nil {
		curlBin = "curl"
	}

	cmd := exec.CommandContext(ctx, curlBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s execution failed: %w (output: %s)", curlBin, err, truncate(string(out), 200))
	}

	htmlContent := string(out)
	fmt.Println("url: ", searchURL)

	// Check for Cloudflare blocks
	if isCloudflareChallenge(htmlContent) {
		return nil, fmt.Errorf("%s blocked by Cloudflare challenge", curlBin)
	}
	if strings.Contains(htmlContent, "cfwaitingroom") || strings.Contains(htmlContent, "Waiting Room") {
		return nil, fmt.Errorf("%s blocked by Cloudflare Waiting Room", curlBin)
	}

	trains, err := parseHTML(htmlContent)
	if err != nil {
		return nil, fmt.Errorf("HTML parsing failed: %w", err)
	}

	q.logger.Info("Curl fetch successful", "trains", len(trains), "binary", curlBin)
	return trains, nil
}

// truncate limits string length for log output
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// fetchViaHTTP uses Go's native http.Client with optional SOCKS5/HTTP proxy.
// Similar approach to traveloka.go's createHTTPClient.
func (q *BrowserQueue) fetchViaHTTP(ctx context.Context, searchURL string) ([]common.Train, error) {
	q.logger.Debug("HTTP client fetching", "url", searchURL)

	transport := &http.Transport{}

	if q.proxyURL != "" {
		parsedURL, err := url.Parse(q.proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}

		if strings.HasPrefix(parsedURL.Scheme, "socks5") {
			// SOCKS5 proxy via golang.org/x/net/proxy
			dialer, err := proxy.FromURL(parsedURL, proxy.Direct)
			if err != nil {
				return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
			}
			if cd, ok := dialer.(proxy.ContextDialer); ok {
				transport.DialContext = cd.DialContext
			} else {
				transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				}
			}
		} else {
			// HTTP/HTTPS proxy
			transport.Proxy = http.ProxyURL(parsedURL)
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	htmlContent := string(body)

	if resp.StatusCode == 403 || isCloudflareChallenge(htmlContent) {
		return nil, fmt.Errorf("HTTP blocked by Cloudflare (status %d)", resp.StatusCode)
	}
	if strings.Contains(htmlContent, "cfwaitingroom") || strings.Contains(htmlContent, "Waiting Room") {
		return nil, fmt.Errorf("HTTP blocked by Cloudflare Waiting Room")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP unexpected status %d", resp.StatusCode)
	}

	trains, err := parseHTML(htmlContent)
	if err != nil {
		return nil, fmt.Errorf("HTML parsing failed: %w", err)
	}

	q.logger.Info("HTTP fetch successful", "trains", len(trains))
	return trains, nil
}

// fetchViaBrowser uses the persistent stealth Chrome page to fetch the page.
// Reusing the same page retains Cloudflare cookies (cf_clearance, __cf_bm, etc.)
// across requests, which is critical for not getting re-challenged.
func (q *BrowserQueue) fetchViaBrowser(ctx context.Context, searchURL string) ([]common.Train, error) {
	q.logger.Debug("Browser navigating (stealth, persistent page)", "url", searchURL)

	if q.page == nil {
		return nil, fmt.Errorf("no persistent browser page available")
	}

	page := q.page.Context(ctx).Timeout(90 * time.Second)

	if err := page.Navigate(searchURL); err != nil {
		return nil, fmt.Errorf("navigation failed: %w", err)
	}

	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("page load failed: %w", err)
	}

	if err := page.WaitStable(2 * time.Second); err != nil {
		q.logger.Debug("WaitStable timeout, continuing with current page state")
	}

	htmlContent, err := page.HTML()
	if err != nil {
		return nil, fmt.Errorf("failed to get page HTML: %w", err)
	}

	// Headless mode cannot solve interactive challenges. Return immediately so
	// the queue can apply bounded backoff without blocking other jobs.
	if isCloudflareChallenge(htmlContent) {
		return nil, fmt.Errorf("blocked by Cloudflare challenge or CAPTCHA; manual intervention required")
	}

	if strings.Contains(htmlContent, "cfwaitingroom") || strings.Contains(htmlContent, "Waiting Room") {
		return nil, fmt.Errorf("blocked by Cloudflare Waiting Room; retry later")
	}

	trains, err := parseHTML(htmlContent)
	if err != nil {
		return nil, fmt.Errorf("HTML parsing failed: %w", err)
	}

	return trains, nil
}

// fetchViaCloudscraper uses JA3 fingerprint spoofing to bypass Cloudflare.
func (q *BrowserQueue) fetchViaCloudscraper(searchURL string) ([]common.Train, error) {
	q.logger.Debug("Cloudscraper fetching", "url", searchURL)

	opts := cycletls.Options{
		Headers: map[string]string{
			"accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
			"accept-language": "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7",
			"cache-control":   "no-cache",
			"pragma":          "no-cache",
			// "sec-ch-ua":                 `"Not:A-Brand";v="99", "Chromium";v="137", "Google Chrome";v="137"`,
			"sec-ch-ua-mobile":          "?0",
			"sec-ch-ua-platform":        `"Windows"`,
			"sec-fetch-dest":            "document",
			"sec-fetch-mode":            "navigate",
			"sec-fetch-site":            "same-origin",
			"sec-fetch-user":            "?1",
			"sec-gpc":                   "1",
			"upgrade-insecure-requests": "1",
			"referer":                   "https://booking.kai.id/",
			// "user-agent":                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36",
		},
		Timeout: 60,
	}

	if q.proxyURL != "" {
		opts.Proxy = q.proxyURL
	}

	resp, err := q.scraper.Do(searchURL, opts, "GET")
	if err != nil {
		return nil, fmt.Errorf("cloudscraper request failed: %w", err)
	}

	htmlContent := resp.Body

	// Check for Cloudflare blocks
	if resp.Status == 403 || isCloudflareChallenge(htmlContent) {
		return nil, fmt.Errorf("cloudscraper blocked by Cloudflare (status %d)", resp.Status)
	}

	if resp.Status != 200 {
		return nil, fmt.Errorf("cloudscraper unexpected status %d", resp.Status)
	}

	trains, err := parseHTML(htmlContent)
	if err != nil {
		return nil, fmt.Errorf("HTML parsing failed: %w", err)
	}

	q.logger.Info("Cloudscraper fetch successful", "trains", len(trains))
	return trains, nil
}

// isCloudflareChallenge checks if the HTML indicates a Cloudflare challenge page.
func isCloudflareChallenge(htmlContent string) bool {
	return strings.Contains(htmlContent, "cf_chl_opt") ||
		strings.Contains(htmlContent, "challenge-platform") ||
		strings.Contains(htmlContent, "Just a moment") ||
		strings.Contains(htmlContent, "cf-browser-verification")
}

// parseHTML extracts train information from the booking.kai.id search results page.
func parseHTML(rawHTML string) ([]common.Train, error) {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return nil, err
	}

	var trains []common.Train

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

// Enqueue submits a search URL to the queue and blocks until the result is
// available. This is the main entry point for providers.
// Returns trains, the fetch method that succeeded, and any error.
func (q *BrowserQueue) Enqueue(ctx context.Context, searchURL string) ([]common.Train, string, error) {
	resultCh := make(chan JobResult, 1)
	job := Job{
		Ctx:       ctx,
		SearchURL: searchURL,
		Result:    resultCh,
	}

	select {
	case q.jobs <- job:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}

	select {
	case res := <-resultCh:
		return res.Trains, res.Method, res.Err
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
}

// Close shuts down the browser and queue worker gracefully.
func (q *BrowserQueue) Close() {
	close(q.jobs)
	<-q.done
	if q.page != nil {
		q.page.Close()
	}
	if q.browser != nil {
		q.browser.Close()
	}
	q.logger.Info("BookingKAI browser queue stopped")
}
