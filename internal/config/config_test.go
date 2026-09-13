package config

import (
	"strings"
	"testing"
	"time"
)

func TestMergeEnv(t *testing.T) {
	got := MergeEnv([]string{"PATH=/bin", "CONCH_SOCKET=/outer.sock", "HOME=/h", "CONCH_SOCKET=/dup.sock"},
		"CONCH_SOCKET=/inner.sock", "TERM=xterm")
	if want := "PATH=/bin,HOME=/h,CONCH_SOCKET=/inner.sock,TERM=xterm"; strings.Join(got, ",") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}

func TestSaveLoadAndOldFiles(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	cfg := Default()
	cfg.UI.Theme, cfg.Notify.Sound, cfg.Notify.Done, cfg.Shell.OMZTheme = "nord", true, false, "agnoster"
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil || got.UI.Theme != "nord" || !got.Notify.Sound || got.Notify.Done || got.Shell.OMZTheme != "agnoster" || got.Keys.Prefix != "ctrl+b" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}

func TestQuietHours(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 9, 13, h, m, 0, 0, time.Local) }
	night := NotifyCfg{QuietStart: "22:00", QuietEnd: "08:00"}
	day := NotifyCfg{QuietStart: "09:00", QuietEnd: "18:30"}
	for _, tc := range []struct {
		cfg  NotifyCfg
		t    time.Time
		want bool
	}{
		{night, at(23, 0), true}, {night, at(3, 0), true}, {night, at(8, 0), false}, {night, at(21, 59), false},
		{day, at(9, 0), true}, {day, at(18, 29), true}, {day, at(18, 30), false},
		{NotifyCfg{}, at(3, 0), false}, {NotifyCfg{QuietStart: "25:00", QuietEnd: "08:00"}, at(3, 0), false},
	} {
		if got := tc.cfg.Quiet(tc.t); got != tc.want {
			t.Errorf("%s-%s at %s: %v", tc.cfg.QuietStart, tc.cfg.QuietEnd, tc.t.Format("15:04"), got)
		}
	}
}
