package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

// appTool runs codesign or spctl and returns what it said; a variable so
// tests answer for them, never touching a real signature.
var appTool = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// VerifyApp checks a downloaded Conch.app before it replaces the running
// one: its signature intact (deep and strict), made by the same team as
// the app running now, and accepted by Gatekeeper as notarized. A running
// app signed ad hoc — built by hand — has no team to match, so it is
// refused too: nothing would say the download came from the same hands.
func VerifyApp(ctx context.Context, next, running string) error {
	if out, err := appTool(ctx, "codesign", "--verify", "--deep", "--strict", next); err != nil {
		return fmt.Errorf("the downloaded Conch.app's signature doesn't hold: %s", firstLine(out, err))
	}
	have := teamID(ctx, running)
	if have == "" {
		return errors.New("this Conch.app isn't signed by a developer (a build of your own?), so a download can't be checked against it: download the new release and replace it by hand")
	}
	got := teamID(ctx, next)
	if got == "" {
		return errors.New("the downloaded Conch.app is not signed by a developer")
	}
	if got != have {
		return fmt.Errorf("the downloaded Conch.app is signed by team %s, not %s as this one is", got, have)
	}
	out, err := appTool(ctx, "spctl", "--assess", "--type", "execute", "--verbose=2", next)
	if err != nil {
		return fmt.Errorf("Gatekeeper refuses the downloaded Conch.app: %s", firstLine(out, err))
	}
	if !strings.Contains(string(out), "source=Notarized Developer ID") {
		return errors.New("the downloaded Conch.app is not notarized by Apple")
	}
	return nil
}

// teamID is the team an app is signed by, "" for none (ad hoc, unsigned).
func teamID(ctx context.Context, app string) string {
	out, _ := appTool(ctx, "codesign", "-dv", "--verbose=2", app)
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "TeamIdentifier="); ok && v != "not set" {
			return v
		}
	}
	return ""
}

func firstLine(out []byte, err error) string {
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return err.Error()
}
