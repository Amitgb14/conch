package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// repoFile is a file at the repository root.
func repoFile(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// installScript runs install.sh against a release server, in a scratch
// home, and returns its output and error.
func installScript(t *testing.T, base, dir string, extra ...string) (string, error) {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command("sh", repoFile(t, "install.sh"))
	cmd.Env = append([]string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + home,
		"CONCH_RELEASE_URL=" + base,
		"CONCH_INSTALL_DIR=" + dir,
	}, extra...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestInstallScript(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("install.sh supports darwin and linux on amd64 and arm64")
	}
	for _, tool := range []string{"sh", "tar", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("needs " + tool)
		}
	}
	const version = "9.9.9"
	platform := runtime.GOOS + "/" + runtime.GOARCH
	asset := ReleaseAsset(version, platform)
	// Laid out as scripts/release.sh builds it.
	archive := tarball(t, strings.TrimSuffix(asset, ".tar.gz")+"/conch", []byte("#!/bin/sh\necho conch 9.9.9\n"))
	sum := sha256.Sum256(archive)
	sums := hex.EncodeToString(sum[:]) + "  " + asset + "\n"
	var serveSums atomic.Value // the handler reads it while the test changes it
	serveSums.Store(sums)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v" + version + "/checksums.txt":
			w.Write([]byte("0000  conch_9.9.9_plan9_mips.tar.gz\n" + serveSums.Load().(string)))
		case "/v" + version + "/" + asset:
			w.Write(archive)
		case "/latest": // as GitHub's latest-release page does
			w.Header().Set("Location", "/releases/tag/v"+version)
			w.WriteHeader(http.StatusFound)
		case "/releases/tag/v" + version:
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Installs, with or without a v, and says when the folder isn't on PATH.
	dir := filepath.Join(t.TempDir(), "bin")
	out, err := installScript(t, srv.URL, dir, "CONCH_VERSION=v"+version)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	bin := filepath.Join(dir, "conch")
	st, err := os.Stat(bin)
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("installed binary: %v %v", st, err)
	}
	if got, _ := exec.Command(bin).Output(); string(got) != "conch 9.9.9\n" {
		t.Fatalf("installed binary runs: %q", got)
	}
	if !strings.Contains(out, "downloading conch 9.9.9 for "+platform) || !strings.Contains(out, "add "+dir+" to your PATH") {
		t.Fatalf("output:\n%s", out)
	}
	if _, err := os.Stat(bin + ".new"); !os.IsNotExist(err) {
		t.Fatal("conch.new left behind")
	}
	// Reinstalling over it works too (the binary is replaced, not appended to).
	if out, err := installScript(t, srv.URL, dir, "CONCH_VERSION="+version); err != nil {
		t.Fatalf("reinstall: %v\n%s", err, out)
	}

	// A tampered archive is refused and the installed binary kept.
	serveSums.Store(strings.Repeat("f", 64) + "  " + asset + "\n")
	out, err = installScript(t, srv.URL, dir, "CONCH_VERSION="+version)
	if err == nil || !strings.Contains(out, "checksum mismatch for "+asset) {
		t.Fatalf("bad checksum: %v\n%s", err, out)
	}
	if got, _ := exec.Command(bin).Output(); string(got) != "conch 9.9.9\n" {
		t.Fatal("a failed install changed the binary")
	}
	// An asset missing from the checksums is refused.
	serveSums.Store("")
	if out, err = installScript(t, srv.URL, dir, "CONCH_VERSION="+version); err == nil || !strings.Contains(out, asset+" is not in checksums.txt") {
		t.Fatalf("missing checksum: %v\n%s", err, out)
	}
	serveSums.Store(sums)
	// A version that wasn't published.
	if out, err = installScript(t, srv.URL, dir, "CONCH_VERSION=1.2.3"); err == nil || !strings.Contains(out, "download") {
		t.Fatalf("unknown version: %v\n%s", err, out)
	}
	// A version as an argument — how the curl one-liner goes back to an
	// older release — and "latest" meaning the newest.
	argDir := filepath.Join(t.TempDir(), "bin")
	arg := exec.Command("sh", repoFile(t, "install.sh"), version)
	arg.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "CONCH_RELEASE_URL=" + srv.URL, "CONCH_INSTALL_DIR=" + argDir}
	if out, err := arg.CombinedOutput(); err != nil || !strings.Contains(string(out), "downloading conch "+version) {
		t.Fatalf("version argument: %v\n%s", err, out)
	}
	if got, _ := exec.Command(filepath.Join(argDir, "conch")).Output(); string(got) != "conch 9.9.9\n" {
		t.Fatalf("version argument installed: %q", got)
	}
	// No version and "latest" both resolve the newest release.
	for _, args := range [][]string{{}, {"latest"}} {
		dir := filepath.Join(t.TempDir(), "bin")
		cmd := exec.Command("sh", append([]string{repoFile(t, "install.sh")}, args...)...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "CONCH_RELEASE_URL=" + srv.URL, "CONCH_INSTALL_DIR=" + dir}
		if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "downloading conch "+version) {
			t.Fatalf("args %v: %v\n%s", args, err, out)
		}
		if got, _ := exec.Command(filepath.Join(dir, "conch")).Output(); string(got) != "conch 9.9.9\n" {
			t.Fatalf("args %v installed: %q", args, got)
		}
	}
	// A "v"-prefixed argument names the same release, and an argument
	// wins over CONCH_VERSION; an empty one falls back to it.
	for _, c := range []struct {
		what string
		args []string
		env  string
	}{
		{"v-prefixed argument", []string{"v" + version}, "CONCH_VERSION=1.2.3"},
		{"argument over CONCH_VERSION", []string{version}, "CONCH_VERSION=1.2.3"},
		{"empty argument", []string{""}, "CONCH_VERSION=" + version},
	} {
		dir := filepath.Join(t.TempDir(), "bin")
		cmd := exec.Command("sh", append([]string{repoFile(t, "install.sh")}, c.args...)...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "CONCH_RELEASE_URL=" + srv.URL, "CONCH_INSTALL_DIR=" + dir, c.env}
		if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "downloading conch "+version) {
			t.Fatalf("%s: %v\n%s", c.what, err, out)
		}
		if got, _ := exec.Command(filepath.Join(dir, "conch")).Output(); string(got) != "conch 9.9.9\n" {
			t.Fatalf("%s installed: %q", c.what, got)
		}
	}

	// Nothing published: it says so instead of installing nothing.
	empty := httptest.NewServer(http.NotFoundHandler())
	defer empty.Close()
	if out, err := installScript(t, empty.URL, filepath.Join(t.TempDir(), "bin")); err == nil || !strings.Contains(out, "could not find the latest release") {
		t.Fatalf("no releases: %v\n%s", err, out)
	}

	// With its folder on PATH there is no hint.
	cmd := exec.Command("sh", repoFile(t, "install.sh"))
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CONCH_RELEASE_URL=" + srv.URL, "CONCH_INSTALL_DIR=" + dir, "CONCH_VERSION=" + version}
	if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "to your PATH") {
		t.Fatalf("on PATH: %v\n%s", err, out)
	}
}

