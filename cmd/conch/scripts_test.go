package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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

// CI runs the suite in pieces (scripts/test.sh's PART), one job each, and
// only all of them together are the whole suite. A part left out of the
// matrix — SHARDS raised without a server-K added, or one deleted — would
// skip a fifth of internal/server's tests with every check still green.
func TestCIRunsEveryPart(t *testing.T) {
	ci, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	shards := regexp.MustCompile(`(?m)^\s+SHARDS: (\d+)\s*$`).FindAllStringSubmatch(string(ci), -1)
	parts := regexp.MustCompile(`(?m)^\s+part: \[([^\]]*)\]\s*$`).FindAllStringSubmatch(string(ci), -1)
	if len(shards) != 1 || len(parts) != 1 {
		t.Fatalf("ci.yml: want one SHARDS and one part list, found %d and %d", len(shards), len(parts))
	}
	n, _ := strconv.Atoi(shards[0][1])
	want := []string{"rest"}
	for k := 1; k <= n; k++ {
		want = append(want, fmt.Sprintf("server-%d", k))
	}
	var got []string
	for _, p := range strings.Split(parts[0][1], ",") {
		got = append(got, strings.TrimSpace(p))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ci.yml runs parts %v with SHARDS=%d; the whole suite is %v", got, n, want)
	}
	if !strings.Contains(string(ci), "PART: ${{ matrix.part }}") || !strings.Contains(string(ci), "run: scripts/test.sh") {
		t.Error("ci.yml's test job no longer hands its part to scripts/test.sh")
	}
}

// What scripts/test.sh refuses before it runs anything: a part it does
// not know, a shard past SHARDS, a SHARDS that is not a count. Each would
// otherwise run nothing, or everything, and pass.
func TestTestScriptRefusesBadParts(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "test.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", script).CombinedOutput(); err != nil {
		t.Fatalf("scripts/test.sh does not parse: %v\n%s", err, out)
	}
	for _, c := range []struct{ shards, part, want string }{
		{"5", "bogus", "all, rest or server-K"},
		{"5", "server-6", "server-K with K from 1 to SHARDS=5"},
		{"5", "server-0", "server-K with K from 1 to SHARDS=5"},
		{"5", "server-", "server-K with K from 1 to SHARDS=5"},
		{"5", "server-x", "server-K with K from 1 to SHARDS=5"},
		{"0", "rest", "a whole number above 0"},
		{"two", "all", "a whole number above 0"},
	} {
		cmd := exec.Command("bash", script)
		cmd.Env = append(os.Environ(), "SHARDS="+c.shards, "PART="+c.part)
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Errorf("SHARDS=%s PART=%s: %v, want exit 2\n%s", c.shards, c.part, err, out)
		}
		if !strings.Contains(string(out), c.want) {
			t.Errorf("SHARDS=%s PART=%s said %q, want %q", c.shards, c.part, out, c.want)
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
		"web/scripts/import-demo/capture.sh", "web/scripts/import-demo/stage.sh", "web/scripts/import-demo/app.sh",
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
	// The import demo is published, so it must never read the developer's
	// own Conductor or reach their own conch: it runs in a scratch HOME on
	// a socket of its own, with an environment its recorder makes from
	// nothing, and its Conductor database is the made-up one.
	demo, err := os.ReadFile(filepath.Join(root, "web/scripts/import-demo/capture.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"unset CONCH_SOCKET CONCH_PANE_ID CONCH_HOME",
		"IE=(env -i HOME=$H CONCH_HOME=$H/.config/conch CONCH_SOCKET=/tmp/conch-import-demo.sock",
		`bash "$T/stage.sh" "$H"`,
		"server stop", // the scratch server it starts is stopped again
	} {
		if !strings.Contains(string(demo), want) {
			t.Errorf("web/scripts/import-demo/capture.sh lost %q, which is what keeps it off your own conch and Conductor", want)
		}
	}
	// Its Conch.app copy is a second app on somebody's desktop beside their
	// own: settings of its own (a bundle id that is not dev.conch.mac),
	// behind their windows and without the global scratch key, and a
	// scratch server reached through CONCH_SOCKET and CONCH_BIN.
	app, err := os.ReadFile(filepath.Join(root, "web/scripts/import-demo/app.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"unset CONCH_SOCKET CONCH_PANE_ID CONCH_HOME",
		"IE=(env -i HOME=$H CONCH_HOME=$H/.config/conch CONCH_SOCKET=$SOCK CONCH_BIN=$WORK/bin/conch",
		"ID=dev.conch.importdemo", `Set :CFBundleIdentifier $ID`,
		`"${IE[@]}" "$APP/Contents/MacOS/Conch" -background YES`,
		`bash "$T/stage.sh" "$H"`, "server stop",
	} {
		if !strings.Contains(string(app), want) {
			t.Errorf("web/scripts/import-demo/app.sh lost %q, which is what keeps its Conch.app off your own", want)
		}
	}
	stage, err := os.ReadFile(filepath.Join(root, "web/scripts/import-demo/stage.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stage), `fakeconductor.py" "$H"`) || strings.Contains(string(stage), "com.conductor.app") {
		t.Error("web/scripts/import-demo/stage.sh must write the made-up Conductor database, never copy a real one")
	}
	rec, err := os.ReadFile(filepath.Join(root, "web/scripts/import-demo/record.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"CONCH_SOCKET": SOCKET`, `SOCKET = "/tmp/conch-import-demo.sock"`, "env=e,"} {
		if !strings.Contains(string(rec), want) {
			t.Errorf("web/scripts/import-demo/record.py lost %q: its commands must run in the environment it makes, not this one", want)
		}
	}
	if strings.Contains(string(rec), "os.environ") {
		t.Error("web/scripts/import-demo/record.py reads os.environ: an inherited CONCH_SOCKET would reach the developer's own conch")
	}

	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3: the capture scripts' python is not checked here")
	}
	for _, s := range []string{"web/scripts/phone-capture/shots.py", "web/scripts/tui-capture/topng.py",
		"web/scripts/tui-capture/drive.py", "web/scripts/tui-capture/tojson.py",
		"web/scripts/import-demo/record.py", "web/scripts/import-demo/fakeconductor.py"} {
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
