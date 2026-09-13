// Package buildinfo identifies this conch binary, so a client can tell
// whether a remote machine runs the same build.
package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime"
	"sync"

	"github.com/Amitgb14/conch/internal/proto"
)

var (
	once  sync.Once
	build string
)

// SourceBuild is set (with -ldflags -X) on binaries cross-built for another
// platform: the build of the conch they were built alongside. Their own
// hash differs from it, but they are the same build.
var SourceBuild string

// Build is a short hash of the running executable; "" if it can't be read.
// Rebuilding changes it even when the version string doesn't. Call it early:
// it hashes the file as it is on disk at the first call.
func Build() string {
	once.Do(func() {
		if exe, err := os.Executable(); err == nil {
			build = HashFile(exe)
		}
	})
	return build
}

// HashFile is the build hash of the executable at path; "" if unreadable.
func HashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// ID identifies the build across platforms: the source build for a
// cross-built binary, else its own hash.
func ID() string {
	if SourceBuild != "" {
		return SourceBuild
	}
	return Build()
}

// Platform is GOOS/GOARCH of this binary, e.g. "darwin/arm64".
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// Info is what `conch version --json` prints.
type Info struct {
	Version      string   `json:"version"`
	Build        string   `json:"build"`
	BuildID      string   `json:"build_id,omitempty"`
	Platform     string   `json:"platform"`
	Capabilities []string `json:"capabilities"`
}

// Current describes this binary.
func Current() Info {
	return Info{Version: proto.Version, Build: Build(), BuildID: ID(), Platform: Platform(), Capabilities: proto.Capabilities}
}
