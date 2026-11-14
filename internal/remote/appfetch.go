package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AppAsset names Conch.app's archive in a release, as macos/release.sh
// makes it: Conch_0.2.1_macos_arm64.zip. Only Apple Silicon is published,
// since Ghostty's library is built for it alone, so another arch
// (runtime.GOARCH) has no asset to choose.
func AppAsset(version, arch string) (string, error) {
	version = strings.TrimPrefix(version, "v")
	if version == "" {
		return "", errors.New("Conch.app: no version to download")
	}
	if arch != "arm64" {
		return "", fmt.Errorf("Conch.app is published for Apple Silicon only, and this Mac is %s", archName(arch))
	}
	return fmt.Sprintf("Conch_%s_macos_arm64.zip", version), nil
}

func archName(arch string) string {
	switch arch {
	case "amd64":
		return "Intel"
	case "":
		return "of an unknown kind"
	}
	return arch
}

// ReleaseNotes is the page a release is described on.
func ReleaseNotes(version string) string {
	version = strings.TrimPrefix(version, "v")
	base := releaseBase()
	if os.Getenv("CONCH_RELEASE_URL") == "" {
		base = strings.TrimSuffix(base, "/download")
	}
	return base + "/tag/v" + version
}

// FetchApp downloads Conch.app's archive of a release into dir and checks
// it against the .sha256 published beside it, returning its path. A file
// that does not match is removed rather than left for somebody to open.
func FetchApp(ctx context.Context, version, arch, dir string) (string, error) {
	asset, err := AppAsset(version, arch)
	if err != nil {
		return "", err
	}
	version = strings.TrimPrefix(version, "v")
	url := releaseBase() + "/v" + version + "/" + asset
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	sums, err := fetch(ctx, url+".sha256")
	if err != nil {
		return "", fmt.Errorf("download Conch %s: its checksum: %w", version, err)
	}
	want := appChecksum(sums, asset)
	if want == "" {
		return "", fmt.Errorf("download Conch %s: %s.sha256 has no checksum for it", version, asset)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download Conch %s: %w", version, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download Conch %s: GET %s: %s", version, url, resp.Status)
	}
	path := filepath.Join(dir, asset)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 1<<30))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = fmt.Errorf("checksum mismatch for %s: it is not the file the release published", asset)
	}
	if err != nil {
		os.Remove(path)
		return "", fmt.Errorf("download Conch %s: %w", version, err)
	}
	return path, nil
}

// appChecksum reads shasum's output for name: "HASH  name", "HASH *name",
// or a bare hash. Anything that is not 64 hex digits is no checksum.
func appChecksum(sums []byte, name string) string {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || len(f) > 2 || (len(f) == 2 && strings.TrimPrefix(f[1], "*") != name) {
			continue
		}
		sum := strings.ToLower(f[0])
		if b, err := hex.DecodeString(sum); err == nil && len(b) == sha256.Size {
			return sum
		}
	}
	return ""
}
