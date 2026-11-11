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

// TerminalForeground is the foreground process of the terminal pid runs
// on: what a person sees running there, which is pid itself while its
// shell waits at a prompt. It is read without the terminal's descriptor,
// for a process that does not hold it (conch cloud agent, beside the
// server that does).
func TerminalForeground(pid int) (Process, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return Process{}, err
	}
	pgid, err := statTPGID(stat)
	if err != nil {
		return Process{}, err
	}
	if pgid <= 0 {
		return Process{}, errNoTerminal
	}
	if p, err := processInfo(pgid); err == nil {
		return p, nil
	}
	return groupMember(pgid)
}

// groupMember is a process of group pgid: the group's leader can be gone
// while the rest of it runs on (`a | b` once a has finished), and the
// group is still what is in front of the terminal.
func groupMember(pgid int) (Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return Process{}, err
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		if g, err := statPGRP(stat); err == nil && g == pgid {
			if p, err := processInfo(pid); err == nil {
				return p, nil
			}
		}
	}
	return Process{}, os.ErrNotExist
}
