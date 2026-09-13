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

// Build is a short hash of the running executable; "" if it can't be read.
// Rebuilding changes it even when the version string doesn't.
func Build() string {
	once.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		f, err := os.Open(exe)
		if err != nil {
			return
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return
		}
		build = hex.EncodeToString(h.Sum(nil))[:12]
	})
	return build
}

// Platform is GOOS/GOARCH of this binary, e.g. "darwin/arm64".
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// Info is what `conch version --json` prints.
type Info struct {
	Version      string   `json:"version"`
	Build        string   `json:"build"`
	Platform     string   `json:"platform"`
	Capabilities []string `json:"capabilities"`
}

// Current describes this binary.
func Current() Info {
	return Info{Version: proto.Version, Build: Build(), Platform: Platform(), Capabilities: proto.Capabilities}
}
