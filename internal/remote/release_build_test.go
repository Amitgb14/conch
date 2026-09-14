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
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/proto"
)

func TestA4ReleaseBase(t *testing.T) {
	t.Setenv("CONCH_RELEASE_URL", "")
	if got := releaseBase(); got != "https://github.com/Amitgb14/conch/releases/download" {
		t.Fatalf("default base %q", got)
	}
	t.Setenv("CONCH_RELEASE_URL", "http://mirror.invalid/conch///")
	if got := releaseBase(); got != "http://mirror.invalid/conch" {
		t.Fatalf("trailing slashes kept: %q", got)
	}
}

func TestA4ReleaseAssetPerPlatform(t *testing.T) {
	for platform, want := range map[string]string{
		"linux/amd64":  "conch_1.2.3_linux_amd64.tar.gz",
		"linux/arm64":  "conch_1.2.3_linux_arm64.tar.gz",
		"darwin/amd64": "conch_1.2.3_darwin_amd64.tar.gz",
		"darwin/arm64": "conch_1.2.3_darwin_arm64.tar.gz",
	} {
		if got := ReleaseAsset("1.2.3", platform); got != want {
			t.Errorf("ReleaseAsset(%s) = %q, want %q", platform, got, want)
		}
	}
}

func TestA4ChecksumFor(t *testing.T) {
	sums := []byte("ABCDEF  conch_1_linux_amd64.tar.gz\n" +
		"123 *conch_1_darwin_arm64.tar.gz\n" +
		"garbage line with many fields\n" +
		"999  conch_1_linux_amd64.tar.gz.sig\n")
	if got := checksumFor(sums, "conch_1_linux_amd64.tar.gz"); got != "abcdef" {
		t.Fatalf("lowercased sum %q", got)
	}
	if got := checksumFor(sums, "conch_1_darwin_arm64.tar.gz"); got != "123" {
		t.Fatalf("binary-mode marker: %q", got)
	}
	if got := checksumFor(sums, "conch_1_linux_arm64.tar.gz"); got != "" {
		t.Fatalf("absent asset %q", got)
	}
}

func a4Tar(t *testing.T, entries ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		body := []byte("body of " + h.Name)
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write(body)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestA4ExtractBinary(t *testing.T) {
	if _, err := extractBinary([]byte("not gzip")); err == nil {
		t.Fatal("non-gzip accepted")
	}
	noConch := a4Tar(t, &tar.Header{Name: "conch_x/README", Mode: 0o644, Typeflag: tar.TypeReg})
	if _, err := extractBinary(noConch); err == nil || !strings.Contains(err.Error(), "no conch binary") {
		t.Fatalf("archive without conch: %v", err)
	}
	// A directory called conch is not the binary; the regular file is.
	withDir := a4Tar(t,
		&tar.Header{Name: "conch/", Mode: 0o755, Typeflag: tar.TypeDir},
		&tar.Header{Name: "conch/conch", Mode: 0o755, Typeflag: tar.TypeReg})
	if b, err := extractBinary(withDir); err != nil || string(b) != "body of conch/conch" {
		t.Fatalf("dir then file: %q %v", b, err)
	}
	// Truncated tar stream inside valid gzip.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write(bytes.Repeat([]byte{0xff}, 700))
	gz.Close()
	if _, err := extractBinary(buf.Bytes()); err == nil {
		t.Fatal("corrupt tar accepted")
	}
}

// a4ReleaseServer serves one release; handlers can be swapped per test.
func a4ReleaseServer(t *testing.T, version, platform string, binary []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	archive := tarball(t, "conch_"+version+"/conch", binary)
	sum := sha256.Sum256(archive)
	asset := ReleaseAsset(version, platform)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/v" + version + "/checksums.txt":
			w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n"))
		case "/v" + version + "/" + asset:
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CONCH_RELEASE_URL", srv.URL)
	return srv, &hits
}

func TestA4FetchRelease(t *testing.T) {
	a4Env(t)
	a4ReleaseServer(t, "2.0.0", "darwin/arm64", []byte("the binary"))

	bin, err := FetchRelease(context.Background(), "v2.0.0", "darwin/arm64")
	if err != nil || string(bin) != "the binary" {
		t.Fatalf("fetch with v prefix: %q %v", bin, err)
	}
	if _, err := FetchRelease(context.Background(), "3.0.0", "darwin/arm64"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("unknown version: %v", err)
	}
}

func TestA4FetchReleaseBadArchive(t *testing.T) {
	a4Env(t)
	bad := []byte("definitely not a tarball")
	sum := sha256.Sum256(bad)
	asset := ReleaseAsset("1.0.0", "linux/amd64")
	missingAsset := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0.0/checksums.txt":
			w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n"))
		case "/v1.0.0/" + asset:
			if missingAsset {
				http.Error(w, "gone", http.StatusGone)
				return
			}
			w.Write(bad)
		}
	}))
	defer srv.Close()
	t.Setenv("CONCH_RELEASE_URL", srv.URL)

	if _, err := FetchRelease(context.Background(), "1.0.0", "linux/amd64"); err == nil || !strings.Contains(err.Error(), "unpack "+asset) {
		t.Fatalf("bad archive: %v", err)
	}
	missingAsset = true
	if _, err := FetchRelease(context.Background(), "1.0.0", "linux/amd64"); err == nil || !strings.Contains(err.Error(), "410") {
		t.Fatalf("missing asset: %v", err)
	}
}

