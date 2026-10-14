// Package when reads the times people give conch for later — 14:05,
// +30m, 2026-10-15 09:00 — and how often to repeat — daily, 6h — the same
// way in the command line and the TUI.
package when

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Parse reads a time to come: 14:05 (today, or tomorrow once it has
// passed), +30m or +2h from now, or 2026-10-15 09:00. A time already gone
// is refused rather than run at once by surprise.
func Parse(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "+") {
		d, err := Span(s[1:])
		if err != nil || d <= 0 {
			return time.Time{}, fmt.Errorf("%q: give a span such as +30m, +2h or +1d", s)
		}
		return now.Add(d), nil
	}
	if t, err := time.ParseInLocation("15:04", s, now.Location()); err == nil {
		at := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
		if !at.After(now) {
			at = at.AddDate(0, 0, 1)
		}
		return at, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			if !t.After(now) {
				return time.Time{}, fmt.Errorf("%s has passed", s)
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q: give a time such as 14:05, +30m or 2026-10-15 09:00", s)
}

// Span reads 30m, 2h, 1d or 1w; Go's own durations (1h30m) too.
func Span(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if n := len(s); n > 1 && (s[n-1] == 'd' || s[n-1] == 'w') {
		v, err := strconv.Atoi(s[:n-1])
		if err != nil {
			return 0, err
		}
		day := 24 * time.Hour
		if s[n-1] == 'w' {
			day *= 7
		}
		return time.Duration(v) * day, nil
	}
	return time.ParseDuration(s)
}

// Every reads how often a task repeats, in minutes.
func Every(s string) (int, error) {
	switch strings.TrimSpace(s) {
	case "hourly":
		return 60, nil
	case "daily":
		return 24 * 60, nil
	case "weekly":
		return 7 * 24 * 60, nil
	}
	d, err := Span(s)
	if err != nil || d < 5*time.Minute {
		return 0, fmt.Errorf("%q: give a span of 5m or more, such as 1h, 1d or weekly", s)
	}
	return int(d / time.Minute), nil
}
