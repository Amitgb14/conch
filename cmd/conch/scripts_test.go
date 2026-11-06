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
