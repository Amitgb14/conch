package remote

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
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

// crossBuild builds conch for platform from the source tree, unless the
// cached binary was already built alongside this very client build. The
// cache is keyed on the local build so client and remote stay in step:
// rebuilding conch locally makes the next install rebuild for remotes too.
func crossBuild(ctx context.Context, platform string, say func(string)) (string, error) {
	buildMu.Lock()
	defer buildMu.Unlock()

	out := cachedBinary(platform)
	stamp := out + ".client-build"
	if b, err := os.ReadFile(stamp); err == nil && strings.TrimSpace(string(b)) == buildinfo.Build() && exists(out) {
		return out, nil
	}

	src := SourceDir()
	goBin, goErr := exec.LookPath("go")
	if src == "" || goErr != nil {
		// No way to build: a release binary of this version, else one put
		// there by hand.
		if proto.IsRelease() {
			if bin, err := downloadRelease(ctx, platform, say); err == nil {
				return bin, nil
			} else if !exists(out) {
				return "", err
			}
		}
		if exists(out) {
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
	_ = os.WriteFile(stamp, []byte(buildinfo.Build()+"\n"), 0o600)
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
