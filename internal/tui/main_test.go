package tui

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// Nothing in this suite may reach out of the process. A test once put its
// own fixture on the developer's clipboard — through the OSC 52 escape
// conch prints, which the terminal running `go test` obligingly honoured —
// and the same would go for a browser window, a desktop notification or
// the bell. Every one of those is a variable in the code precisely so this
// can turn it off for all of them at once; a test that wants to check one
// replaces it again for itself.
func TestMain(m *testing.M) {
	putClipboard = func(text string) { lastClipboard = text }
	openInBrowser = func(url string) error { lastOpened = url; return nil }
	runOutward = func(*exec.Cmd) {}
	startOutward = func(*exec.Cmd) error { return nil }
	ringBell = func() { bells++ }
	signalProcess = func(pid int, sig syscall.Signal) error { signalled = append(signalled, pid); return nil }
	// sandbox-cli's sandboxd is never the developer's, wherever it listens.
	for _, v := range []string{"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "SANDBOX_CONTEXT", "SANDBOXD_TOKEN"} {
		os.Unsetenv(v)
	}
	m.Run()
}

// What the suite's stubs saw last, for tests that care.
var (
	lastClipboard string
	lastOpened    string
	bells         int
	signalled     []int
)
