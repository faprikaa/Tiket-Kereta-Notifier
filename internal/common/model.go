package common

import (
	"context"
	"sync"
	"time"
)

// Train represents a standardized train data structure
type Train struct {
	Name          string
	Class         string
	Price         string
	DepartureTime string
	ArrivalTime   string
	Availability  string // "AVAILABLE" or "FULL"
	SeatsLeft     string // e.g. "50", "0"
}

// CheckResult represents a single check result for history
type CheckResult struct {
	Timestamp       time.Time
	TrainsFound     int
	AvailableTrains []Train
	Error           string
}

// ProviderStatus contains status information for a provider
type ProviderStatus struct {
	StartTime        time.Time
	TotalChecks      int
	SuccessfulChecks int
	FailedChecks     int
	LastCheckTime    time.Time
	LastCheckFound   bool
	LastCheckError   string
	Origin           string
	Destination      string
	Date             string
	TrainName        string
	Interval         time.Duration
}

// StatusTracker provides thread-safe status tracking for providers (DRY)
type StatusTracker struct {
	mu               sync.RWMutex
	StartTime        time.Time
	TotalChecks      int
	SuccessfulChecks int
	FailedChecks     int
	LastCheckTime    time.Time
	LastCheckFound   bool
	LastCheckError   string
}

// NewStatusTracker creates a new status tracker
func NewStatusTracker() *StatusTracker {
	return &StatusTracker{
		StartTime: time.Now(),
	}
}

// RecordCheckStart records the start of a check
func (s *StatusTracker) RecordCheckStart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TotalChecks++
	s.LastCheckTime = time.Now()
}

// RecordCheckSuccess records a successful check
func (s *StatusTracker) RecordCheckSuccess(found bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SuccessfulChecks++
	s.LastCheckFound = found
	s.LastCheckError = ""
}

// RecordCheckError records a failed check
func (s *StatusTracker) RecordCheckError(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FailedChecks++
	s.LastCheckFound = false
	s.LastCheckError = err
}

// GetStats returns the current stats (thread-safe)
func (s *StatusTracker) GetStats() (startTime time.Time, total, success, failed int, lastTime time.Time, lastFound bool, lastErr string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.StartTime, s.TotalChecks, s.SuccessfulChecks, s.FailedChecks, s.LastCheckTime, s.LastCheckFound, s.LastCheckError
}

// Provider defines the standard interface for train search providers
type Provider interface {
	Search(ctx context.Context) ([]Train, error)
	SearchAll(ctx context.Context) ([]Train, error)
	Name() string
	StartScheduler(ctx context.Context, notifyFunc func(message string))
	GetHistory(n int) []CheckResult
	GetStatus() ProviderStatus
}
