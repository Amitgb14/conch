package when

import (
	"testing"
	"time"
)

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 10, 14, 13, 0, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"14:05":              time.Date(2026, 10, 14, 14, 5, 0, 0, time.Local),
		"09:00":              time.Date(2026, 10, 15, 9, 0, 0, 0, time.Local),  // passed: tomorrow
		"13:00":              time.Date(2026, 10, 15, 13, 0, 0, 0, time.Local), // now is not later
		"+30m":               now.Add(30 * time.Minute),
		"+1d":                now.Add(24 * time.Hour),
		"+1h30m":             now.Add(90 * time.Minute),
		"2026-10-15 09:00":   time.Date(2026, 10, 15, 9, 0, 0, 0, time.Local),
		" 2026-10-15T09:00 ": time.Date(2026, 10, 15, 9, 0, 0, 0, time.Local),
	} {
		got, err := Parse(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("%q: %v %v, want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "soon", "+", "+-5m", "+0m", "25:00", "2026-10-13 09:00", "2026-13-01 09:00"} {
		if _, err := Parse(bad, now); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
}

func TestParseEvery(t *testing.T) {
	for in, want := range map[string]int{"daily": 1440, "hourly": 60, "weekly": 10080, "1d": 1440, "2w": 20160, "90m": 90, "5m": 5, "1h30m": 90} {
		if got, err := Every(in); err != nil || got != want {
			t.Errorf("%q: %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "4m", "-1d", "often", "1x", "d"} {
		if _, err := Every(bad); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
}
