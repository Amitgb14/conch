package detect

import (
	"bytes"
	"encoding/binary"

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
