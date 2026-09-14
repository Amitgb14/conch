// Package update finds newer conch builds — a rebuilt or downloaded binary,
// a GitHub release, machines running older builds — and applies them
// without stopping panes: binaries are replaced, servers reloaded.
package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// Executable is the running conch's path, symlinks resolved.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// Compare orders versions like "0.2.0" and "0.10.1-rc.1": -1, 0 or 1, as
// SemVer does. Pre-release suffixes sort before the release; build metadata
// ("+build.5") is ignored.
func Compare(a, b string) int {
	a, b = strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")
	a, _, _ = strings.Cut(a, "+")
	b, _, _ = strings.Cut(b, "+")
	ma, pa, _ := strings.Cut(a, "-")
	mb, pb, _ := strings.Cut(b, "-")
	na, nb := strings.Split(ma, "."), strings.Split(mb, ".")
	for i := 0; i < max(len(na), len(nb)); i++ {
		x, y := num(na, i), num(nb, i)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa == pb:
		return 0
	case pa == "":
		return 1
	case pb == "":
		return -1
	}
	return comparePre(strings.Split(pa, "."), strings.Split(pb, "."))
}

// comparePre orders dot-separated pre-release identifiers: numbers
// numerically and before words, words as text, and a shorter list first
// when one is a prefix of the other.
func comparePre(a, b []string) int {
	for i := 0; i < min(len(a), len(b)); i++ {
		x, xerr := strconv.Atoi(a[i])
		y, yerr := strconv.Atoi(b[i])
		switch {
		case xerr == nil && yerr == nil && x != y:
			return cmpInt(x, y)
		case xerr == nil && yerr != nil:
			return -1
		case xerr != nil && yerr == nil:
			return 1
		case xerr != nil && a[i] != b[i]:
			return strings.Compare(a[i], b[i])
		}
	}
	return cmpInt(len(a), len(b))
}

func cmpInt(x, y int) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

func num(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, _ := strconv.Atoi(parts[i])
	return n
}

// SameBuild reports whether a server runs the same conch as this process.
// Release builds compare versions (a downloaded binary for another
// platform has its own hash); development builds compare build IDs, which
// cross-built binaries inherit. ok is false when the server can't tell.
func SameBuild(s proto.HelloResult) (same, ok bool) {
	if proto.IsRelease() && s.Version != "" && !strings.Contains(s.Version, "dev") {
		return s.Version == proto.Version, true
	}
	if s.BuildID == "" {
		return false, false
	}
	return s.BuildID == buildinfo.ID(), true
}

// Release is a newer published release than this build, if any.
type Release struct {
	Version string
}

// NewerRelease checks GitHub for a release newer than this build. Development
// builds never ask: they update from source.
func NewerRelease(ctx context.Context) (*Release, error) {
	if !proto.IsRelease() {
		return nil, nil
	}
	latest, err := remote.LatestRelease(ctx)
	if err != nil {
		return nil, err
	}
	if Compare(latest, proto.Version) <= 0 {
		return nil, nil
	}
	return &Release{Version: latest}, nil
}

// InstallRelease downloads a release for this platform over the executable
// at exe.
func InstallRelease(ctx context.Context, version, exe string) error {
	bin, err := remote.FetchRelease(ctx, version, buildinfo.Platform())
	if err != nil {
		return err
	}
	if err := remote.ReplaceExecutable(exe, bin); err != nil {
		return fmt.Errorf("replace %s: %w", exe, err)
	}
	return nil
}

// Reload asks a server to reload into bin ("" for its own executable).
func Reload(ctx context.Context, c *client.Client, bin string) error {
	if len(c.MissingCapabilities([]string{"server.reload.v1"})) > 0 {
		return fmt.Errorf("the server there predates reloading; restart it once")
	}
	var res proto.ServerReloadResult
	return c.Call(ctx, proto.MethodServerReload, proto.ServerReloadParams{Binary: bin}, &res)
}

// Machine installs this build on a remote machine and reloads its server
// onto it, keeping its panes.
func Machine(ctx context.Context, c *client.Client, target string, say func(string)) error {
	platform := c.Server.Platform
	if platform == "" {
		return fmt.Errorf("the server there doesn't report its platform")
	}
	path, err := remote.Install(ctx, target, platform, false, say)
	if err != nil {
		return err
	}
	say("reloading the server")
	return Reload(ctx, c, path)
}
