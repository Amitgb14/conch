package server

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerPID is the process at the other end of a unix socket connection, or
// 0 when the kernel won't say.
func peerPID(nc net.Conn) int {
	uc, ok := nc.(*net.UnixConn)
	if !ok {
		return 0
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0
	}
	pid := 0
	raw.Control(func(fd uintptr) {
		pid, _ = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	})
	return pid
}

// peerUID is the user at the other end of a unix socket connection, or -1
// when the kernel won't say.
func peerUID(nc net.Conn) int {
	uc, ok := nc.(*net.UnixConn)
	if !ok {
		return -1
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1
	}
	uid := -1
	raw.Control(func(fd uintptr) {
		if x, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED); err == nil {
			uid = int(x.Uid)
		}
	})
	return uid
}
