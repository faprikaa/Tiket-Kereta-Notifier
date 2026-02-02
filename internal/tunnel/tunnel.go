// Package tunnel provides Cloudflare Quick Tunnel functionality
package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Tunnel represents a cloudflared tunnel instance
type Tunnel struct {
	URL     string
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	logger  *slog.Logger
	started bool
	mu      sync.Mutex
}

// New creates a new tunnel instance
func New(logger *slog.Logger) *Tunnel {
	return &Tunnel{
		logger: logger,
	}
}

// Start starts cloudflared tunnel pointing to the given local URL
// Returns the public trycloudflare.com URL
func (t *Tunnel) Start(ctx context.Context, localURL string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started {
		return t.URL, nil
	}

	// Create cancellable context for the tunnel process
	tunnelCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel

	// Start cloudflared tunnel
	t.cmd = exec.CommandContext(tunnelCtx, "cloudflared", "tunnel", "--url", localURL)

	// Get stderr pipe (cloudflared outputs to stderr)
	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		cancel()
		return "", fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	// Start the process
	if err := t.cmd.Start(); err != nil {
		cancel()
		return "", fmt.Errorf("failed to start cloudflared: %w", err)
	}

	t.logger.Info("Starting cloudflared tunnel...", "local_url", localURL)

	// Parse output to find the public URL
	urlChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(stderr)
		// Match URL pattern like https://something.trycloudflare.com
		urlRegex := regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

		for scanner.Scan() {
			line := scanner.Text()
			t.logger.Debug("cloudflared", "output", line)

			// Look for the tunnel URL
			if matches := urlRegex.FindString(line); matches != "" {
				select {
				case urlChan <- matches:
				default:
				}
			}

			// Check for errors
			if strings.Contains(line, "error") || strings.Contains(line, "failed") {
				if strings.Contains(line, "not found") || strings.Contains(line, "not installed") {
					errChan <- fmt.Errorf("cloudflared not installed: %s", line)
					return
				}
			}
		}
	}()

	// Wait for URL with timeout
	select {
	case url := <-urlChan:
		t.URL = url
		t.started = true
		t.logger.Info("Tunnel started", "public_url", url)
		return url, nil
	case err := <-errChan:
		t.Stop()
		return "", err
	case <-time.After(30 * time.Second):
		t.Stop()
		return "", fmt.Errorf("timeout waiting for tunnel URL")
	case <-ctx.Done():
		t.Stop()
		return "", ctx.Err()
	}
}

// Stop stops the cloudflared tunnel
func (t *Tunnel) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started {
		return
	}

	t.logger.Info("Stopping cloudflared tunnel...")

	if t.cancel != nil {
		t.cancel()
	}

	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		_ = t.cmd.Wait()
	}

	t.started = false
	t.URL = ""
	t.logger.Info("Tunnel stopped")
}

// IsRunning returns whether the tunnel is currently running
func (t *Tunnel) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.started
}

// GetURL returns the public tunnel URL
func (t *Tunnel) GetURL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.URL
}
