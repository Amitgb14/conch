package server

import (
	"os"
	"strings"
)

// dropMallocStackLogging takes macOS's malloc stack logging variables out
// of the server's environment, which every process it starts inherits — a
// helper conch, git, gh, a pane's shell. Started with one set (by a
// debugging session, or a terminal launched from one), each of those
// printed "MallocStackLogging: can't turn off malloc stack logging because
// it was not enabled" on its stderr, which is server.log: thousands of
// lines on one Mac (#199). Nothing conch starts wants it.
func dropMallocStackLogging() {
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "MallocStackLogging") {
			os.Unsetenv(name)
		}
	}
}