func TestA4FetchErrors(t *testing.T) {
	if _, err := fetch(context.Background(), "://bad url"); err == nil {
		t.Fatal("bad url accepted")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // connection refused from here on
	if _, err := fetch(context.Background(), url+"/x"); err == nil {
		t.Fatal("closed server")
	}
	t.Setenv("CONCH_RELEASE_URL", url)
	if _, err := LatestRelease(context.Background()); err == nil || !strings.Contains(err.Error(), "find latest conch release") {
		t.Fatalf("latest from closed server: %v", err)
	}
	t.Setenv("CONCH_RELEASE_URL", "http://bad host:99999")
	if _, err := LatestRelease(context.Background()); err == nil {
		t.Fatal("latest from invalid url")
	}
}

func TestA4LatestReleaseNoRedirect(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("CONCH_RELEASE_URL", srv.URL+"/")
	_, err := LatestRelease(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no published release") || !strings.Contains(err.Error(), "404") {
		t.Fatalf("no release: %v", err)
	}
	if method != http.MethodHead || path != "/latest" {
		t.Fatalf("request %s %s", method, path)
	}
}

func TestA4ReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "bin", "conch")
	if err := ReplaceExecutable(path, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceExecutable(path, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v %v", st, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "v2" {
		t.Fatalf("content %q", b)
	}
	if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}

	file := filepath.Join(dir, "plainfile")
	os.WriteFile(file, nil, 0o600)
	if err := ReplaceExecutable(filepath.Join(file, "conch"), []byte("x")); err == nil {
		t.Fatal("directory under a file accepted")
	}
	// Destination is a non-empty directory: the rename fails.
	target := filepath.Join(dir, "occupied")
	os.MkdirAll(filepath.Join(target, "child"), 0o700)
	if err := ReplaceExecutable(target, []byte("x")); err == nil {
		t.Fatal("rename over a directory accepted")
	}
	ro := filepath.Join(dir, "ro")
	os.MkdirAll(ro, 0o500)
	defer os.Chmod(ro, 0o700)
	if os.Getuid() != 0 {
		if err := ReplaceExecutable(filepath.Join(ro, "conch"), []byte("x")); err == nil {
			t.Fatal("write into read-only dir accepted")
		}
	}
}

