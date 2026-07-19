package web

import (
	"testing"
	"time"
)

func TestCompactCount(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{7, "7"},
		{999, "999"},
		{1000, "1K"},
		{1500, "1.5K"},
		{54321, "54.3K"},
		{999000, "999K"},
		{2000000, "2M"},
		{2500000, "2.5M"},
		{1800000000, "1.8B"},
		{2000000000, "2B"},
	}
	for _, tt := range tests {
		if got := compactCount(tt.n); got != tt.want {
			t.Errorf("compactCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestRelTime(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{time.Minute, "1 minute ago"},
		{12 * time.Minute, "12 minutes ago"},
		{time.Hour, "1 hour ago"},
		{3 * time.Hour, "3 hours ago"},
		{26 * time.Hour, "1 day ago"},
		{5 * 24 * time.Hour, "5 days ago"},
		{40 * 24 * time.Hour, "1 month ago"},
		{300 * 24 * time.Hour, "10 months ago"},
		{800 * 24 * time.Hour, "2 years ago"},
	}
	for _, tt := range tests {
		if got := relTime(now.Add(-tt.ago), now); got != tt.want {
			t.Errorf("relTime(now-%v) = %q, want %q", tt.ago, got, tt.want)
		}
	}
}
