package common

import (
	"strconv"
	"strings"
)

// IsWildcard reports whether name is a wildcard train name ("any" or "*"),
// meaning no name filter should be applied.
func IsWildcard(name string) bool {
	n := strings.TrimSpace(strings.ToLower(name))
	return n == "any" || n == "*"
}

// ParsePrice parses a price string (e.g. "Rp 350000", "350000", "Rp350.000")
// into an integer in Rupiah. Returns 0 if the value cannot be parsed.
func ParsePrice(s string) int {
	// Strip common prefixes and whitespace
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "Rp ")
	s = strings.TrimPrefix(s, "Rp")
	// Remove dots and commas (thousand separators)
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, ",", "")
	// Remove any decimal part (e.g. ".00")
	if idx := strings.Index(s, "."); idx != -1 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return v
}