func TestA4DownloadReleaseWriteFails(t *testing.T) {
	a4Env(t)
	old := proto.Version
	proto.Version = "4.5.6"
	defer func() { proto.Version = old }()
	a4ReleaseServer(t, "4.5.6", "linux/amd64", []byte("bin"))
	// The cache location is under a regular file.
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	t.Setenv("CONCH_HOME", file)
	if _, err := downloadRelease(context.Background(), "linux/amd64", func(string) {}); err == nil {
		t.Fatal("unwritable cache accepted")
	}
}

func TestA4IsConchModule(t *testing.T) {
	dir := t.TempDir()
	gomod := filepath.Join(dir, "go.mod")
	if isConchModule(gomod) {
		t.Fatal("missing go.mod")
	}
	os.WriteFile(gomod, []byte("// comment\nmodule example.com/other\n"), 0o600)
	if isConchModule(gomod) {
		t.Fatal("other module accepted")
	}
	os.WriteFile(gomod, []byte("go 1.22\n"), 0o600)
	if isConchModule(gomod) {
		t.Fatal("no module line accepted")
	}
	os.WriteFile(gomod, []byte("module github.com/Amitgb14/conch\n\ngo 1.25\n"), 0o600)
	if !isConchModule(gomod) {
		t.Fatal("conch module rejected")
	}
}

// a4NoSource makes SourceDir find nothing: no CONCH_SOURCE and a working
// directory outside any conch checkout. The test binary lives in a build
// cache directory, which has no go.mod above it.
func a4NoSource(t *testing.T) {
	t.Helper()
	t.Setenv("CONCH_SOURCE", "")
	t.Chdir(t.TempDir())
	if exe, _ := os.Executable(); SourceDir() != "" {
		t.Skipf("test binary %s sits inside a conch tree", exe)
	}
}

func a4FakeSource(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "go.mod"), []byte("module github.com/Amitgb14/conch\n"), 0o600)
	os.MkdirAll(filepath.Join(src, "internal", "deep"), 0o700)
	return src
}

func TestA4SourceDirFromEnv(t *testing.T) {
	src := a4FakeSource(t)
	a4NoSource(t)
	t.Setenv("CONCH_SOURCE", filepath.Join(src, "internal", "deep"))
	if got := SourceDir(); got != src {
		t.Fatalf("SourceDir %q, want %q", got, src)
	}
}

func TestA4CrossBuildCacheHit(t *testing.T) {
	a4Env(t)
	a4NoSource(t)
	out := cachedBinary("linux/arm64")
	if want := filepath.Join(os.Getenv("CONCH_HOME"), "binaries", "linux-arm64", "conch"); out != want {
		t.Fatalf("cache path %q", out)
	}
	os.MkdirAll(filepath.Dir(out), 0o700)
	os.WriteFile(out, []byte("cached"), 0o755)
	os.WriteFile(out+".client-build", []byte(buildinfo.Build()+"\n"), 0o600)
	var steps []string
	got, err := crossBuild(context.Background(), "linux/arm64", func(s string) { steps = append(steps, s) })
	if err != nil || got != out || len(steps) != 0 {
		t.Fatalf("cache hit: %q %v %q", got, err, steps)
	}
}

func TestA4CrossBuildNothingAvailable(t *testing.T) {
	a4Env(t)
	a4NoSource(t)
	old := proto.Version
	proto.Version = "0.1.0-dev"
	defer func() { proto.Version = old }()
	_, err := crossBuild(context.Background(), "linux/arm64", func(string) {})
	if err == nil || !strings.Contains(err.Error(), "no conch source tree") || !strings.Contains(err.Error(), "CONCH_REMOTE_BINARY") {
		t.Fatalf("no source: %v", err)
	}

	// A stale binary put there by hand is used rather than failing.
	out := cachedBinary("linux/arm64")
	os.MkdirAll(filepath.Dir(out), 0o700)
	os.WriteFile(out, []byte("by hand"), 0o755)
	if got, err := crossBuild(context.Background(), "linux/arm64", func(string) {}); err != nil || got != out {
		t.Fatalf("hand-placed binary: %q %v", got, err)
	}
}

