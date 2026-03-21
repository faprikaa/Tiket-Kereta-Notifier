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
	"github.com/go-rod/rod/lib/proto"

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
// single worker goroutine, using a real headless Chrome browser (go-rod) to
// bypass Cloudflare challenges.
type BrowserQueue struct {
	logger  *slog.Logger
	browser *rod.Browser
	jobs    chan Job
	done    chan struct{}
}

// NewBrowserQueue creates a shared browser queue with a headless Chrome instance.
// All bookingkai providers should share the same queue so requests are serialized.
func NewBrowserQueue(logger *slog.Logger, proxyURL string) *BrowserQueue {
	// Configure browser launcher
	l := launcher.New().
		Headless(true).
		Set("disable-gpu").
		Set("no-sandbox").
		Set("disable-dev-shm-usage")

	if proxyURL != "" {
		l = l.Set("proxy-server", proxyURL)
		logger.Info("BookingKAI browser using proxy", "proxy", proxyURL)
	}

	controlURL, err := l.Launch()
	if err != nil {
		logger.Error("Failed to launch browser", "error", err)
		// Return queue anyway — doFetch will fail with clear error
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
	logger.Info("BookingKAI browser queue started", "proxy", proxyURL)
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

// doFetch navigates the browser to the search URL and extracts train data.
func (q *BrowserQueue) doFetch(ctx context.Context, searchURL string) ([]common.Train, error) {
	if q.browser == nil {
		return nil, fmt.Errorf("browser not initialized — launch failed at startup")
	}

	q.logger.Debug("Browser navigating", "url", searchURL)

	// Create a new page for each request to avoid stale state
	page, err := q.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, fmt.Errorf("failed to create page: %w", err)
	}
	defer page.Close()

	// Set a timeout for the entire navigation + wait cycle
	page = page.Context(ctx).Timeout(60 * time.Second)

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

	// Check if we're still on a Cloudflare challenge page
	if strings.Contains(htmlContent, "cf_chl_opt") || strings.Contains(htmlContent, "challenge-platform") {
		// Wait longer for Cloudflare to resolve
		q.logger.Info("Cloudflare challenge detected, waiting for resolution...")
		time.Sleep(10 * time.Second)

		if err := page.WaitStable(2 * time.Second); err != nil {
			q.logger.Debug("WaitStable after CF challenge timeout")
		}

		htmlContent, err = page.HTML()
		if err != nil {
			return nil, fmt.Errorf("failed to get page HTML after CF wait: %w", err)
		}

		// Still blocked?
		if strings.Contains(htmlContent, "cf_chl_opt") || strings.Contains(htmlContent, "challenge-platform") {
			return nil, fmt.Errorf("blocked by Cloudflare JS challenge")
		}
	}

	if strings.Contains(htmlContent, "Just a moment") || strings.Contains(htmlContent, "cf-browser-verification") {
		return nil, fmt.Errorf("blocked by Cloudflare challenge page")
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
