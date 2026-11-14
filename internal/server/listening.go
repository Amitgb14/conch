package server

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
)

// Which TCP ports a pane's programs listen on (pane.listening.v1), so a
// pane running a dev server can say where it is: the Mac app puts the
// port in the pane's header and opens it on a click. It is looked for
// every few seconds, while anybody is connected, across the process tree
// under each pane's program — `npm run dev` listens from a grandchild of
// the shell — and a change goes out as pane.updated.

// listeningEvery is how often the ports are looked for.
const listeningEvery = 4 * time.Second

// processParents lists every process's parent: pid → ppid. A variable so
// tests can give a table of their own.
var processParents = func(ctx context.Context) (map[int]int, error) {
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil, err
	}
	return parsePs(out), nil
}

// listeningByPid says which TCP ports each process listens on. A variable
// so tests can give their own; the real one asks lsof where it is (macOS,
// most Linux machines) and reads /proc on a Linux without it.
var listeningByPid = func(ctx context.Context) (map[int][]int, error) {
	if path, err := exec.LookPath("lsof"); err == nil {
		out, err := exec.CommandContext(ctx, path, "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpn").Output()
		// lsof exits 1 when nothing listens at all, which is an answer.
		if err == nil || len(out) > 0 || isExit1(err) {
			return parseLsof(out), nil
		}
		return nil, err
	}
	if runtime.GOOS == "linux" {
		return procListening("/proc")
	}
	return nil, nil
}

func isExit1(err error) bool {
	ee, ok := err.(*exec.ExitError)
	return ok && ee.ExitCode() == 1
}

// parsePs reads `ps -o pid=,ppid=`.
func parsePs(out []byte) map[int]int {
	parents := map[int]int{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			parents[pid] = ppid
		}
	}
	return parents
}

// parseLsof reads `lsof -Fpn`: a "p<pid>" line, then an "n<address>" line
// for each socket of it, the port after the last colon ("*:4317",
// "127.0.0.1:4317", "[::1]:4317").
func parseLsof(out []byte) map[int][]int {
	ports := map[int][]int{}
	pid := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			i := strings.LastIndexByte(line, ':')
			if pid <= 0 || i < 0 {
				continue
			}
			if port, err := strconv.Atoi(line[i+1:]); err == nil && port > 0 && port < 65536 && !slices.Contains(ports[pid], port) {
				ports[pid] = append(ports[pid], port)
			}
		}
	}
	return ports
}

// procListening finds listening sockets the way lsof would on Linux:
// /proc/net/tcp and tcp6 say which socket inodes listen and on what port,
// and each process's fd directory says which inodes it holds.
func procListening(proc string) (map[int][]int, error) {
	inodes := map[string]int{}
	for _, f := range []string{"net/tcp", "net/tcp6"} {
		b, err := os.ReadFile(filepath.Join(proc, f))
		if err != nil {
			continue
		}
		for inode, port := range parseProcNetTCP(b) {
			inodes[inode] = port
		}
	}
	ports := map[int][]int{}
	if len(inodes) == 0 {
		return ports, nil
	}
	dirs, err := os.ReadDir(proc)
	if err != nil {
		return nil, err
	}
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(proc, d.Name(), "fd"))
		if err != nil {
			continue // not ours to read
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(proc, d.Name(), "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			if port, ok := inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")]; ok && !slices.Contains(ports[pid], port) {
				ports[pid] = append(ports[pid], port)
			}
		}
	}
	return ports, nil
}

// parseProcNetTCP reads /proc/net/tcp: for each socket in the LISTEN state
// (0A), its inode and local port (hex, after the colon of local_address).
func parseProcNetTCP(b []byte) map[string]int {
	out := map[string]int{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != "0A" {
			continue
		}
		i := strings.LastIndexByte(f[1], ':')
		if i < 0 {
			continue
		}
		port, err := strconv.ParseInt(f[1][i+1:], 16, 32)
		if err != nil || port <= 0 {
			continue
		}
		out[f[9]] = int(port)
	}
	return out
}

// portsUnder gathers the ports listened on by root and every process
// under it, lowest first.
func portsUnder(root int, children map[int][]int, ports map[int][]int) []int {
	var out []int
	seen := map[int]bool{}
	stack := []int{root}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		for _, p := range ports[pid] {
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
		stack = append(stack, children[pid]...)
	}
	slices.Sort(out)
	return out
}

// lookListening looks once, and tells clients about each pane whose
// ports changed.
func (s *Server) lookListening(ctx context.Context) {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.panes))
	for _, e := range s.panes {
		entries = append(entries, e)
	}
	anyone := len(s.clients) > 0
	s.mu.Unlock()
	if !anyone || len(entries) == 0 {
		return
	}
	parents, err := processParents(ctx)
	if err != nil {
		return
	}
	byPid, err := listeningByPid(ctx)
	if err != nil {
		return
	}
	children := map[int][]int{}
	for pid, ppid := range parents {
		if pid != ppid {
			children[ppid] = append(children[ppid], pid)
		}
	}
	s.bgMu.Lock()
	s.lastPorts = byPid
	s.bgMu.Unlock()
	var procs map[int]psRow
	looked, lookOK := false, false
	for _, e := range entries {
		info := e.info()
		var now []int
		if info.PID > 0 {
			now = portsUnder(info.PID, children, byPid)
		}
		// What an agent that is not working left running: looked for
		// only when there is such an agent, since it reads every
		// process's command line.
		var jobs []proto.BackgroundJob
		keepJobs := false
		if a := info.Agent; a != nil && a.State != detect.StateWorking && info.PID > 0 {
			if !looked {
				looked = true
				procs, lookOK = s.processesSeen(ctx)
			}
			if lookOK {
				jobs = agentJobs(info.PID, e.explain().Process.PID, s.manifests[a.Name], procs, byPid, time.Now())
			} else {
				keepJobs = true // a failed look changes nothing
			}
		}
		e.mu.Lock()
		same := slices.Equal(e.listening, now)
		e.listening = now
		if !keepJobs {
			jobs = steadySince(e.jobs, jobs)
			same = same && jobsKey(e.jobs) == jobsKey(jobs)
			e.jobs = jobs
		}
		e.mu.Unlock()
		if !same && s.alive(e) {
			s.reshown(e)
		}
	}
}

// runListening looks for listening ports until the server stops.
func (s *Server) runListening(quit <-chan struct{}) {
	t := time.NewTicker(listeningEvery)
	defer t.Stop()
	for {
		select {
		case <-quit:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			s.lookListening(ctx)
			cancel()
		}
	}
}
