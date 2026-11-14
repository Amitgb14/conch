package server

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// What an agent left running when it stopped working (agent.background.v1):
// a background shell from Claude Code's run_in_background, a dev server, a
// test watcher. An agent that looks finished can still be changing files,
// holding a port or spending money, and nothing said so. It is found on
// the same look as the listening ports, from the process tree: what runs
// below the agent's own process, less the agent itself and the helpers it
// keeps for its own sake, and less anything only seconds old — a `git
// status` it just ran is not background work, and would make the count
// flicker.

// jobMinAge is how long a child must have run to count.
const jobMinAge = 5 * time.Second

// jobCommandMax is how much of a job's command is kept, in runes.
const jobCommandMax = 160

// psRow is one process from processTable.
type psRow struct {
	pid, ppid int
	since     time.Time // when it started; zero when ps could not say
	args      string
}

// processTable lists every process with its parent, its start and its
// command line. A variable so tests can give a table of their own.
var processTable = func(ctx context.Context) (map[int]psRow, error) {
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,etime=,args=").Output()
	if err != nil {
		return nil, err
	}
	return parsePsTable(out, time.Now()), nil
}

// parsePsTable reads `ps -o pid=,ppid=,etime=,args=`. The command keeps
// its own spacing, since it is shown.
func parsePsTable(out []byte, now time.Time) map[int]psRow {
	rows := map[int]psRow{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		rest := sc.Text()
		var f [3]string
		ok := true
		for i := range f {
			rest = strings.TrimLeft(rest, " \t")
			j := strings.IndexAny(rest, " \t")
			if j < 0 {
				j = len(rest)
			}
			f[i], rest = rest[:j], rest[j:]
			if f[i] == "" {
				ok = false
			}
		}
		if !ok {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		r := psRow{pid: pid, ppid: ppid, args: strings.TrimSpace(rest)}
		if d, ok := parseEtime(f[2]); ok {
			r.since = now.Add(-d)
		}
		rows[pid] = r
	}
	return rows
}

// parseEtime reads ps's elapsed time, [[dd-]hh:]mm:ss.
func parseEtime(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	var days int
	if i := strings.IndexByte(s, '-'); i >= 0 {
		d, err := strconv.Atoi(s[:i])
		if err != nil || d < 0 {
			return 0, false
		}
		days, s = d, s[i+1:]
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	total := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || p[0] == '-' || p[0] == '+' {
			return 0, false
		}
		total = total*60 + n
	}
	return time.Duration(days)*24*time.Hour + time.Duration(total)*time.Second, true
}

// defaultHelpers are programs any agent keeps below it for itself, by name.
var defaultHelpers = []string{
	"caffeinate", // Claude Code keeps the Mac awake while it works
	"conch",      // conch's own hooks and status line
	"gopls", "rust-analyzer", "clangd", "jdtls", "sourcekit-lsp",
}

// isHelper says whether p is something the agent keeps for itself: an MCP
// server, a language server, a name in the manifest's helpers.
func isHelper(m *detect.Manifest, p detect.Process, args string) bool {
	for _, n := range p.Names() {
		if slices.Contains(defaultHelpers, n) || slices.Contains(m.Helpers, n) ||
			strings.Contains(n, "language-server") || strings.Contains(n, "langserver") || strings.HasSuffix(n, "-lsp") {
			return true
		}
	}
	a := strings.ToLower(args)
	return strings.Contains(a, "modelcontextprotocol") || strings.Contains(a, "mcp")
}

// processOf is a row as detect matches processes: its name is the base of
// the command's first word.
func processOf(r psRow) detect.Process {
	args := strings.Fields(r.args)
	p := detect.Process{PID: r.pid, Args: args}
	if len(args) > 0 {
		p.Name = filepath.Base(args[0])
	}
	return p
}

// agentJobs lists what the agent in the pane whose program is root has
// running in the background. hint is the pid the detector last saw the
// agent as; when that is not the agent any more it is looked for below
// root by name. A job is a child of the agent's process (or of a copy of
// the agent below it, as Codex runs its native binary under a script),
// with the ports it or its children listen on.
func agentJobs(root, hint int, m *detect.Manifest, procs map[int]psRow, ports map[int][]int, now time.Time) []proto.BackgroundJob {
	if m == nil || len(procs) == 0 {
		return nil
	}
	children := map[int][]int{}
	for pid, r := range procs {
		if pid != r.ppid {
			children[r.ppid] = append(children[r.ppid], pid)
		}
	}
	for _, c := range children {
		slices.Sort(c)
	}
	isAgent := func(pid int) bool {
		r, ok := procs[pid]
		return ok && m.MatchProcess(processOf(r))
	}
	under := func(pid int) bool { // pid is root or below it
		for seen := 0; seen < len(procs)+1; seen++ {
			if pid == root {
				return true
			}
			r, ok := procs[pid]
			if !ok || r.ppid == pid {
				return false
			}
			pid = r.ppid
		}
		return false
	}
	agent := 0
	if hint > 0 && isAgent(hint) && under(hint) {
		agent = hint
	} else {
		queue, seen := []int{root}, map[int]bool{}
		for len(queue) > 0 && agent == 0 {
			pid := queue[0]
			queue = queue[1:]
			if seen[pid] {
				continue
			}
			seen[pid] = true
			if isAgent(pid) {
				agent = pid
			}
			queue = append(queue, children[pid]...)
		}
	}
	if agent == 0 {
		return nil
	}
	var jobs []proto.BackgroundJob
	seen := map[int]bool{}
	var walk func(pid int)
	walk = func(pid int) {
		if seen[pid] {
			return
		}
		seen[pid] = true
		for _, c := range children[pid] {
			r := procs[c]
			p := processOf(r)
			switch {
			case m.MatchProcess(p):
				walk(c)
			case isHelper(m, p, r.args), r.since.IsZero(), now.Sub(r.since) < jobMinAge, seen[c]:
			default:
				jobs = append(jobs, proto.BackgroundJob{PID: c, Command: jobCommand(r.args), Since: r.since,
					Ports: portsUnder(c, children, ports)})
			}
		}
	}
	walk(agent)
	if len(jobs) == 0 {
		return nil
	}
	return jobs
}

// jobCommand is a job's command as somebody would recognise it. Claude
// Code runs a command as `zsh -c -l source <snapshot> && … && eval
// '<command>' < /dev/null && pwd -P >| <file>`, of which only the quoted
// part is what was asked for.
func jobCommand(args string) string {
	args = strings.TrimSpace(args)
	if i := strings.Index(args, "eval '"); i >= 0 {
		if cmd, ok := shellQuoted(args[i+len("eval "):]); ok {
			args = cmd
		}
	}
	args = gitx.Printable(args)
	if r := []rune(args); len(r) > jobCommandMax {
		args = string(r[:jobCommandMax-1]) + "…"
	}
	return args
}

// shellQuoted reads one single-quoted shell word at the start of s, with
// '\” standing for a quote inside it.
func shellQuoted(s string) (string, bool) {
	if !strings.HasPrefix(s, "'") {
		return "", false
	}
	var b strings.Builder
	s = s[1:]
	for {
		i := strings.IndexByte(s, '\'')
		if i < 0 {
			return "", false
		}
		b.WriteString(s[:i])
		s = s[i+1:]
		if !strings.HasPrefix(s, "\\''") {
			return b.String(), true
		}
		b.WriteByte('\'')
		s = s[3:]
	}
}

// jobsKey says whether two lists of jobs would be shown differently.
func jobsKey(jobs []proto.BackgroundJob) string {
	var b strings.Builder
	for _, j := range jobs {
		fmt.Fprintf(&b, "%d %q %d %v;", j.PID, j.Command, j.Since.Unix(), j.Ports)
	}
	return b.String()
}

// steadySince keeps the start a job was first given: ps says how long a
// process has run, to the second, so the start worked out from it moves
// by a second between looks and every look would read as a change.
func steadySince(before, now []proto.BackgroundJob) []proto.BackgroundJob {
	for i := range now {
		for _, b := range before {
			if b.PID == now[i].PID && b.Command == now[i].Command {
				d := now[i].Since.Sub(b.Since)
				if d > -2*time.Second && d < 2*time.Second {
					now[i].Since = b.Since
				}
			}
		}
	}
	return now
}

// processesSeen reads the process table, giving a process whose start ps
// could not say the time this server first saw it — a later start, never
// an earlier one, so it errs towards not counting a child yet.
func (s *Server) processesSeen(ctx context.Context) (map[int]psRow, bool) {
	procs, err := processTable(ctx)
	if err != nil {
		return nil, false
	}
	now := time.Now()
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	if s.procSeen == nil {
		s.procSeen = map[int]time.Time{}
	}
	for pid, r := range procs {
		if r.since.IsZero() {
			if _, ok := s.procSeen[pid]; !ok {
				s.procSeen[pid] = now
			}
			r.since = s.procSeen[pid]
			procs[pid] = r
		}
	}
	for pid := range s.procSeen {
		if _, ok := procs[pid]; !ok {
			delete(s.procSeen, pid)
		}
	}
	return procs, true
}

// stopJob ends one job an agent left running, and what it started: only
// one the last look named for that pane, and only while it is still below
// the pane's program, so a pid that has since ended and been reused by
// something else is never signalled.
func (s *Server) stopJob(p proto.AgentBackgroundStopParams) *proto.Error {
	e, perr := s.get(p.ID)
	if perr != nil {
		return perr
	}
	e.mu.Lock()
	known := p.PID > 0 && slices.ContainsFunc(e.jobs, func(j proto.BackgroundJob) bool { return j.PID == p.PID })
	e.mu.Unlock()
	if !known {
		return proto.Errorf(proto.ErrBadRequest, "pid %d is not running in the background of %s", p.PID, p.ID)
	}
	root := e.p.Info().PID
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	procs, err := processTable(ctx)
	if err != nil {
		return proto.Errorf(proto.ErrInternal, "listing processes: %v", err)
	}
	below := false
	for pid, hops := p.PID, 0; hops <= len(procs); hops++ {
		r, ok := procs[pid]
		if !ok || r.ppid == pid {
			break
		}
		if r.ppid == root {
			below = true
			break
		}
		pid = r.ppid
	}
	if !below {
		s.dropJob(e, p.PID)
		return proto.Errorf(proto.ErrBadRequest, "pid %d has already ended", p.PID)
	}
	children := map[int][]int{}
	for pid, r := range procs {
		if pid != r.ppid {
			children[r.ppid] = append(children[r.ppid], pid)
		}
	}
	// The job and everything under it: a dev server run through npm is a
	// shell, npm and node, and ending the shell alone can leave node.
	tree, seen := []int{p.PID}, map[int]bool{p.PID: true}
	for i := 0; i < len(tree); i++ {
		for _, c := range children[tree[i]] {
			if !seen[c] {
				seen[c] = true
				tree = append(tree, c)
			}
		}
	}
	for i := len(tree) - 1; i >= 0; i-- {
		syscall.Kill(tree[i], syscall.SIGTERM)
	}
	log.Printf("pane %s: stopped background job %d (%d processes)", p.ID, p.PID, len(tree))
	s.dropJob(e, p.PID)
	return nil
}

// dropJob forgets a job without waiting for the next look.
func (s *Server) dropJob(e *entry, pid int) {
	e.mu.Lock()
	e.jobs = slices.DeleteFunc(slices.Clone(e.jobs), func(j proto.BackgroundJob) bool { return j.PID == pid })
	e.mu.Unlock()
	if s.alive(e) {
		s.reshown(e)
	}
}

// jobsAtTurnEnd looks for an agent's jobs as it stops working, rather than
// at the next look a few seconds on, so the update that says it finished —
// and the notification a client makes of it — can say what it left
// running. The ports are the last look's; the next one brings them up to
// date. The caller holds e.evalMu.
func (s *Server) jobsAtTurnEnd(e *entry, agent string) {
	s.mu.Lock()
	anyone := len(s.clients) > 0
	s.mu.Unlock()
	root := e.p.Info().PID
	if !anyone || root <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	procs, ok := s.processesSeen(ctx)
	if !ok {
		return
	}
	s.bgMu.Lock()
	ports := s.lastPorts
	s.bgMu.Unlock()
	jobs := agentJobs(root, e.explain().Process.PID, s.manifests[agent], procs, ports, time.Now())
	e.mu.Lock()
	e.jobs = steadySince(e.jobs, jobs)
	e.mu.Unlock()
}
