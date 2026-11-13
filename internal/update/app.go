package update

import (
	"context"
	"runtime"
	"strings"

	"github.com/Amitgb14/conch/internal/remote"
)

// AppCheck is what Conch.app's check for a newer release of itself found
// (`conch app update check -json`, which the app reads).
type AppCheck struct {
	// Current is the app's own version, as it was given.
	Current string `json:"current"`
	// Dev is a build from source, which never asks: it updates from source.
	Dev bool `json:"dev,omitempty"`
	// Latest is the newest published release, when one was asked for.
	Latest string `json:"latest,omitempty"`
	// Newer is Latest being newer than Current.
	Newer bool `json:"newer,omitempty"`
	// Notes is the page the release is described on.
	Notes string `json:"notes,omitempty"`
	// Asset is the archive this Mac would download, or "" with Unavailable
	// saying why there is none (an Intel Mac).
	Asset       string `json:"asset,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

// CheckApp asks for the latest release, as conch's own check does
// (remote.LatestRelease), and compares it with the app's version.
func CheckApp(ctx context.Context, current string) (AppCheck, error) {
	current = strings.TrimPrefix(current, "v")
	c := AppCheck{Current: current}
	if !isAppRelease(current) {
		c.Dev = true
		return c, nil
	}
	latest, err := remote.LatestRelease(ctx)
	if err != nil {
		return c, err
	}
	c.Latest = latest
	c.Newer = Compare(latest, current) > 0
	if c.Newer {
		c.Notes = remote.ReleaseNotes(latest)
		if a, err := remote.AppAsset(latest, runtime.GOARCH); err != nil {
			c.Unavailable = err.Error()
		} else {
			c.Asset = a
		}
	}
	return c, nil
}

// isAppRelease is a version a release could have been published as.
func isAppRelease(v string) bool { return v != "" && !strings.Contains(v, "dev") }
