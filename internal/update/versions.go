package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/remote"
)

// keptLimit is how many replaced binaries are kept for moving back; older
// ones are deleted as new ones arrive.
const keptLimit = 3

// versionsDir holds the binaries of versions conch has run and the note of
// which one to go back to.
func versionsDir() string { return filepath.Join(config.Dir(), "versions") }

// keptName is the file a version's binary is kept under. Development builds
// of one version share a name, so a newer one replaces the older copy.
func keptName(version string) string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	v = strings.Map(func(r rune) rune {
		if r == '.' || r == '-' || r == '+' || r == '_' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return r
		}
		return '_'
	}, v)
	if v == "" {
		return ""
	}
	return filepath.Join(versionsDir(), "conch-"+v)
}

// Kept is the binary conch kept of version when it moved off it, so going
// back needs no download.
func Kept(version string) (string, bool) {
	path := keptName(version)
	if path == "" {
		return "", false
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() == 0 {
		return "", false
	}
	return path, true
}

// keep copies the executable at exe under its version, so a later move back
// to it needs no download — and works for a version that was never
// published, like a local build. The copy is a convenience, so a failure to
// make it is silent: that version is downloaded again instead.
func keep(exe, version string) {
	path := keptName(version)
	if path == "" || exe == "" {
		return
	}
	bin, err := os.ReadFile(exe)
	if err != nil || len(bin) == 0 {
		return
	}
	if err := os.MkdirAll(versionsDir(), 0o700); err != nil {
		return
	}
	tmp := path + ".new"
	if os.WriteFile(tmp, bin, 0o755) != nil {
		os.Remove(tmp)
		return
	}
	if os.Rename(tmp, path) != nil {
		os.Remove(tmp)
		return
	}
	prune()
}

// prune deletes all but the newest keptLimit kept binaries.
func prune() {
	ents, err := os.ReadDir(versionsDir())
	if err != nil {
		return
	}
	type kept struct {
		name string
		mod  time.Time
	}
	var files []kept
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "conch-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, kept{e.Name(), info.ModTime()})
	}
	slices.SortStableFunc(files, func(a, b kept) int { return b.mod.Compare(a.mod) }) // newest first
	for _, f := range files[min(keptLimit, len(files)):] {
		os.Remove(filepath.Join(versionsDir(), f.name))
	}
}

// previousFile notes the version to go back to.
func previousFile() string { return filepath.Join(versionsDir(), "previous") }

// Previous is the version conch ran before the last update or move back,
// "" when none was recorded (a fresh install, or the note was removed).
// Moving back notes the version left behind in turn, so back and forward
// are the same step.
func Previous() string {
	b, err := os.ReadFile(previousFile())
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(string(b)), "v")
}

// setPrevious records the version conch is moving off. Silent on failure:
// without the note, moving back needs the version named.
func setPrevious(version string) {
	if version == "" {
		return
	}
	if os.MkdirAll(versionsDir(), 0o700) != nil {
		return
	}
	os.WriteFile(previousFile(), []byte(version+"\n"), 0o600)
}

// isReleaseVersion reports whether a version could have been published:
// development builds carry "dev" and never were.
func isReleaseVersion(v string) bool {
	return v != "" && !strings.Contains(v, "dev")
}

// binary is the conch binary of version for this platform: the copy kept
// when conch moved off it, else the published release.
func binary(ctx context.Context, version string) ([]byte, error) {
	if path, ok := Kept(version); ok {
		if bin, err := os.ReadFile(path); err == nil && len(bin) > 0 {
			return bin, nil
		}
	}
	if !isReleaseVersion(version) {
		return nil, fmt.Errorf("conch %s is not a published release and no copy of it was kept", version)
	}
	return remote.FetchRelease(ctx, version, buildinfo.Platform())
}

// Releases lists the versions published upstream — what the install script
// would fetch — newest first.
func Releases(ctx context.Context) ([]string, error) {
	vs, err := remote.Releases(ctx)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(vs)
	slices.SortStableFunc(out, func(a, b string) int { return Compare(b, a) }) // newest first
	return out, nil
}
