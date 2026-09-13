package detect

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Process describes the foreground process of a terminal.
type Process struct {
	PID  int      `json:"pid"`
	Name string   `json:"name"` // kernel command name
	Args []string `json:"args,omitempty"`
}

// interpreters run agents written as scripts; the script names the agent.
var interpreters = map[string]bool{"node": true, "bun": true, "deno": true, "python": true, "python3": true}

// Names returns the identifiers an agent can be matched by: the kernel
// command name, the base name of argv[0], and for interpreters the script
// (so `node /usr/local/bin/gemini` matches gemini).
func (p Process) Names() []string {
	names := []string{p.Name}
	if len(p.Args) > 0 {
		if b := filepath.Base(p.Args[0]); b != p.Name {
			names = append(names, b)
		}
		if (interpreters[p.Name] || interpreters[filepath.Base(p.Args[0])]) && len(p.Args) > 1 {
			for _, a := range p.Args[1:] {
				if !strings.HasPrefix(a, "-") {
					names = append(names, strings.TrimSuffix(filepath.Base(a), filepath.Ext(a)))
					break
				}
			}
		}
	}
	return names
}

// ForegroundProcess returns the foreground process group leader of the
// terminal whose master side is ptmx.
func ForegroundProcess(ptmx *os.File) (Process, error) {
	pgid, err := unix.IoctlGetInt(int(ptmx.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return Process{}, err
	}
	return processInfo(pgid)
}
