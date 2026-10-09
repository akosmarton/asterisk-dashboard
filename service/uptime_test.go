package service

import (
	"testing"
)

func TestFormatShortUptime(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"2 hours, 39 minutes, 34 seconds", "2h 39m 34s"},
		{"1 hour, 5 minutes, 2 seconds", "1h 5m 2s"},
		{"45 seconds", "45s"},
		{"10 minutes, 15 seconds", "10m 15s"},
		{"3 days, 4 hours, 12 minutes, 5 seconds", "3d 4h 12m"},
		{"2 weeks, 3 days, 1 hour", "2w 3d 1h"},
		{"1 year, 2 weeks, 3 days", "1y 2w 3d"},
		{"", "N/A"},
		{"N/A", "N/A"},
	}

	for _, tc := range tests {
		got := FormatShortUptime(tc.input)
		if got != tc.expected {
			t.Errorf("FormatShortUptime(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}
