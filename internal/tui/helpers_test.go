package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// a4Shell runs the editor script with a stand-in editor that prints the
// arguments it was given, so the test reads what a real editor would have
// been told without running one.
func a4Shell(t *testing.T, editor, file, line string) string {
	t.Helper()
	dir := t.TempDir()
	name := editor
	flags := ""
	if i := strings.Index(editor, " "); i >= 0 {
		name, flags = editor[:i], editor[i:]
	}
	bin := filepath.Join(dir, filepath.Base(name))
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-lc", editAtLineScript, "sh", file, line)
	cmd.Env = append(os.Environ(), "VISUAL=", "EDITOR="+bin+flags, "PATH="+dir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v: %s", editor, err, out)
	}
	return string(out)
}
