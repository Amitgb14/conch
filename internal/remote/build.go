package remote

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

const modulePath = "github.com/Amitgb14/conch"

// buildMu serialises cross-builds: several machines of the same platform
// may connect at once.
var buildMu sync.Mutex

// SourceDir finds conch's source tree: $CONCH_SOURCE, or a directory above
// the running executable or the working directory whose go.mod declares
// conch's module. It returns "" when there is none.
func SourceDir() string {
	var starts []string
	if d := os.Getenv("CONCH_SOURCE"); d != "" {
		starts = append(starts, d)
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		starts = append(starts, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	for _, start := range starts {
		for dir := start; ; dir = filepath.Dir(dir) {
			if isConchModule(filepath.Join(dir, "go.mod")) {
				return dir
			}
			if parent := filepath.Dir(dir); parent == dir {
				break
			}
		}
	}
	return ""
}

func isConchModule(gomod string) bool {
	f, err := os.Open(gomod)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) == 2 && fields[0] == "module" {
			return fields[1] == modulePath
		}
	}
	return false
}

// cachedBinary is where a cross-built binary for platform is kept.
func cachedBinary(platform string) string {
	return filepath.Join(config.Dir(), "binaries", strings.ReplaceAll(platform, "/", "-"), "conch")
}

// cacheKey is what a cached binary must have been made against to be
// installed again: this client's build, and the state of the source tree a
// cross-build would read. The client alone is not enough — a checkout that
// moves on without conch being rebuilt, or a conch built in one worktree
// and run from another, leaves the two out of step, and then the binary
// conch installs on every machine is from a build nobody is running. That
// is not visible as a build mismatch either: it shows up as a remote whose
// server always lacks capabilities, with "reload the server" as the advice
// and no reload that can help.
func cacheKey(src string) string {
	return buildinfo.Build() + " " + sourceStamp(src)
}

// sourceStamp fingerprints the source tree: the name, size and
// modification time of every file `go build` would read. "" has none.
func sourceStamp(src string) string {
	if src == "" {
		return "nosource"
	}
	h := sha256.New()
	_ = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			// Dot directories (.git above all), what a package manager
			// unpacked, and conch's own build output hold nothing the
			// compiler reads and plenty of files to stat.
			if path != src && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "bin") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") && name != "go.mod" && name != "go.sum" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			rel = path
		}
		fmt.Fprintf(h, "%s %d %d\n", rel, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// stampCache records what the cached binary for a platform was made
// against, so that the next install knows whether to make it again.
func stampCache(out, src string) {
	_ = os.WriteFile(out+".client-build", []byte(cacheKey(src)+"\n"), 0o600)
}

// crossBuild builds conch for platform from the source tree, unless the
// cached binary was already made against this client build and this source
// tree. The cache is keyed on both so client and remote stay in step:
// rebuilding conch locally, or a checkout that moves on, makes the next
// install build again for remotes too.
func crossBuild(ctx context.Context, platform string, say func(string)) (string, error) {
	buildMu.Lock()
	defer buildMu.Unlock()

	src := SourceDir()
	out := cachedBinary(platform)
	stamp := out + ".client-build"
	if b, err := os.ReadFile(stamp); err == nil && strings.TrimSpace(string(b)) == cacheKey(src) && exists(out) {
		return out, nil
	}

	goBin, goErr := exec.LookPath("go")
	if src == "" || goErr != nil {
		// No way to build: a release binary of this version, else one put
		// there by hand.
		if proto.IsRelease() {
			if bin, err := downloadRelease(ctx, platform, say); err == nil {
				stampCache(bin, src)
				return bin, nil
			} else if !exists(out) {
				return "", err
			}
		}
		if exists(out) {
			// All there is. It may well be from another build than this
			// conch — nothing here can make a fresh one — so it is said
			// rather than passed off as this build.
			say(fmt.Sprintf("no way to build conch for %s here: installing the cached binary, which may be from another build", platform))
			return out, nil
		}
	}
	if src == "" {
		return "", fmt.Errorf("no conch binary for %s and no conch source tree to build one from (set CONCH_SOURCE, or CONCH_REMOTE_BINARY to a binary)", platform)
	}
	if goErr != nil {
		return "", fmt.Errorf("no conch binary for %s: building one needs the Go toolchain (found source at %s)", platform, src)
	}
	osName, arch, _ := strings.Cut(platform, "/")
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return "", err
	}

	say(fmt.Sprintf("building conch for %s from %s", platform, src))
	tmp := out + ".tmp"
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	ldflags := "-X " + modulePath + "/internal/proto.Version=" + proto.Version +
		" -X " + modulePath + "/internal/buildinfo.SourceBuild=" + buildinfo.ID()
	cmd := exec.CommandContext(ctx, goBin, "build", "-trimpath", "-ldflags", ldflags, "-o", tmp, "./cmd/conch")
	cmd.Dir = src
	cmd.Env = config.MergeEnv(os.Environ(), "CGO_ENABLED=0", "GOOS="+osName, "GOARCH="+arch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("build conch for %s: %v\n%s", platform, err, strings.TrimSpace(stderr.String()))
	}
	if err := os.Rename(tmp, out); err != nil {
		return "", err
	}
	stampCache(out, src)
	return out, nil
}

// binaryFor returns a conch binary for platform: this executable when the
// platforms match, $CONCH_REMOTE_BINARY, a build from source (cached), or
// the matching release download.
func binaryFor(ctx context.Context, platform string, say func(string)) (string, error) {
	if platform == runtime.GOOS+"/"+runtime.GOARCH {
		return os.Executable()
	}
	if b := os.Getenv("CONCH_REMOTE_BINARY"); b != "" {
		return b, nil
	}
	return crossBuild(ctx, platform, say)
}
