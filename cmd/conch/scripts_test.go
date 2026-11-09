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
	scripts := []string{"scripts/release.sh", "scripts/brew-formula.sh", "macos/bundle.sh", "macos/release.sh", "macos/notarize.sh"}
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
		"--options runtime", `"$here/notarize.sh" "$stage/notarize.zip" "$profile"`, "stapler staple", "Notarized Developer ID"} {
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

// notarize.sh waits for Apple's answer itself, because `notarytool submit
// --wait` gave up without a status while Apple was slow and the release
// read that as a rejection. Driven here by a fake xcrun: a submission that
// is in progress (or unanswered) for a while and then Accepted passes; one
// Apple calls Invalid, one with no id, and one never answered fail.
func TestNotarizeWaitsForApple(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "macos", "notarize.sh"))
	if err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
dir=$(dirname "$0")
echo "$*" >>"$dir/calls"
case "$2" in
submit) cat "$dir/submit" ;;
info)
	n=$(($(cat "$dir/n" 2>/dev/null || echo 0) + 1)); echo $n >"$dir/n"
	sed -n "${n}p" "$dir/answers" | grep . || tail -1 "$dir/answers" ;;
esac
`
	submitted := "Conducting pre-submission checks for x.zip\nSubmission ID received\n  id: abc-123\n"
	cases := []struct {
		name, submit, answers, wait string
		ok                          bool
		want                        string
	}{
		{"accepted after a wait", submitted, "  status: In Progress\nError: network\n  status: In Progress\n  status: Accepted\n", "", true, "status: Accepted"},
		{"invalid", submitted, "  status: In Progress\n  status: Invalid\n", "", false, "(Invalid) — xcrun notarytool log abc-123"},
		{"rejected", submitted, "  status: Rejected\n", "", false, "(Rejected)"},
		{"no id", "Error: HTTP status code: 401\n", "  status: Accepted\n", "", false, "gave no submission id"},
		{"never answered", submitted, "  status: In Progress\n", "0", false, "(In Progress)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range map[string]string{"xcrun": fake, "submit": c.submit, "answers": c.answers} {
				mode := os.FileMode(0o644)
				if name == "xcrun" {
					mode = 0o755
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("sh", script, "x.zip", "prof")
			wait := c.wait
			if wait == "" {
				wait = "60"
			}
			cmd.Env = append(os.Environ(), "PATH="+dir+":/usr/bin:/bin", "CONCH_NOTARY_POLL=0", "CONCH_NOTARY_WAIT="+wait)
			out, err := cmd.CombinedOutput()
			if (err == nil) != c.ok {
				t.Fatalf("exit error %v, want ok=%v\n%s", err, c.ok, out)
			}
			if !strings.Contains(string(out), c.want) {
				t.Errorf("output lacks %q:\n%s", c.want, out)
			}
			calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
			if strings.Contains(string(calls), "--wait") {
				t.Errorf("submitted with --wait, which gives up silently:\n%s", calls)
			}
			if c.ok && !strings.Contains(string(calls), "notarytool info abc-123 --keychain-profile prof") {
				t.Errorf("never asked Apple about the submission:\n%s", calls)
			}
		})
	}
}