func TestA4CrossBuildDownloadsRelease(t *testing.T) {
	a4Env(t)
	a4NoSource(t)
	old := proto.Version
	proto.Version = "5.0.0"
	defer func() { proto.Version = old }()
	_, hits := a4ReleaseServer(t, "5.0.0", "linux/arm64", []byte("released binary"))

	var steps []string
	got, err := crossBuild(context.Background(), "linux/arm64", func(s string) { steps = append(steps, s) })
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got); string(b) != "released binary" {
		t.Fatalf("binary %q", b)
	}
	if len(steps) != 1 || !strings.Contains(steps[0], "downloading conch 5.0.0 for linux/arm64") {
		t.Fatalf("steps %q", steps)
	}
	// Stamped with this client build, so the next call is a cache hit.
	n := hits.Load()
	if _, err := crossBuild(context.Background(), "linux/arm64", func(string) {}); err != nil || hits.Load() != n {
		t.Fatalf("second call downloaded again: %v (hits %d -> %d)", err, n, hits.Load())
	}

	// Download fails and nothing is cached: the download error is returned.
	_, err = crossBuild(context.Background(), "darwin/amd64", func(string) {})
	if err == nil || !strings.Contains(err.Error(), "not in the release checksums") {
		t.Fatalf("failed download: %v", err)
	}

	// Download fails but an older cached binary exists: use it.
	out := cachedBinary("darwin/amd64")
	os.MkdirAll(filepath.Dir(out), 0o700)
	os.WriteFile(out, []byte("old"), 0o755)
	if got, err := crossBuild(context.Background(), "darwin/amd64", func(string) {}); err != nil || got != out {
		t.Fatalf("fallback to cache: %q %v", got, err)
	}
}

func TestA4CrossBuildNoGoToolchain(t *testing.T) {
	a4Env(t)
	src := a4FakeSource(t)
	a4NoSource(t)
	t.Setenv("CONCH_SOURCE", src)
	t.Setenv("PATH", t.TempDir()) // no go here
	old := proto.Version
	proto.Version = "0.1.0-dev"
	defer func() { proto.Version = old }()
	_, err := crossBuild(context.Background(), "linux/arm64", func(string) {})
	if err == nil || !strings.Contains(err.Error(), "needs the Go toolchain") || !strings.Contains(err.Error(), src) {
		t.Fatalf("no go: %v", err)
	}
}

func TestA4CrossBuildFails(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go toolchain")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	a4Env(t)
	src := a4FakeSource(t) // declares conch's module but has no cmd/conch
	a4NoSource(t)
	t.Setenv("CONCH_SOURCE", src)
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOFLAGS", "")
	var steps []string
	_, err := crossBuild(context.Background(), "linux/arm64", func(s string) { steps = append(steps, s) })
	if err == nil || !strings.Contains(err.Error(), "build conch for linux/arm64") {
		t.Fatalf("broken source: %v", err)
	}
	if len(steps) != 1 || !strings.Contains(steps[0], "building conch for linux/arm64 from "+src) {
		t.Fatalf("steps %q", steps)
	}
	out := cachedBinary("linux/arm64")
	if _, err := os.Stat(out + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary build output left behind")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("failed build cached")
	}
}

func TestA4CrossBuildCacheDirUnwritable(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	a4Env(t)
	src := a4FakeSource(t)
	a4NoSource(t)
	t.Setenv("CONCH_SOURCE", src)
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	t.Setenv("CONCH_HOME", file)
	if _, err := crossBuild(context.Background(), "linux/arm64", func(string) {}); err == nil {
		t.Fatal("cache dir under a file accepted")
	}
}
