package detect

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Process describes the foreground process of a terminal.
type Process struct {
	PID  int      `json:"pid"`
	Name string   `json:"name"` // kernel command name
	Args []string `json:"args,omitempty"`
}

// Names returns the identifiers an agent can be matched by: the kernel
// command name and the base name of argv[0].
func (p Process) Names() []string {
	names := []string{p.Name}
	if len(p.Args) > 0 {
		if b := filepath.Base(p.Args[0]); b != p.Name {
			names = append(names, b)
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
