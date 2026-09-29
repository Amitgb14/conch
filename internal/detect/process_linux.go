package detect

import (
	"os"
	"strconv"
	"strings"
)

func processInfo(pid int) (Process, error) {
	dir := "/proc/" + strconv.Itoa(pid)
	comm, err := os.ReadFile(dir + "/comm")
	if err != nil {
		return Process{}, err
	}
	p := Process{PID: pid, Name: strings.TrimSpace(string(comm))}
	if cmdline, err := os.ReadFile(dir + "/cmdline"); err == nil {
		p.Args = strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
	}
	return p, nil
}

// ParentPID returns the parent of process pid.
func ParentPID(pid int) (int, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, err
	}
	return statParent(stat)
}
