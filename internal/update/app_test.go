package update

import (
	"context"
	"strings"
	"testing"
)

// Conch.app's check: the latest release against the app's own version,
// pre-releases sorting before their release, and a development build
// never asking at all.
func TestCheckApp(t *testing.T) {
	a3Isolate(t)
	rs := a3NewReleaseServer(t)
	ctx := context.Background()
	for _, c := range []struct {
		current, latest string
		newer           bool
	}{
		{"0.2.0", "0.2.1", true},
		{"v0.2.0", "0.10.0", true},
		{"0.2.1", "0.2.1", false},
		{"0.2.2", "0.2.1", false},
		{"0.2.1-rc.1", "0.2.1", true},
		{"0.2.1-rc.1", "0.2.1-rc.2", true},
		{"0.2.1", "0.2.1-rc.2", false},
	} {
		rs.mu.Lock()
		rs.latest = c.latest
		rs.mu.Unlock()
		got, err := CheckApp(ctx, c.current)
		if err != nil {
			t.Fatalf("%s → %s: %v", c.current, c.latest, err)
		}
		if got.Newer != c.newer || got.Latest != c.latest || got.Dev || got.Current != strings.TrimPrefix(c.current, "v") {
			t.Errorf("%s → %s: %+v", c.current, c.latest, got)
		}
		if c.newer && !strings.HasSuffix(got.Notes, "/tag/v"+c.latest) {
			t.Errorf("notes: %q", got.Notes)
		}
		if c.newer && got.Asset != "" && got.Asset != "Conch_"+c.latest+"_macos_arm64.zip" {
			t.Errorf("asset: %q", got.Asset)
		}
	}
}

func TestCheckAppDevBuildNeverAsks(t *testing.T) {
	a3Isolate(t)
	rs := a3NewReleaseServer(t)
	rs.latest = "99.0.0"
	for _, v := range []string{"0.2.1-dev", "dev", "", "0.3.0-dev+abc"} {
		got, err := CheckApp(context.Background(), v)
		if err != nil || !got.Dev || got.Newer || got.Latest != "" {
			t.Errorf("%q: %+v %v", v, got, err)
		}
	}
	if len(rs.requests) != 0 {
		t.Fatalf("a development build asked: %v", rs.requests)
	}
}

func TestCheckAppErrors(t *testing.T) {
	a3Isolate(t)
	a3NewReleaseServer(t) // nothing published
	if _, err := CheckApp(context.Background(), "0.2.0"); err == nil || !strings.Contains(err.Error(), "no published release") {
		t.Fatalf("no release: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CheckApp(ctx, "0.2.0"); err == nil {
		t.Fatal("cancelled: no error")
	}
}
