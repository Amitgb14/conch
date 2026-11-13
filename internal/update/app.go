package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

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

// volumesDir is where macOS mounts a disk image; a variable for tests.
var volumesDir = "/Volumes"

// AppReplaceable says why the Conch.app at app cannot be replaced by an
// update, or nil when it can: run translocated (macOS's random read-only
// copy of an app opened where it was downloaded), from its disk image, or
// from somewhere this user cannot write — an install made by an admin.
// The update renames the bundle within its folder, so both have to be
// writable.
func AppReplaceable(app string) error {
	if app == "" || !strings.HasSuffix(filepath.Clean(app), ".app") {
		return fmt.Errorf("%q is not an application bundle (.app)", app)
	}
	if strings.Contains(app, "/AppTranslocation/") {
		return errors.New("macOS is running Conch from a temporary copy (App Translocation): move Conch.app to your Applications folder, open it from there, and update again")
	}
	if fi, err := os.Stat(filepath.Join(app, "Contents")); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not an application bundle", app)
	}
	dir := filepath.Dir(app)
	if !folderWritable(dir) {
		if strings.HasPrefix(app, volumesDir+"/") {
			return errors.New("Conch is running from its disk image, which can't be written to: drag it to your Applications folder, open it from there, and update again")
		}
		return fmt.Errorf("can't write to %s, where Conch.app is installed (an admin installed it?): download the new release and replace it by hand", dir)
	}
	for _, p := range []string{app, filepath.Join(app, "Contents")} {
		if syscall.Access(p, 2 /* W_OK */) != nil {
			return fmt.Errorf("can't write to %s (an admin installed it?): download the new release and replace it by hand", app)
		}
	}
	return nil
}

// folderWritable tries rather than reads permissions: a read-only volume
// or an ACL says no only when something is written.
func folderWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".conch-update-probe-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}
