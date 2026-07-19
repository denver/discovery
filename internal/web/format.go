package web

import (
	"strconv"
	"strings"
	"time"
)

// compactCount formats a counter the way leaderboards read them: exact
// under 1000, then one decimal with a K/M/B suffix ("54.3K", "1.8B"),
// trailing ".0" trimmed ("2K", not "2.0K").
func compactCount(n int64) string {
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	units := []struct {
		div    float64
		suffix string
	}{
		{1e9, "B"},
		{1e6, "M"},
		{1e3, "K"},
	}
	for _, u := range units {
		if float64(n) >= u.div {
			s := strconv.FormatFloat(float64(n)/u.div, 'f', 1, 64)
			return strings.TrimSuffix(s, ".0") + u.suffix
		}
	}
	return strconv.FormatInt(n, 10) // unreachable
}

// relTime renders t relative to now ("12 minutes ago"). Times in the
// future or under a minute ago read "just now".
func relTime(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute")
	case d < 24*time.Hour:
		return plural(int(d/time.Hour), "hour")
	case d < 30*24*time.Hour:
		return plural(int(d/(24*time.Hour)), "day")
	case d < 365*24*time.Hour:
		return plural(int(d/(30*24*time.Hour)), "month")
	default:
		return plural(int(d/(365*24*time.Hour)), "year")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit + " ago"
	}
	return strconv.Itoa(n) + " " + unit + "s ago"
}
