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

// Provider defines the standard interface for train search providers
type Provider interface {
	// Search checks for train availability (may filter by configured train name)
	Search(ctx context.Context) ([]Train, error)

	// SearchAll returns all trains without filtering
	SearchAll(ctx context.Context) ([]Train, error)

	// Name returns the provider name
	Name() string

	// StartScheduler starts the provider's internal monitoring loop
	StartScheduler(ctx context.Context, notifyFunc func(message string))

	// GetHistory returns the last N check results
	GetHistory(n int) []CheckResult
}
