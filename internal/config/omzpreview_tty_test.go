package config

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// underTTY marks the copy of the test binary run inside a terminal.
const underTTY = "CONCH_TEST_OMZ_UNDER_TTY"

// A theme is told the terminal is promptCols wide whatever terminal conch
// itself runs in. zsh asks its controlling terminal for the size and
// believes that over COLUMNS, so in a 120-column pane — where conch's own
// checks run the suite — every prompt that prints its width came out 120
// wide and TestOMZPrompt failed there and nowhere else. This runs it the
// way a pane does: the test binary in a session of its own, its
// controlling terminal 120 columns wide.
func TestOMZPromptIgnoresTheTerminal(t *testing.T) {
	if os.Getenv(underTTY) != "" {
		t.Skip("already inside the terminal")
	}
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("no zsh")
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^TestOMZPrompt$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), underTTY+"=1")
	// StartWithSize makes the terminal the child's controlling one.
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 40})
	if err != nil {
		t.Skipf("no pty here: %v", err)
	}
	defer ptmx.Close()
	var out bytes.Buffer
	read := make(chan struct{})
	go func() {
		io.Copy(&out, ptmx) //nolint:errcheck // ends with EIO once the child is gone
		close(read)
	}()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err = <-waited:
	case <-time.After(2 * time.Minute):
		cmd.Process.Kill() //nolint:errcheck
		t.Fatal("TestOMZPrompt inside a terminal never finished")
	}
	ptmx.Close()
	<-read
	got := out.String()
	if err != nil || !strings.Contains(got, "--- PASS: TestOMZPrompt") {
		t.Fatalf("TestOMZPrompt inside a 120-column terminal: %v\n%s", err, got)
	}
}
