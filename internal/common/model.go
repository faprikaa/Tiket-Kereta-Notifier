package common

import (
	"context"
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

// Provider defines the standard interface for train search providers
type Provider interface {
	Search(ctx context.Context) ([]Train, error)
	SearchAll(ctx context.Context) ([]Train, error)
	Name() string
	StartScheduler(ctx context.Context, notifyFunc func(message string))
	GetHistory(n int) []CheckResult
	GetStatus() ProviderStatus
}
