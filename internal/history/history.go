// Package history provides history storage for train check results
package history

import (
	"sync"
	"time"

	"tiket-kereta-notifier/internal/common"
)

// Store provides thread-safe history storage
type Store struct {
	mu      sync.RWMutex
	results []common.CheckResult
	maxSize int
}

// NewStore creates a new history store with specified max size
func NewStore(maxSize int) *Store {
	if maxSize <= 0 {
		maxSize = 100 // Default max history size
	}
	return &Store{
		results: make([]common.CheckResult, 0, maxSize),
		maxSize: maxSize,
	}
}

// Add adds a new check result to history
func (s *Store) Add(result common.CheckResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Add timestamp if not set
	if result.Timestamp.IsZero() {
		result.Timestamp = time.Now()
	}

	// Prepend to keep newest first
	s.results = append([]common.CheckResult{result}, s.results...)

	// Trim if exceeds max size
	if len(s.results) > s.maxSize {
		s.results = s.results[:s.maxSize]
	}
}

// GetLast returns the last N check results (newest first)
func (s *Store) GetLast(n int) []common.CheckResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if n <= 0 || n > len(s.results) {
		n = len(s.results)
	}

	// Return a copy to prevent external modification
	result := make([]common.CheckResult, n)
	copy(result, s.results[:n])
	return result
}

// Count returns the total number of stored results
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.results)
}
