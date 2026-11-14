package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Conch.app is published for Apple Silicon only, under the name
// macos/release.sh gives it.
func TestAppAsset(t *testing.T) {
	if got, err := AppAsset("v0.2.1", "arm64"); err != nil || got != "Conch_0.2.1_macos_arm64.zip" {
		t.Fatalf("arm64: %q %v", got, err)
	}
	if got, err := AppAsset("0.3.0-rc.1", "arm64"); err != nil || got != "Conch_0.3.0-rc.1_macos_arm64.zip" {
		t.Fatalf("pre-release: %q %v", got, err)
	}
	for _, arch := range []string{"amd64", "", "386"} {
		if _, err := AppAsset("0.2.1", arch); err == nil || !strings.Contains(err.Error(), "Apple Silicon") {
			t.Errorf("%q: %v", arch, err)
		}
	}
	if _, err := AppAsset("", "arm64"); err == nil {
		t.Error("no version: no error")
	}
}

func TestReleaseNotes(t *testing.T) {
	t.Setenv("CONCH_RELEASE_URL", "")
	if got := ReleaseNotes("0.2.1"); got != "https://github.com/Amitgb14/conch/releases/tag/v0.2.1" {
		t.Fatal(got)
	}
	t.Setenv("CONCH_RELEASE_URL", "http://mirror.test/r/")
	if got := ReleaseNotes("v0.2.1"); got != "http://mirror.test/r/tag/v0.2.1" {
		t.Fatal(got)
	}
}

// appServer serves one release of Conch.app; sum is what its .sha256 says.
func appServer(t *testing.T, zip []byte, sum *string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0.2.1/Conch_0.2.1_macos_arm64.zip":
			w.Write(zip)
		case "/v0.2.1/Conch_0.2.1_macos_arm64.zip.sha256":
			if *sum == "" {
				http.NotFound(w, r)
				return
			}
			w.Write([]byte(*sum))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CONCH_RELEASE_URL", srv.URL)
}

func TestFetchApp(t *testing.T) {
	zip := []byte("PK\x03\x04 not really a zip")
	h := sha256.Sum256(zip)
	good := hex.EncodeToString(h[:]) + "  Conch_0.2.1_macos_arm64.zip\n"
	sum := good
	appServer(t, zip, &sum)
	ctx := context.Background()

	dir := t.TempDir()
	path, err := FetchApp(ctx, "0.2.1", "arm64", dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir || filepath.Base(path) != "Conch_0.2.1_macos_arm64.zip" {
		t.Fatalf("path %s", path)
	}
	if b, _ := os.ReadFile(path); string(b) != string(zip) {
		t.Fatalf("content %q", b)
	}

	// The checksum in upper case, as a bare hash, or marked binary (*name):
	// shasum's forms all read.
	for _, s := range []string{strings.ToUpper(hex.EncodeToString(h[:])) + "  Conch_0.2.1_macos_arm64.zip\n",
		hex.EncodeToString(h[:]) + "\n", hex.EncodeToString(h[:]) + " *Conch_0.2.1_macos_arm64.zip\n"} {
		sum = s
		if _, err := FetchApp(ctx, "0.2.1", "arm64", t.TempDir()); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}

	// A wrong checksum leaves nothing behind.
	sum = strings.Repeat("0", 64) + "  Conch_0.2.1_macos_arm64.zip\n"
	dir = t.TempDir()
	if _, err := FetchApp(ctx, "0.2.1", "arm64", dir); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("mismatch: %v", err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
	// A checksum for another file, or garbage, is no checksum.
	for _, s := range []string{hex.EncodeToString(h[:]) + "  Conch_0.2.0_macos_arm64.zip\n", "not a hash\n", ""} {
		sum = s
		if _, err := FetchApp(ctx, "0.2.1", "arm64", t.TempDir()); err == nil || !strings.Contains(err.Error(), "checksum") {
			t.Errorf("%q: %v", s, err)
		}
	}
	// A release with no app for it.
	sum = good
	if _, err := FetchApp(ctx, "0.2.2", "arm64", t.TempDir()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing release: %v", err)
	}
	// Intel: refused before anything is downloaded.
	if _, err := FetchApp(ctx, "0.2.1", "amd64", t.TempDir()); err == nil || !strings.Contains(err.Error(), "Apple Silicon") {
		t.Fatalf("amd64: %v", err)
	}
	// A cancelled context.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := FetchApp(cctx, "0.2.1", "arm64", t.TempDir()); err == nil {
		t.Fatal("cancelled: no error")
	}
}
