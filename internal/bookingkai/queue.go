// Package bookingkai provides train search functionality by scraping booking.kai.id
package bookingkai

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Danny-Dasilva/CycleTLS/cycletls"
	"github.com/RomainMichau/cloudscraper_go/cloudscraper"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/stealth"

	"golang.org/x/net/html"

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
}

// BrowserQueue serializes all browser requests to booking.kai.id through a
// single worker goroutine. It first tries a stealth headless Chrome browser
// (go-rod + stealth), and falls back to cloudscraper (JA3 spoofing) if
// Cloudflare still blocks.
type BrowserQueue struct {
	logger   *slog.Logger
	proxyURL string
	browser  *rod.Browser
	scraper  cloudscraper.CloudScraper
	jobs     chan Job
	done     chan struct{}
}

// NewBrowserQueue creates a shared browser queue with stealth browser + cloudscraper fallback.
// All bookingkai providers should share the same queue so requests are serialized.
func NewBrowserQueue(logger *slog.Logger, proxyURL string) *BrowserQueue {
	// --- 1. Launch stealth browser ---
	l := launcher.New().
		Headless(true).
		Set("disable-blink-features", "AutomationControlled").
		Set("disable-gpu").
		Set("no-sandbox").
		Set("disable-dev-shm-usage").
		Set("disable-infobars").
		Set("window-size", "1920,1080").
		Set("lang", "id-ID,id,en-US,en")

	if proxyURL != "" {
		chromeProxy := strings.Replace(proxyURL, "socks5h://", "socks5://", 1)
		l = l.Set("proxy-server", chromeProxy)
		logger.Info("BookingKAI browser using proxy", "proxy", chromeProxy)
	}

	var browser *rod.Browser
	controlURL, err := l.Launch()
	if err != nil {
		logger.Warn("Failed to launch browser, will use cloudscraper only", "error", err)
	} else {
		browser = rod.New().ControlURL(controlURL)
		if err := browser.Connect(); err != nil {
			logger.Warn("Failed to connect to browser, will use cloudscraper only", "error", err)
			browser = nil
		} else {
			browser.IgnoreCertErrors(true)
		}
	}

	// --- 2. Initialize cloudscraper (JA3 spoofing) ---
	scraper, err := cloudscraper.Init(false, false)
	if err != nil {
		logger.Warn("Failed to init cloudscraper", "error", err)
	}

	q := &BrowserQueue{
		logger:   logger,
		proxyURL: proxyURL,
		browser:  browser,
		scraper:  scraper,
		jobs:     make(chan Job, 64),
		done:     make(chan struct{}),
	}
	go q.worker()
	logger.Info("BookingKAI queue started",
		"browser", browser != nil,
		"cloudscraper", scraper != nil,
		"proxy", proxyURL)
	return q
}

// worker processes jobs one at a time from the queue channel.
func (q *BrowserQueue) worker() {
	defer close(q.done)
	for job := range q.jobs {
		trains, err := q.doFetch(job.Ctx, job.SearchURL)
		job.Result <- JobResult{Trains: trains, Err: err}
	}
}

// doFetch tries the stealth browser first, and falls back to cloudscraper
// if Cloudflare blocks the browser.
func (q *BrowserQueue) doFetch(ctx context.Context, searchURL string) ([]common.Train, error) {
	// Try browser first
	if q.browser != nil {
		trains, err := q.fetchViaBrowser(ctx, searchURL)
		if err == nil {
			return trains, nil
		}
		q.logger.Warn("Browser fetch failed, trying cloudscraper fallback", "error", err)
	}

	// Fallback: cloudscraper (JA3 spoofing)
	if q.scraper != nil {
		return q.fetchViaCloudscraper(searchURL)
	}

	return nil, fmt.Errorf("both browser and cloudscraper unavailable")
}

// fetchViaBrowser uses stealth headless Chrome to fetch the page.
func (q *BrowserQueue) fetchViaBrowser(ctx context.Context, searchURL string) ([]common.Train, error) {
	q.logger.Debug("Browser navigating (stealth)", "url", searchURL)

	page, err := stealth.Page(q.browser)
	if err != nil {
		return nil, fmt.Errorf("failed to create stealth page: %w", err)
	}
	defer page.Close()

	page = page.Context(ctx).Timeout(90 * time.Second)

	if err := page.Navigate(searchURL); err != nil {
		return nil, fmt.Errorf("navigation failed: %w", err)
	}

	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("page load failed: %w", err)
	}

	if err := page.WaitStable(1 * time.Second); err != nil {
		q.logger.Debug("WaitStable timeout, continuing with current page state")
	}

	htmlContent, err := page.HTML()
	if err != nil {
		return nil, fmt.Errorf("failed to get page HTML: %w", err)
	}

	// Check for Cloudflare challenge — wait for auto-resolve
	if isCloudflareChallenge(htmlContent) {
		q.logger.Info("Cloudflare challenge detected, waiting for auto-resolve...")

		for i := 0; i < 10; i++ {
			time.Sleep(3 * time.Second)

			htmlContent, err = page.HTML()
			if err != nil {
				return nil, fmt.Errorf("failed to get page HTML during CF wait: %w", err)
			}

			if !isCloudflareChallenge(htmlContent) {
				q.logger.Info("Cloudflare challenge resolved!")
				break
			}
			q.logger.Debug("Still waiting for Cloudflare...", "attempt", i+1)
		}

		if isCloudflareChallenge(htmlContent) {
			return nil, fmt.Errorf("blocked by Cloudflare challenge (timeout after 30s)")
		}
	}

	if strings.Contains(htmlContent, "cfwaitingroom") || strings.Contains(htmlContent, "Waiting Room") {
		return nil, fmt.Errorf("blocked by Cloudflare Waiting Room")
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
			"accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
			"accept-language":           "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7",
			"cache-control":             "no-cache",
			"pragma":                    "no-cache",
			"sec-ch-ua":                 `"Not:A-Brand";v="99", "Brave";v="133", "Chromium";v="133"`,
			"sec-ch-ua-mobile":          "?0",
			"sec-ch-ua-platform":        `"Windows"`,
			"sec-fetch-dest":            "document",
			"sec-fetch-mode":            "navigate",
			"sec-fetch-site":            "same-origin",
			"sec-fetch-user":            "?1",
			"sec-gpc":                   "1",
			"upgrade-insecure-requests": "1",
			"referer":                   "https://booking.kai.id/",
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
func (q *BrowserQueue) Enqueue(ctx context.Context, searchURL string) ([]common.Train, error) {
	resultCh := make(chan JobResult, 1)
	job := Job{
		Ctx:       ctx,
		SearchURL: searchURL,
		Result:    resultCh,
	}

	select {
	case q.jobs <- job:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case res := <-resultCh:
		return res.Trains, res.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close shuts down the browser and queue worker gracefully.
func (q *BrowserQueue) Close() {
	close(q.jobs)
	<-q.done
	if q.browser != nil {
		q.browser.Close()
	}
	q.logger.Info("BookingKAI browser queue stopped")
}