// release.sh builds the archives install.sh and FetchRelease download:
// the three must agree on names and layout.
func TestReleaseScriptsAgreeOnNames(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(repoFile(t, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	release, install := read("scripts/release.sh"), read("install.sh")
	const name = "conch_${version}_${os}_${arch}"
	for _, want := range []string{`name="` + name + `"`, `"$dist/$name.tar.gz" "$name"`, `-o "$stage/conch"`, "checksums.txt",
		"darwin/amd64 darwin/arm64 linux/amd64 linux/arm64", "proto.Version=$version", "version=${version#v}"} {
		if !strings.Contains(release, want) {
			t.Errorf("release.sh lacks %q", want)
		}
	}
	for _, want := range []string{`asset="` + name + `.tar.gz"`, `"$tmp/` + name + `/conch"`, `/v$version"`, "checksums.txt", "version=${version#v}"} {
		if !strings.Contains(install, want) {
			t.Errorf("install.sh lacks %q", want)
		}
	}
	if got := ReleaseAsset("1.2.3", "linux/arm64"); got != "conch_1.2.3_linux_arm64.tar.gz" {
		t.Errorf("ReleaseAsset = %q", got)
	}
	wf := read(".github/workflows/release.yml")
	for _, want := range []string{`tags: ["v*"]`, `scripts/release.sh "${GITHUB_REF_NAME}"`, "dist/*.tar.gz dist/checksums.txt"} {
		if !strings.Contains(wf, want) {
			t.Errorf("release.yml lacks %q", want)
		}
	}
}
