package detect

import (
	"bytes"
	"encoding/binary"
	"os"

	"golang.org/x/sys/unix"
)

func processInfo(pid int) (Process, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return Process{}, err
	}
	p := Process{PID: pid, Name: cString(kp.Proc.P_comm[:])}
	// kern.procargs2 is: int32 argc, the exec path, NUL padding, then argv.
	// It fails for processes of other users; the name alone is still useful.
	if raw, err := unix.SysctlRaw("kern.procargs2", pid); err == nil && len(raw) > 4 {
		argc := int(binary.LittleEndian.Uint32(raw[:4]))
		rest := raw[4:]
		if i := bytes.IndexByte(rest, 0); i >= 0 {
			rest = bytes.TrimLeft(rest[i:], "\x00")
		}
		for len(p.Args) < argc && len(rest) > 0 {
			i := bytes.IndexByte(rest, 0)
			if i < 0 {
				p.Args = append(p.Args, string(rest))
				break
			}
			p.Args = append(p.Args, string(rest[:i]))
			rest = rest[i+1:]
		}
	}
	return p, nil
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// ParentPID returns the parent of process pid.
func ParentPID(pid int) (int, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	if kp.Proc.P_pid != int32(pid) { // the kernel answers a pid that is gone with zeros
		return 0, os.ErrNotExist
	}
	return int(kp.Eproc.Ppid), nil
}

// TerminalForeground is the foreground process of the terminal pid runs
// on: what a person sees running there, which is pid itself while its
// shell waits at a prompt. It is read without the terminal's descriptor,
// for a process that does not hold it (conch cloud agent, beside the
// server that does).
func TerminalForeground(pid int) (Process, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return Process{}, err
	}
	if kp.Proc.P_pid != int32(pid) {
		return Process{}, os.ErrNotExist
	}
	pgid := int(kp.Eproc.Tpgid)
	if pgid <= 0 {
		return Process{}, errNoTerminal
	}
	// The group's leader can be gone while the rest of it runs on (`a | b`
	// once a has finished), and the group is still what is in front.
	members, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
	if err != nil {
		return Process{}, err
	}
	for _, m := range members {
		if int(m.Proc.P_pid) == pgid {
			return processInfo(pgid)
		}
	}
	for _, m := range members {
		if m.Proc.P_pid > 0 {
			return processInfo(int(m.Proc.P_pid))
		}
	}
	return Process{}, os.ErrNotExist
}
