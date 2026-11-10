package detect

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
)

// statParent reads the parent's pid from a Linux /proc/PID/stat line:
// "pid (comm) state ppid ...". comm may hold spaces and parentheses, so the
// fields start after the last ')'. Kept apart from process_linux.go so it
// is tested everywhere.
func statParent(stat []byte) (int, error) { return statField(stat, 1) }

// statTPGID reads the foreground process group of the process's
// controlling terminal (tpgid), -1 when it has none.
func statTPGID(stat []byte) (int, error) { return statField(stat, 5) }

// statField is the n-th field after comm: 0 is the state, 1 the parent.
func statField(stat []byte, n int) (int, error) {
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, errors.New("malformed /proc stat")
	}
	fields := strings.Fields(string(stat[i+1:]))
	if len(fields) < n+1 {
		return 0, errors.New("malformed /proc stat")
	}
	return strconv.Atoi(fields[n])
}
