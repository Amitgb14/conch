//go:build !linux

package sysstat

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// psProcs lists every process with ps, where there is no /proc to read.
func psProcs(ctx context.Context) ([]Proc, error) {
	out, err := exec.CommandContext(ctx, "ps", psArgs...).Output()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	lines := parsePS(out)
	procs := make([]Proc, len(lines))
	for i, l := range lines {
		procs[i] = Proc{PID: l.pid, PPID: l.ppid, Name: baseName(l.comm), User: l.user, RSS: l.rss, CPU: l.cpu,
			Started: now.Add(-l.elapsed).Truncate(time.Second)}
	}
	return procs, nil
}

// psCommands is each pid's whole command line, asked of ps for the few
// a window lists rather than for every process.
func psCommands(ctx context.Context, pids []int) map[int]string {
	if len(pids) == 0 {
		return nil
	}
	list := make([]string, len(pids))
	for i, p := range pids {
		list[i] = strconv.Itoa(p)
	}
	// ps exits 1 when one of them has ended since; what it printed holds.
	out, _ := exec.CommandContext(ctx, "ps", "-o", "pid=,args=", "-p", strings.Join(list, ",")).Output()
	return parseArgs(out)
}
