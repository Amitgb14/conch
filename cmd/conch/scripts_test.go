package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The release scripts run on a tag, or by hand on a Mac — never in CI's
// test run — so a typo in one is found at the worst moment. Each one has
// to parse as sh, and the desktop beta's has to stamp a version and leave
// this Mac's source path out of what it ships.
func TestReleaseScriptsParse(t *testing.T) {
	root := filepath.Join("..", "..")
	scripts := []string{"scripts/release.sh", "scripts/brew-formula.sh", "macos/bundle.sh", "macos/release.sh"}
	for _, s := range scripts {
		out, err := exec.Command("sh", "-n", filepath.Join(root, s)).CombinedOutput()
		if err != nil {
			t.Errorf("%s does not parse: %v\n%s", s, err, out)
		}
	}
	bundle, err := os.ReadFile(filepath.Join(root, "macos/bundle.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"internal/proto.Version=${CONCH_VERSION#v}", `[ -z "${CONCH_VERSION:-}" ] || source_key=""`} {
		if !strings.Contains(string(bundle), want) {
			t.Errorf("macos/bundle.sh lost %q: a release build would not be stamped, or would ship this Mac's source path", want)
		}
	}
	release, err := os.ReadFile(filepath.Join(root, "macos/release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CONCH_VERSION=$version", "codesign --verify", "Print :ConchSource", `[ "$got" = "$version" ]`,
		// Notarized means: hardened runtime, Apple's acceptance checked, the
		// ticket stapled, and Gatekeeper asked rather than assumed.
		"--options runtime", `grep -q "status: Accepted"`, "stapler staple", "Notarized Developer ID"} {
		if !strings.Contains(string(release), want) {
			t.Errorf("macos/release.sh lost its check %q", want)
		}
	}
}

// The website's capture scripts are the same kind of thing: they run by
// hand on a Mac to redraw the screenshots, never in CI, so nothing else
// would notice a typo until somebody needed them. They must parse, and
// the ones that photograph a phone must keep conch away from the
// developer's own server — a capture that ran against it would send
// keystrokes into real agents and put real work on the website.
func TestCaptureScriptsParse(t *testing.T) {
	root := filepath.Join("..", "..")
	scripts := []string{
		"web/scripts/tui-capture/capture.sh", "web/scripts/tui-capture/run.sh", "web/scripts/tui-capture/stage.sh",
		"web/scripts/phone-capture/capture.sh",
	}
	for _, s := range scripts {
		out, err := exec.Command("bash", "-n", filepath.Join(root, s)).CombinedOutput()
		if err != nil {
			t.Errorf("%s does not parse: %v\n%s", s, err, out)
		}
	}
	phone, err := os.ReadFile(filepath.Join(root, "web/scripts/phone-capture/capture.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"unset CONCH_SOCKET CONCH_PANE_ID CONCH_HOME", // never the developer's own session
		"CONCH_SOCKET=/tmp/cwd-inner.sock",            // the staged demo's socket, not ~/.config/conch
		"-listen 127.0.0.1",                           // the gateway is reachable from this computer only
	} {
		if !strings.Contains(string(phone), want) {
			t.Errorf("web/scripts/phone-capture/capture.sh lost %q, which is what keeps it off your own conch", want)
		}
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3: the capture scripts' python is not checked here")
	}
	for _, s := range []string{"web/scripts/phone-capture/shots.py", "web/scripts/tui-capture/topng.py",
		"web/scripts/tui-capture/drive.py", "web/scripts/tui-capture/tojson.py"} {
		cmd := exec.Command(python, "-m", "py_compile", filepath.Join(root, s))
		// Keep the compiled files out of the repository.
		cmd.Env = append(os.Environ(), "PYTHONPYCACHEPREFIX="+t.TempDir())
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("%s does not compile: %v\n%s", s, err, out)
		}
	}
}
