// Package bookingkai provides train search functionality by scraping booking.kai.id
package bookingkai

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
// single worker goroutine, using a stealth headless Chrome browser (go-rod +
// stealth) to bypass Cloudflare challenges.
type BrowserQueue struct {
	logger  *slog.Logger
	browser *rod.Browser
	jobs    chan Job
	done    chan struct{}
}

// NewBrowserQueue creates a shared browser queue with a stealth headless Chrome.
// All bookingkai providers should share the same queue so requests are serialized.
func NewBrowserQueue(logger *slog.Logger, proxyURL string) *BrowserQueue {
	// Configure browser launcher with anti-detection flags
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
		// Chrome doesn't support socks5h:// — convert to socks5://
		// Chrome's SOCKS5 already resolves DNS remotely by default.
		chromeProxy := strings.Replace(proxyURL, "socks5h://", "socks5://", 1)
		l = l.Set("proxy-server", chromeProxy)
		logger.Info("BookingKAI browser using proxy", "proxy", chromeProxy)
	}

	controlURL, err := l.Launch()
	if err != nil {
		logger.Error("Failed to launch browser", "error", err)
		q := &BrowserQueue{
			logger: logger,
			jobs:   make(chan Job, 64),
			done:   make(chan struct{}),
		}
		go q.worker()
		return q
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		logger.Error("Failed to connect to browser", "error", err)
		q := &BrowserQueue{
			logger: logger,
			jobs:   make(chan Job, 64),
			done:   make(chan struct{}),
		}
		go q.worker()
		return q
	}

	// Ignore certificate errors (useful with some proxies)
	browser.IgnoreCertErrors(true)

	q := &BrowserQueue{
		logger:  logger,
		browser: browser,
		jobs:    make(chan Job, 64),
		done:    make(chan struct{}),
	}
	go q.worker()
	logger.Info("BookingKAI stealth browser queue started", "proxy", proxyURL)
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

// doFetch navigates a stealth browser page to the search URL and extracts train data.
func (q *BrowserQueue) doFetch(ctx context.Context, searchURL string) ([]common.Train, error) {
	if q.browser == nil {
		return nil, fmt.Errorf("browser not initialized — launch failed at startup")
	}

	q.logger.Debug("Browser navigating (stealth)", "url", searchURL)

	// Create a stealth page — patches navigator.webdriver, chrome runtime,
	// plugins, languages, etc. to evade bot detection.
	page, err := stealth.Page(q.browser)
	if err != nil {
		return nil, fmt.Errorf("failed to create stealth page: %w", err)
	}
	defer page.Close()

	// Set a timeout for the entire navigation + wait cycle
	page = page.Context(ctx).Timeout(90 * time.Second)

	// Navigate to the search URL
	if err := page.Navigate(searchURL); err != nil {
		return nil, fmt.Errorf("navigation failed: %w", err)
	}

	// Wait for page to be fully loaded
	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("page load failed: %w", err)
	}

	// Wait for the page to stabilize (Cloudflare challenge + KAI page render)
	if err := page.WaitStable(1 * time.Second); err != nil {
		q.logger.Debug("WaitStable timeout, continuing with current page state")
	}

	// Get the page HTML
	htmlContent, err := page.HTML()
	if err != nil {
		return nil, fmt.Errorf("failed to get page HTML: %w", err)
	}

	// Check if we're on a Cloudflare challenge page — wait for auto-resolve
	if isCloudflareChallenge(htmlContent) {
		q.logger.Info("Cloudflare challenge detected, waiting for auto-resolve...")

		// Poll every 3 seconds for up to 30 seconds
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

		// Final check
		if isCloudflareChallenge(htmlContent) {
			return nil, fmt.Errorf("blocked by Cloudflare challenge (timeout after 30s)")
		}
	}

	if strings.Contains(htmlContent, "cfwaitingroom") || strings.Contains(htmlContent, "Waiting Room") {
		return nil, fmt.Errorf("blocked by Cloudflare Waiting Room")
	}

	// Parse trains from HTML
	trains, err := parseHTML(htmlContent)
	if err != nil {
		return nil, fmt.Errorf("HTML parsing failed: %w", err)
	}

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
