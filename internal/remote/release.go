package remote

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/proto"
)

// releaseBase is where release assets are downloaded from; $CONCH_RELEASE_URL
// overrides it (a mirror, or a test server).
func releaseBase() string {
	if u := os.Getenv("CONCH_RELEASE_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "https://" + modulePath + "/releases/download"
}

// ReleaseAsset names the archive published for platform, e.g.
// conch_0.2.0_linux_amd64.tar.gz. scripts/release.sh and install.sh use the
// same scheme.
func ReleaseAsset(version, platform string) string {
	return fmt.Sprintf("conch_%s_%s.tar.gz", version, strings.ReplaceAll(platform, "/", "_"))
}

// downloadRelease fetches this version's release binary for platform into
// the cache. The caller holds buildMu.
func downloadRelease(ctx context.Context, platform string, say func(string)) (string, error) {
	say(fmt.Sprintf("downloading conch %s for %s", proto.Version, platform))
	bin, err := FetchRelease(ctx, proto.Version, platform)
	if err != nil {
		return "", err
	}
	out := cachedBinary(platform)
	if err := writeExecutable(out, bin); err != nil {
		return "", err
	}
	_ = os.WriteFile(out+".client-build", []byte(buildinfo.Build()+"\n"), 0o600)
	return out, nil
}

// FetchRelease downloads the conch binary of a release for platform and
// verifies it against the release checksums.
func FetchRelease(ctx context.Context, version, platform string) ([]byte, error) {
	version = strings.TrimPrefix(version, "v")
	asset := ReleaseAsset(version, platform)
	base := releaseBase() + "/v" + version
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	sums, err := fetch(ctx, base+"/checksums.txt")
	if err != nil {
		return nil, fmt.Errorf("download conch %s for %s: %w", version, platform, err)
	}
	want := checksumFor(sums, asset)
	if want == "" {
		return nil, fmt.Errorf("download conch %s: %s is not in the release checksums", version, asset)
	}
	archive, err := fetch(ctx, base+"/"+asset)
	if err != nil {
		return nil, fmt.Errorf("download conch %s for %s: %w", version, platform, err)
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("download conch %s: checksum mismatch for %s", version, asset)
	}
	bin, err := extractBinary(archive)
	if err != nil {
		return nil, fmt.Errorf("unpack %s: %w", asset, err)
	}
	return bin, nil
}

// LatestRelease returns the newest published version, e.g. "0.2.0". The
// latest-release page redirects to its tag, so no API token is involved.
func LatestRelease(ctx context.Context) (string, error) {
	base := releaseBase()
	url := strings.TrimSuffix(base, "/download") + "/latest"
	if os.Getenv("CONCH_RELEASE_URL") != "" {
		url = base + "/latest"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", err
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Do(req)
	if err != nil {
		return "", fmt.Errorf("find latest conch release: %w", err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/v")
	if i < 0 {
		return "", fmt.Errorf("find latest conch release: no published release (%s)", resp.Status)
	}
	return loc[i+len("/tag/v"):], nil
}

// ReplaceExecutable atomically swaps the file at path for bin.
func ReplaceExecutable(path string, bin []byte) error {
	return writeExecutable(path, bin)
}

func writeExecutable(path string, bin []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

// checksumFor finds name in sha256sum-style output.
func checksumFor(sums []byte, name string) string {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

// extractBinary returns the conch executable from a release archive.
func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("no conch binary in archive")
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "conch" {
			return io.ReadAll(io.LimitReader(tr, 256<<20))
		}
	}
}
