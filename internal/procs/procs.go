// Package procs reads what processes on this computer use — memory and CPU
// time from ps, the whole machine's memory and CPU from the system — and
// adds them up by process tree. The TUI's resource monitor and the phone
// gateway's GET /api/resources both count conch this way: a pane is its
// program and everything that program started.
package procs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Proc is one process from ps.
type Proc struct {
	PID, PPID int
	RSS       int64         // resident memory, KiB
	CPU       time.Duration // CPU time used so far
	Comm      string
}

// Sample is every process on this computer at one moment.
type Sample struct {
	At    time.Time
	Procs map[int]Proc
	Err   error
}

// RunPS lists every process; tests replace it.
var RunPS = func(ctx context.Context) ([]byte, error) {
	// The same fields exist in procps (Linux) and BSD ps (macOS); comm is
	// last because it may contain spaces.
	return exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,rss=,time=,comm=").Output()
}

// Parse reads ps output, skipping lines it can't parse.
func Parse(out []byte) map[int]Proc {
	procs := map[int]Proc{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		rss, err3 := strconv.ParseInt(f[2], 10, 64)
		cpu, ok := ParseCPUTime(f[3])
		if err1 != nil || err2 != nil || err3 != nil || !ok || pid < 0 || rss < 0 {
			continue
		}
		procs[pid] = Proc{PID: pid, PPID: ppid, RSS: rss, CPU: cpu, Comm: strings.Join(f[4:], " ")}
	}
	return procs
}

// ParseCPUTime reads ps's time column: [DD-][HH:]MM:SS[.ss]. Linux prints
// 00:01:02, macOS 1:02.34 (minutes may pass 59 there).
func ParseCPUTime(s string) (time.Duration, bool) {
	var days int64
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.ParseInt(d, 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	sec, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil || sec < 0 || math.IsInf(sec, 0) || math.IsNaN(sec) {
		return 0, false
	}
	total := sec + float64(days)*86400
	for i, mult := len(parts)-2, 60.0; i >= 0; i, mult = i-1, mult*60 {
		n, err := strconv.ParseInt(parts[i], 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		total += float64(n) * mult
	}
	// Whole milliseconds, so float noise never shows up as CPU use.
	return time.Duration(math.Round(total*1000)) * time.Millisecond, true
}

// Take runs ps, giving it timeout. A ps that fails but still prints its
// list (a process it couldn't read) is as good as one that didn't.
func Take(timeout time.Duration) Sample {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := RunPS(ctx)
	s := Sample{At: time.Now(), Procs: Parse(out)}
	if len(s.Procs) == 0 {
		if err == nil {
			err = fmt.Errorf("ps listed no processes")
		}
		s.Err = err
	}
	return s
}

// Children maps each pid to its children, in pid order.
func Children(procs map[int]Proc) map[int][]int {
	children := map[int][]int{}
	for _, p := range procs {
		if p.PPID != p.PID {
			children[p.PPID] = append(children[p.PPID], p.PID)
		}
	}
	for _, c := range children {
		slices.Sort(c)
	}
	return children
}

// Tree lists root and its descendants that aren't claimed yet, claiming
// them. Each pid is taken once, so a parent loop in ps's list (a pid reused
// while ps was reading) can't send it round forever.
func Tree(root int, children map[int][]int, procs map[int]Proc, claimed map[int]bool) []int {
	if _, ok := procs[root]; !ok || claimed[root] {
		return nil
	}
	var out []int
	stack := []int{root}
	claimed[root] = true
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		out = append(out, pid)
		for _, c := range children[pid] {
			if !claimed[c] {
				claimed[c] = true
				stack = append(stack, c)
			}
		}
	}
	return out
}

// RSS adds up the resident memory of pids in s, in KiB.
func (s *Sample) RSS(pids []int) int64 {
	var rss int64
	for _, pid := range pids {
		rss += s.Procs[pid].RSS
	}
	return rss
}

// Used is the CPU time pids spent between prev and cur. A process missing
// from prev, or whose time went down (its pid was reused), counts all its
// time as new.
func Used(prev, cur *Sample, pids []int) time.Duration {
	var used time.Duration
	for _, pid := range pids {
		used += Spent(prev, cur, pid)
	}
	return used
}

// Spent is Used for one process.
func Spent(prev, cur *Sample, pid int) time.Duration {
	p := cur.Procs[pid]
	if prev != nil {
		if old, ok := prev.Procs[pid]; ok && old.CPU <= p.CPU {
			return p.CPU - old.CPU
		}
	}
	return p.CPU
}

// Elapsed is the time between two samples, when CPU use can be worked out
// from them: there is an earlier sample, it did not fail, and the clock
// went forward.
func Elapsed(prev, cur *Sample) (time.Duration, bool) {
	if prev == nil || prev.Err != nil || cur == nil {
		return 0, false
	}
	d := cur.At.Sub(prev.At)
	return d, d > 0
}
