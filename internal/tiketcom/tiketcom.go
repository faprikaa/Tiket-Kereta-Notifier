// Package tiketcom provides train search functionality using Tiket.com API
package tiketcom

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"tiket-kereta-notifier/internal/common"
	"tiket-kereta-notifier/internal/history"
)

// APIUrl is the Tiket.com train search API endpoint template
const APIUrl = "https://www.tiket.com/ms-gateway/tix-train-search-v2/v7/train/journeys"

// Provider implements common.Provider for Tiket.com
type Provider struct {
	Logger        *slog.Logger
	Origin        string
	Destination   string
	Date          string        // YYYYMMDD format
	TrainName     string        // Optional: specific train to monitor
	CheckInterval time.Duration // Polling interval
	ProxyURL      string        // SOCKS5 proxy URL (e.g., "socks5h://127.0.0.1:40000")
	history       *history.Store
	status        *common.StatusTracker
}

// NewProvider creates a new Tiket.com provider
func NewProvider(logger *slog.Logger, origin, dest, date, trainName string, interval time.Duration, proxyURL string) *Provider {
	return &Provider{
		Logger:        logger,
		Origin:        origin,
		Destination:   dest,
		Date:          date,
		TrainName:     trainName,
		CheckInterval: interval,
		ProxyURL:      proxyURL,
		history:       history.NewStore(100),
		status:        common.NewStatusTracker(),
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "Tiket.com"
}

// Response types matching the API response structure
type APIResponse struct {
	Code    string        `json:"code"`
	Message string        `json:"message"`
	Errors  interface{}   `json:"errors"`
	Data    *ResponseData `json:"data"`
}

type ResponseData struct {
	Parameter      *SearchParameter `json:"parameter"`
	DepartJourneys *JourneysData    `json:"departJourneys"`
}

type SearchParameter struct {
	DepartDate      string `json:"departDate"`
	OriginCode      string `json:"originCode"`
	OriginName      string `json:"originName"`
	DestinationCode string `json:"destinationCode"`
	DestinationName string `json:"destinationName"`
}

type JourneysData struct {
	Journeys []Journey `json:"journeys"`
}

type Journey struct {
	ID               string            `json:"id"`
	SegmentSchedules []SegmentSchedule `json:"segmentSchedules"`
	DepartDate       string            `json:"departDate"`
	DepartTime       string            `json:"departTime"`
	ArriveDate       string            `json:"arriveDate"`
	ArriveTime       string            `json:"arriveTime"`
	TotalDuration    float64           `json:"totalDuration"`
}

type SegmentSchedule struct {
	ID               string         `json:"id"`
	ScheduleID       string         `json:"scheduleId"`
	TrainNumber      string         `json:"trainNumber"`
	TrainName        string         `json:"trainName"`
	DepartureStation *Station       `json:"departureStation"`
	ArrivalStation   *Station       `json:"arrivalStation"`
	DepartureDate    string         `json:"departureDate"`
	DepartureTime    string         `json:"departureTime"`
	ArrivalDate      string         `json:"arrivalDate"`
	ArrivalTime      string         `json:"arrivalTime"`
	TripDuration     float64        `json:"tripDuration"`
	WagonClass       *WagonClass    `json:"wagonClass"`
	SubClass         *SubClass      `json:"subClass"`
	AvailableSeats   int            `json:"availableSeats"`
	ScheduleFares    []ScheduleFare `json:"scheduleFares"`
}

type Station struct {
	ID   int    `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

type WagonClass struct {
	ID     int    `json:"id"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type SubClass struct {
	ID   int    `json:"id"`
	Code string `json:"code"`
}

type ScheduleFare struct {
	PaxType     string  `json:"paxType"`
	PriceAmount float64 `json:"priceAmount"`
}

// Search performs a train search using Tiket.com API via curl_chrome110
func (p *Provider) Search(ctx context.Context) ([]common.Train, error) {
	if p.Origin == "" || p.Destination == "" {
		return nil, fmt.Errorf("origin and destination required")
	}

	p.Logger.Info("Searching Tiket.com", "origin", p.Origin, "dest", p.Destination, "date", p.Date)

	// Build URL
	url := fmt.Sprintf("%s?orig=%s&otype=STATION&dest=%s&dtype=STATION&ttype=ONE_WAY&ddate=%s&acount=1&icount=0",
		APIUrl, p.Origin, p.Destination, p.Date)

	// Use curl_chrome110 to bypass Cloudflare
	args := []string{
		"-s", // Silent mode
		"-L", // Follow redirects
	}

	// Add proxy if configured
	if p.ProxyURL != "" {
		args = append(args, "-x", p.ProxyURL)
	}

	args = append(args,
		"-H", "accept: application/json, text/plain, */*",
		"-H", "user-agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/110.0.0.0 Safari/537.36",
		"-H", "x-audience: tiket.com",
		url,
	)

	cmd := exec.CommandContext(ctx, "curl_chrome110", args...)
	out, err := cmd.Output()
	if err != nil {
		p.Logger.Error("Failed to execute curl_chrome110", "error", err)
		return nil, fmt.Errorf("curl execution failed: %w", err)
	}

	// Parse response
	var result APIResponse
	if err := json.Unmarshal(out, &result); err != nil {
		p.Logger.Error("Failed to parse response", "error", err, "body", string(out))
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Code != "SUCCESS" {
		p.Logger.Error("Tiket.com API error", "code", result.Code, "message", result.Message)
		return nil, fmt.Errorf("API error: %s - %s", result.Code, result.Message)
	}

	if result.Data == nil || result.Data.DepartJourneys == nil {
		return nil, fmt.Errorf("no journey data in response")
	}

	// Parse trains from response
	trains := p.parseTrains(result.Data.DepartJourneys.Journeys)

	// Filter by TrainName if configured
	if p.TrainName != "" {
		var filtered []common.Train
		target := strings.ToLower(p.TrainName)
		for _, t := range trains {
			if strings.Contains(strings.ToLower(t.Name), target) {
				filtered = append(filtered, t)
			}
		}
		return filtered, nil
	}

	return trains, nil
}

// parseTrains extracts train information from API response
func (p *Provider) parseTrains(journeys []Journey) []common.Train {
	var trains []common.Train

	for _, journey := range journeys {
		for _, seg := range journey.SegmentSchedules {
			// Get price from schedule fares
			price := ""
			for _, fare := range seg.ScheduleFares {
				if fare.PaxType == "ADULT" {
					price = fmt.Sprintf("Rp %.0f", fare.PriceAmount)
					break
				}
			}

			// Determine availability
			availability := "FULL"
			seatsLeft := fmt.Sprintf("%d", seg.AvailableSeats)
			if seg.AvailableSeats > 0 {
				availability = "AVAILABLE"
			}

			// Get class info
			class := ""
			if seg.WagonClass != nil {
				class = seg.WagonClass.Detail
				if seg.SubClass != nil {
					class = fmt.Sprintf("%s (%s)", seg.WagonClass.Detail, seg.SubClass.Code)
				}
			}

			// Format time (remove seconds)
			depTime := formatTime(seg.DepartureTime)
			arrTime := formatTime(seg.ArrivalTime)

			trains = append(trains, common.Train{
				Name:          seg.TrainName,
				Class:         class,
				Price:         price,
				DepartureTime: depTime,
				ArrivalTime:   arrTime,
				Availability:  availability,
				SeatsLeft:     seatsLeft,
			})
		}
	}

	return trains
}

// formatTime converts "HH:MM:SS" to "HH:MM"
func formatTime(t string) string {
	parts := strings.Split(t, ":")
	if len(parts) >= 2 {
		return parts[0] + ":" + parts[1]
	}
	return t
}

// SearchAll returns all trains without filtering by TrainName
func (p *Provider) SearchAll(ctx context.Context) ([]common.Train, error) {
	// Temporarily clear TrainName filter
	originalName := p.TrainName
	p.TrainName = ""
	defer func() { p.TrainName = originalName }()

	return p.Search(ctx)
}

// StartScheduler starts the polling loop
func (p *Provider) StartScheduler(ctx context.Context, notifyFunc func(message string)) {
	interval := p.CheckInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	p.Logger.Info("Tiket.com scheduler started", "interval", interval, "target", p.TrainName)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.status.RecordCheckStart()

			p.Logger.Debug("Scheduler checking Tiket.com...")
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

			// Filter for trains with available seats only
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
				msg := fmt.Sprintf("🎫 TIKETCOM [%s] %s→%s\n✅ %s tersedia! (%d found)\n\n",
					p.Date, p.Origin, p.Destination, p.TrainName, len(availableTrains))
				for _, t := range availableTrains {
					msg += fmt.Sprintf("• %s [%s]\n  💺 %s seats @ Rp%s\n", t.Name, t.Class, t.SeatsLeft, t.Price)
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
