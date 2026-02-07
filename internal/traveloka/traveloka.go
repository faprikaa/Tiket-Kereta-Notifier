// Package traveloka provides train search functionality using Traveloka API
package traveloka

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tiket-kereta-notifier/internal/common"
	"tiket-kereta-notifier/internal/history"
)

// APIUrl is the Traveloka train search API endpoint
const APIUrl = "https://www.traveloka.com/api/v2/train/search/inventoryv2"

// Provider implements common.Provider for Traveloka
type Provider struct {
	Origin        string
	Destination   string
	Day           int
	Month         int
	Year          int
	Logger        *slog.Logger
	TrainName     string        // Optional: specific train to monitor
	CheckInterval time.Duration // Polling interval
	ProxyURL      string        // Optional SOCKS5 proxy
	history       *history.Store
	status        *common.StatusTracker
}

// NewProvider creates a new Traveloka provider
func NewProvider(logger *slog.Logger, origin, dest string, day, month, year int, trainName string, interval time.Duration, proxyURL string) *Provider {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Provider{
		Origin:        origin,
		Destination:   dest,
		Day:           day,
		Month:         month,
		Year:          year,
		TrainName:     trainName,
		CheckInterval: interval,
		ProxyURL:      proxyURL,
		Logger:        logger,
		history:       history.NewStore(100),
		status:        common.NewStatusTracker(),
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "Traveloka"
}

// StartScheduler starts the polling loop
func (p *Provider) StartScheduler(ctx context.Context, notifyFunc func(message string)) {
	ticker := time.NewTicker(p.CheckInterval)
	defer ticker.Stop()

	p.Logger.Info("Traveloka scheduler started", "interval", p.CheckInterval, "target", p.TrainName)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.status.RecordCheckStart()

			p.Logger.Debug("Scheduler checking Traveloka...")
			trains, err := p.Search(ctx)
			if err != nil {
				p.status.RecordCheckError(err.Error())
				p.history.Add(common.CheckResult{
					Timestamp: time.Now(),
					Error:     err.Error(),
				})
				continue
			}

			// Filter for AVAILABLE trains only for notification
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
				msg := fmt.Sprintf("Target Train Available! (%d found)\n", len(availableTrains))
				for _, t := range availableTrains {
					msg += fmt.Sprintf("- %s: %s seats (%s)\n", t.Name, t.SeatsLeft, t.Price)
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
		Date:             fmt.Sprintf("%d-%02d-%02d", p.Year, p.Month, p.Day),
		TrainName:        p.TrainName,
		Interval:         p.CheckInterval,
	}
}

// Search performs a train search using Traveloka API
func (p *Provider) Search(ctx context.Context) (trains []common.Train, err error) {
	defer func() {
		if err != nil {
			p.Logger.Error("Search error", "error", err)
		}
	}()

	client := p.createHTTPClient()

	payload := fmt.Sprintf(`{"fields":[],"data":{"departureDate":{"day":%d,"month":%d,"year":%d},"returnDate":null,"destination":"%s","origin":"%s","numOfAdult":1,"numOfInfant":0,"providerType":"KAI","currency":"IDR","trackingMap":{"utmId":null,"utmEntryTimeMillis":0}},"clientInterface":"desktop"}`,
		p.Day, p.Month, p.Year, p.Destination, p.Origin)

	data := strings.NewReader(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", APIUrl, data)
	if err != nil {
		p.Logger.Error("Failed to create request", "error", err)
		return nil, err
	}

	req.Header.Add("accept", "*/*")
	req.Header.Add("accept-language", "en-US,en;q=0.9")
	req.Header.Add("content-type", "application/json")
	req.Header.Add("origin", "https://www.traveloka.com")
	req.Header.Add("priority", "u=1, i")
	req.Header.Add("sec-ch-ua", "\"Not(A:Brand\";v=\"8\", \"Chromium\";v=\"144\", \"Microsoft Edge\";v=\"144\"")
	req.Header.Add("sec-ch-ua-mobile", "?0")
	req.Header.Add("sec-ch-ua-platform", "\"Windows\"")
	req.Header.Add("sec-fetch-dest", "empty")
	req.Header.Add("sec-fetch-mode", "cors")
	req.Header.Add("sec-fetch-site", "same-origin")
	req.Header.Add("t-a-v", "262360")
	req.Header.Add("tv-clientsessionid", "T1-web.01KGE6X3HP3X6MVCNR2AMJEF6N")
	req.Header.Add("tv-country", "ID")
	req.Header.Add("tv-currency", "IDR")
	req.Header.Add("tv-language", "en_ID")
	req.Header.Add("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36 Edg/144.0.0.0")
	req.Header.Add("www-app-version", "release_webgtr_desktop_20251222-2dc8e9ba35")
	req.Header.Add("x-client-interface", "desktop")
	req.Header.Add("x-did", "MDFLR0U2WDUxN0IyVEFaMTEzS1ZXNjE0M0U=")
	req.Header.Add("x-domain", "train")
	req.Header.Add("x-route-prefix", "en-id")
	req.Header.Add("Cookie", "tvl=/EIrVOerOa3DtJX7TcoqVVwPlJH9eW394k6fpaWLslcRunZ6tdOB0t8G9Mc9M1KOayJocKqhwWyUybA+siomf6Z1NIRkl4+zP9m6QLveVWNSBmE6j7N/f5Ak4a0XPxvp5PPK4CfwSL4WnqZ1SZwpIMrI9rbU3DW4937voR2AAOcDDXyp2/aJ3Q79M0+frVADhokUVa9kILrNKLkScFJy7VIttFPPtON02+yXg5HdMc5J65CukxBe9VbpglxHeDegHRPmjv9YxYhy2+PoUiJXdqontxt+emW4gz5FzyM/R1zafJn/ZFOiPxS94eecEXjf9n4iQiGRhpUisToKdn4FLFURPlNBkscyIExnIU35ktdTpzHmYVbwE06WNijs6/J8ZbwtkShBaXTYXwtgjc0zvlObFjXolfzyOjDbeYbO15zeJGT7NghchNsIQD1JL0gzbIZOz6PrmBQ4EVS4lKAqmYw1z/6N7h2/MntnNCQsSYwuuhUBoT29+UZP/+sTLAu0ot7l9fGx+2q29idcK4AUqYeCt2LPYnS+drO4p0qmyrkeXdLywrmm4xzyf8xYzPvifhU=~djAy; tvo=L2FwaS92Mi90cmFpbi9zZWFyY2gvaW52ZW50b3J5djI=; tvs=J8BxatqpFpVo6+xwWoQP8nMW1TKfNx8Y4EVxwZ6Svdrac+vGcw9dHsFj4lHJJZ0BsImyaom969VcPcRgIVAvDkHyFRC7llLBL0UVuX3wq5ANJ34+E2o20rQoKYTRJlsRUSbiEWViA/uwFBF8hL187HCJfeg4bXr9R+LhK1fbhMIolE8y1gsO/d/ugwZmj93fn8N5QVHooriCrYe9oPgudN1w1LBtX5LpNH17VQyvhUVA8KnRmyX4hjkcYuZebPk+eCDfATTrXEK682ho6uZrJUh2TKkUtU5JoL+Bhpg7Q3zWqQJJmG1NcOugyQ6BTCayl3flbVa4t/EXMM85wB7LEcZ6EKziLNO5/X2E4GHLkQcZ9pazc9RrVDtxvpbdB9HXbggGw0VEsrcTAoucgufy8X1BMesK+y6EKPECThRu3mKwL8EheU96pjk4W8sVPEFpljXj1S+ajnhHxxbxHFC3gnfe80/DCtlkEh+I7msTTaNE/z2ZbqZRzeqnGAkT05JgrkQnM52dylVRTCe+BSWUobx+ScewH5Wkjm2yyicYp+B+/M0vSU6gwXSNjr+AavguyYOYYZY6wQ==~djAy")

	resp, err := client.Do(req)
	if err != nil {
		p.Logger.Error("Failed to send request", "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	bodyText, err := io.ReadAll(resp.Body)
	if err != nil {
		p.Logger.Error("Failed to read response", "error", err)
		return nil, err
	}

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		p.Logger.Error("API returned error", "status", resp.StatusCode, "body", string(bodyBytes))
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(bodyText, &result); err != nil {
		p.Logger.Error("Failed to parse response", "error", err)
		return nil, err
	}

	// Extract trains from response
	allTrains := parseTrains(result)

	// Filter if TrainName is specified
	if p.TrainName != "" {
		var filtered []common.Train
		target := strings.ToLower(p.TrainName)
		for _, t := range allTrains {
			if strings.Contains(strings.ToLower(t.Name), target) {
				filtered = append(filtered, t)
			}
		}
		// Return filtered list
		trains = filtered
	} else {
		// Return all trains
		trains = allTrains
	}

	return trains, nil
}

// SearchAll returns all trains without filtering by TrainName
func (p *Provider) SearchAll(ctx context.Context) ([]common.Train, error) {
	// Temporarily clear TrainName filter
	originalName := p.TrainName
	p.TrainName = ""
	defer func() { p.TrainName = originalName }()

	return p.Search(ctx)
}

// parseTrains extracts train information from API response
func parseTrains(data map[string]interface{}) []common.Train {
	var trains []common.Train

	dataObj, ok := data["data"].(map[string]interface{})
	if !ok {
		return trains
	}

	inventories, ok := dataObj["departTrainInventories"].([]interface{})
	if !ok {
		return trains
	}

	for _, inv := range inventories {
		inventory, ok := inv.(map[string]interface{})
		if !ok {
			continue
		}

		train := common.Train{
			Name:         getString(inventory, "trainBrandLabel"),
			Class:        getString(inventory, "ticketLabel"),
			Availability: getString(inventory, "availability"),
			SeatsLeft:    getString(inventory, "numSeatsAvailable"),
		}

		// Parse fare
		if fare, ok := inventory["fare"].(map[string]interface{}); ok {
			if cv, ok := fare["currencyValue"].(map[string]interface{}); ok {
				train.Price = fmt.Sprintf("Rp %s", getString(cv, "amount"))
			}
		}

		// Parse departure time
		if dt, ok := inventory["departureTime"].(map[string]interface{}); ok {
			if hm, ok := dt["hourMinute"].(map[string]interface{}); ok {
				train.DepartureTime = fmt.Sprintf("%s:%s", getString(hm, "hour"), padZero(getString(hm, "minute")))
			}
		}

		// Parse arrival time
		if at, ok := inventory["arrivalTime"].(map[string]interface{}); ok {
			if hm, ok := at["hourMinute"].(map[string]interface{}); ok {
				train.ArrivalTime = fmt.Sprintf("%s:%s", getString(hm, "hour"), padZero(getString(hm, "minute")))
			}
		}

		trains = append(trains, train)
	}

	return trains
}

// --- Legacy Helper used by Bot (Keep for now or Refactor Bot to use Provider) ---
// SearchParams helper struct
type SearchParams struct {
	Origin, Destination string
	Day, Month, Year    int
}

// SearchLegacy is a wrapper to maintain compatibility if needed,
// OR we update bot.go to use NewProvider(...)
// Let's update helper functions for internal use:

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func padZero(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

// Helper to filter results (moved from SearchResult method to standalone or Provider method)
func FindTrain(trains []common.Train, name string) *common.Train {
	nameLower := strings.ToLower(name)
	for i := range trains {
		if strings.Contains(strings.ToLower(trains[i].Name), nameLower) {
			return &trains[i]
		}
	}
	return nil
}

// createHTTPClient creates an HTTP client with optional proxy support
func (p *Provider) createHTTPClient() *http.Client {
	transport := &http.Transport{}

	if p.ProxyURL != "" {
		proxyURL, err := url.Parse(p.ProxyURL)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
			p.Logger.Debug("Using proxy", "url", p.ProxyURL)
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}
}
