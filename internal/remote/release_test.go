package remote

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func tarball(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		data []byte
	}{{"conch_x/LICENSE", []byte("license")}, {name, body}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(f.data)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestDownloadRelease(t *testing.T) {
	old := proto.Version
	proto.Version = "9.8.7"
	defer func() { proto.Version = old }()
	t.Setenv("CONCH_HOME", t.TempDir())

	archive := tarball(t, "conch_x/conch", []byte("#!/bin/sh\necho fake\n"))
	sum := sha256.Sum256(archive)
	asset := ReleaseAsset("9.8.7", "linux/arm64")
	if asset != "conch_9.8.7_linux_arm64.tar.gz" {
		t.Fatalf("asset name %q", asset)
	}
	checksums := hex.EncodeToString(sum[:]) + "  " + asset + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v9.8.7/checksums.txt":
			w.Write([]byte(checksums))
		case "/v9.8.7/" + asset:
			w.Write(archive)
		case "/latest":
			w.Header().Set("Location", "https://github.com/Amitgb14/conch/releases/tag/v9.8.7")
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("CONCH_RELEASE_URL", srv.URL)

	bin, err := downloadRelease(context.Background(), "linux/arm64", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); !strings.Contains(string(b), "echo fake") {
		t.Fatalf("binary: %q", b)
	}

	if v, err := LatestRelease(context.Background()); err != nil || v != "9.8.7" {
		t.Fatalf("latest: %q %v", v, err)
	}

	checksums = strings.Repeat("0", 64) + "  " + asset + "\n"
	if _, err := downloadRelease(context.Background(), "linux/arm64", func(string) {}); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered archive: %v", err)
	}
	if _, err := downloadRelease(context.Background(), "darwin/amd64", func(string) {}); err == nil || !strings.Contains(err.Error(), "not in the release checksums") {
		t.Fatalf("missing asset: %v", err)
	}
}
