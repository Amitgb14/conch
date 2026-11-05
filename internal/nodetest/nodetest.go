// Package nodetest finds node for the tests that run conch's JavaScript:
// the phone's app (internal/phone) and the settings page
// (internal/configweb).
package nodetest

import (
	"os"
	"os/exec"
	"testing"
)

// Path returns node's path. Without node the test is skipped, since conch
// builds and tests with go alone — except under CI, where a skip would
// pass JavaScript nobody ran, so there it fails.
func Path(t testing.TB) string {
	t.Helper()
	p, err := exec.LookPath("node")
	if err == nil {
		return p
	}
	if Required() {
		t.Fatalf("node is not installed, and CI=%q: the JavaScript tests must run here", os.Getenv("CI"))
		return ""
	}
	t.Skip("node is not installed; the JavaScript tests were not run")
	return ""
}

// Required reports whether a missing node is a failure: under CI, which
// GitHub Actions (and most others) announce by setting CI=true.
func Required() bool {
	switch os.Getenv("CI") {
	case "", "0", "false":
		return false
	}
	return true
}
