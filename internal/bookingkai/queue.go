// Package bookingkai provides train search functionality by scraping booking.kai.id
package bookingkai

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/http2"

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

// BrowserQueue serializes all HTTP requests to booking.kai.id through a single
// worker goroutine, preventing concurrent requests that could trigger rate
// limiting or Cloudflare challenges.
type BrowserQueue struct {
	logger   *slog.Logger
	proxyURL string
	client   *http.Client
	jobs     chan Job
	done     chan struct{}
}

// NewBrowserQueue creates a shared browser queue. All bookingkai providers
// should share the same queue instance so that requests are serialized.
func NewBrowserQueue(logger *slog.Logger, proxyURL string) *BrowserQueue {
	q := &BrowserQueue{
		logger:   logger,
		proxyURL: proxyURL,
		jobs:     make(chan Job, 64),
		done:     make(chan struct{}),
	}
	q.initClient()
	go q.worker()
	logger.Info("BookingKAI browser queue started", "proxy", proxyURL)
	return q
}

// initClient creates the shared HTTP client with uTLS + HTTP/2.
func (q *BrowserQueue) initClient() {
	jar, _ := cookiejar.New(nil)
	proxyURL := q.proxyURL

	t2 := &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return dialUTLS(ctx, network, addr, proxyURL)
		},
		AllowHTTP: false,
	}

	q.client = &http.Client{
		Transport: t2,
		Jar:       jar,
		Timeout:   60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			setBrowserHeaders(req, req.URL.String())
			return nil
		},
	}
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

// worker processes jobs one at a time from the queue channel.
func (q *BrowserQueue) worker() {
	defer close(q.done)
	for job := range q.jobs {
		trains, err := q.doFetch(job.Ctx, job.SearchURL)
		job.Result <- JobResult{Trains: trains, Err: err}
	}
}

// doFetch performs the actual HTTP request and HTML parsing.
func (q *BrowserQueue) doFetch(ctx context.Context, searchURL string) ([]common.Train, error) {
	q.logger.Debug("Queue processing request", "url", searchURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	setBrowserHeaders(req, "https://booking.kai.id/")

	resp, err := q.client.Do(req)
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

	return trains, nil
}

// parseHTML extracts train information from the booking.kai.id search results page.
// (Moved here from bookingkai.go since it's used by the queue worker.)
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

// Close shuts down the queue worker gracefully.
func (q *BrowserQueue) Close() {
	close(q.jobs)
	<-q.done
	q.logger.Info("BookingKAI browser queue stopped")
}
