package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
)

// Hooks are the person's actions run on events (config.HookEvents): the
// server is where the events are seen, and it is there with no TUI open.
// Each run is a short-lived job of its own — never in the way of what
// fired it, stopped at its timeout with everything it started, its output
// kept in a log of bounded size, and a failure said in the activity feed
// where the autopilot's notes are, since that is where somebody looks for
// what happened while they were away.

const (
	hookStateFile = "hooks.json"
	hookLogDir    = "hooks/logs"
	// hookLogMax is how much of one run's output is kept: a hook that
	// prints megabytes must not fill the disk a run at a time.
	hookLogMax = 1 << 20
	// hookLogsKept is how many runs' logs are kept, newest.
	hookLogsKept = 200
	// hookKillWait is how long a run stopped at its timeout has to let go
	// of its output before it is given up on.
	hookKillWait = 2 * time.Second
)

// hookEvent is one event and what is known about it, handed to a hook in
// its environment.
type hookEvent struct {
	Name      string
	ProjectID string
	Project   string // its name
	Root      string
	Branch    string
	Worktree  string
	Pane      string
	Agent     string
	Message   string
	PR        int
	PRURL     string
	PRTitle   string
	Error     string
}

// env is the event as variables. CONCH_PANE, not CONCH_PANE_ID: the
// latter tells conch's own commands they run inside that pane, and an
// agent's hook report sent from a hook would land on the agent's pane.
func (ev hookEvent) env(action string) []string {
	kv := []string{
		"CONCH_EVENT=" + ev.Name,
		"CONCH_ACTION=" + action,
		"CONCH_PROJECT=" + ev.Project,
		"CONCH_PROJECT_ID=" + ev.ProjectID,
		"CONCH_ROOT=" + ev.Root,
		"CONCH_BRANCH=" + ev.Branch,
		"CONCH_WORKTREE=" + ev.Worktree,
		"CONCH_PANE=" + ev.Pane,
		"CONCH_AGENT=" + ev.Agent,
		"CONCH_MESSAGE=" + ev.Message,
		"CONCH_PR_URL=" + ev.PRURL,
		"CONCH_PR_TITLE=" + ev.PRTitle,
		"CONCH_ERROR=" + ev.Error,
	}
	pr := ""
	if ev.PR > 0 {
		pr = strconv.Itoa(ev.PR)
	}
	return append(kv, "CONCH_PR="+pr)
}

// dir is where a hook for the event runs: the worktree, else the project,
// else home.
func (ev hookEvent) dir() string {
	for _, d := range []string{ev.Worktree, ev.Root} {
		if st, err := os.Stat(d); d != "" && err == nil && st.IsDir() {
			return d
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

type hooks struct {
	configDir string
	sockPath  string
	// load reads the actions; config.Load but in tests.
	load func() (config.Config, error)
	// note says a failure in the activity feed.
	note func(proto.AutopilotNote)

	mu      sync.Mutex
	saved   hookFile
	seq     int
	running map[int]*exec.Cmd
	stopped bool
	wg      sync.WaitGroup
	// ran is told every run's end, in tests.
	ran func(hookRun)
}

// hookFile is hooks.json: what each project's pull requests were at the
// last read, so a pull request opened or merged is an event once —
// across a restart or a reload too.
type hookFile struct {
	Seen map[string]bool   `json:"seen,omitempty"`
	PRs  map[string]string `json:"prs,omitempty"` // project\x00branch → "number STATE"
}

// hookRun is how one run ended.
type hookRun struct {
	Action, Event string
	Log           string
	Exit          int
	TimedOut      bool
	Stopped       bool // by the server stopping or reloading
	Cut           int64
	Err           error
}

func loadHooks(configDir, sockPath string) *hooks {
	h := &hooks{configDir: configDir, sockPath: sockPath, load: hookActions, running: map[int]*exec.Cmd{}}
	if testing.Testing() {
		// A server test that makes a worktree, or sees an agent finish,
		// would otherwise run the hooks of whatever config.toml it found —
		// the developer's own, where a test forgot CONCH_HOME. A test of
		// hooks sets load itself.
		h.load = func() (config.Config, error) { return config.Config{}, nil }
	}
	raw, err := os.ReadFile(filepath.Join(configDir, hookStateFile))
	if err == nil {
		if err := json.Unmarshal(raw, &h.saved); err != nil {
			log.Printf("hooks: %s: %v", hookStateFile, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("hooks: %v", err)
	}
	orNew(&h.saved.Seen)
	orNew(&h.saved.PRs)
	return h
}

// hookActions is config.toml's actions and, after them, the hook files
// installed from a registry (integration.KindHook).
func hookActions() (config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, err
	}
	files, _ := integration.Hooks()
	cfg.Actions = append(cfg.Actions, files...)
	return cfg, nil
}

func (h *hooks) saveLocked() {
	raw, err := json.MarshalIndent(h.saved, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(h.configDir, hookStateFile)
	if err := os.WriteFile(path+".tmp", raw, 0o600); err != nil {
		log.Printf("hooks: %v", err)
		return
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		log.Printf("hooks: %v", err)
	}
}

// fire runs every action on the event, in the background: the config is
// read there too, so nothing the event's own goroutine holds waits on a
// file or a hook.
func (h *hooks) fire(ev hookEvent) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.wg.Add(1)
	h.mu.Unlock()
	goSafe("hook "+ev.Name, func() {
		defer h.wg.Done()
		cfg, err := h.load()
		if err != nil {
			log.Printf("hooks: %s: config: %v", ev.Name, err)
			return
		}
		acts := cfg.ActionsFor(ev.Name)
		var wg sync.WaitGroup
		for _, a := range acts {
			wg.Add(1)
			goSafe("hook "+a.Name, func() {
				defer wg.Done()
				h.run(a, ev)
			})
		}
		wg.Wait()
	})
}

// run is one action on one event, start to end.
func (h *hooks) run(a config.Action, ev hookEvent) {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.seq++
	seq := h.seq
	h.mu.Unlock()

	res := hookRun{Action: a.Name, Event: ev.Name}
	defer func() {
		if f := h.ran; f != nil {
			f(res)
		}
	}()
	dir := filepath.Join(h.configDir, hookLogDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		res.Err = err
		h.failed(a, ev, res, a.HookTimeout())
		return
	}
	res.Log = filepath.Join(dir, fmt.Sprintf("%s-%04d-%s.log", time.Now().Format("20060102-150405"), seq%10000, slug(a.Name)))
	f, err := os.OpenFile(res.Log, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		res.Err = err
		h.failed(a, ev, res, a.HookTimeout())
		return
	}
	defer f.Close()
	start := time.Now()
	fmt.Fprintf(f, "conch: %s on %s at %s\n\n", a.Name, ev.Name, start.Format(time.RFC3339))

	ctx, cancel := context.WithTimeout(context.Background(), a.HookTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", a.Run)
	cmd.Dir = ev.dir()
	cmd.Env = append(hookBaseEnv(h.sockPath), ev.env(a.Name)...)
	out := &cappedWriter{w: f, max: hookLogMax}
	cmd.Stdout, cmd.Stderr = out, out
	// Its own process group, so a timeout stops what it started too: a
	// shell killed alone leaves its sleep holding the output open.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = hookKillWait

	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	if err := cmd.Start(); err != nil {
		h.mu.Unlock()
		res.Err = err
		fmt.Fprintf(f, "conch: could not start: %v\n", err)
		h.failed(a, ev, res, a.HookTimeout())
		return
	}
	h.running[seq] = cmd
	h.mu.Unlock()

	err = cmd.Wait()
	h.mu.Lock()
	delete(h.running, seq)
	res.Stopped = h.stopped
	h.mu.Unlock()

	res.Cut = out.cut
	if res.Cut > 0 {
		fmt.Fprintf(f, "\nconch: output cut at %d bytes; %d more not kept\n", hookLogMax, res.Cut)
	}
	res.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	took := time.Since(start).Round(time.Millisecond)
	switch {
	case err == nil:
		fmt.Fprintf(f, "\nconch: finished in %s\n", took)
	case res.Stopped:
		res.Exit = exitCode(err)
		fmt.Fprintf(f, "\nconch: stopped after %s: the server stopped or reloaded\n", took)
	case res.TimedOut:
		res.Exit = exitCode(err)
		fmt.Fprintf(f, "\nconch: stopped after %s, its timeout\n", took)
	default:
		res.Exit, res.Err = exitCode(err), err
		fmt.Fprintf(f, "\nconch: failed after %s: %v\n", took, err)
	}
	h.prune(dir)
	if err != nil && !res.Stopped {
		h.failed(a, ev, res, a.HookTimeout())
	}
}

// failed says a run that went wrong where the activity is read.
func (h *hooks) failed(a config.Action, ev hookEvent, res hookRun, timeout time.Duration) {
	var text string
	switch {
	case res.TimedOut:
		text = fmt.Sprintf("hook %s on %s took longer than %s and was stopped", a.Name, ev.Name, timeout)
	case res.Exit > 0:
		text = fmt.Sprintf("hook %s on %s failed (exit %d)", a.Name, ev.Name, res.Exit)
	default:
		text = fmt.Sprintf("hook %s on %s failed: %v", a.Name, ev.Name, res.Err)
	}
	if res.Log != "" {
		text += " — its output is in " + res.Log
	}
	log.Printf("hooks: %s", text)
	if h.note != nil {
		h.note(proto.AutopilotNote{ProjectID: ev.ProjectID, Branch: ev.Branch, Text: text, Error: true, Kind: proto.ActivityHook})
	}
}

// stopAll stops every run under way and starts no more: the server is
// stopping or about to reload, and a run's output goes to a pipe this
// process reads, which the new program could not.
func (h *hooks) stopAll() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.stopped = true
	for _, cmd := range h.running {
		killGroup(cmd)
	}
	h.mu.Unlock()
}

// carryOn starts hooks again after stopAll, for a reload whose exec
// failed: this process goes on serving, and its hooks with it.
func (h *hooks) carryOn() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.stopped = false
	h.mu.Unlock()
}

// wait is for tests: every fire and run under way has ended.
func (h *hooks) wait() { h.wg.Wait() }

func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if c := ee.ExitCode(); c >= 0 {
			return c
		}
		return 1
	}
	return 0
}

// hookBaseEnv is the server's environment without what would make a hook
// pass for something it is not — a pane, or a reload — and with the
// socket, so a hook can drive conch.
func hookBaseEnv(sock string) []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "CONCH_PANE_ID", "CONCH_SOCKET", reloadStateEnv:
			continue
		}
		out = append(out, kv)
	}
	return append(out, "CONCH_SOCKET="+sock)
}

// prune keeps the newest hookLogsKept logs.
func (h *hooks) prune(dir string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) <= hookLogsKept {
		return
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // a timestamp first, so oldest first
	for _, n := range names[:max(0, len(names)-hookLogsKept)] {
		os.Remove(filepath.Join(dir, n))
	}
}

// slug is an action's name as part of a file name.
func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
		if b.Len() >= 40 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "hook"
	}
	return s
}

// cappedWriter keeps the first max bytes and counts the rest, never
// failing a write: a hook told its output went nowhere would stop.
type cappedWriter struct {
	mu  sync.Mutex
	w   io.Writer
	max int64
	n   int64
	cut int64
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := int64(len(p))
	if room := c.max - c.n; keep > room {
		keep = max(room, 0)
	}
	if keep > 0 {
		c.w.Write(p[:keep])
		c.n += keep
	}
	c.cut += int64(len(p)) - keep
	return len(p), nil
}

// observePRs is a project's pull requests as just read, by branch, and
// the events in them since the last read: one opened (or reopened), one
// merged that was seen open. A project read for the first time has none —
// everything open then was opened before the server could see it.
func (h *hooks) observePRs(projectID string, prs map[string]proto.PRInfo) []hookEvent {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	first := !h.saved.Seen[projectID]
	changed := first
	h.saved.Seen[projectID] = true
	prefix := projectID + "\x00"
	var out []hookEvent
	now := map[string]bool{}
	for branch, pr := range prs {
		key := prefix + branch
		now[key] = true
		cur := fmt.Sprintf("%d %s", pr.Number, pr.State)
		prev := h.saved.PRs[key]
		h.saved.PRs[key] = cur
		changed = changed || prev != cur
		if first || prev == cur {
			continue
		}
		var prevNum int
		var prevState string
		fmt.Sscanf(prev, "%d %s", &prevNum, &prevState)
		ev := hookEvent{ProjectID: projectID, Branch: branch, PR: pr.Number, PRURL: pr.URL, PRTitle: pr.Title}
		switch {
		case pr.State == "OPEN":
			ev.Name = config.EventPROpened
		case pr.State == "MERGED" && prevNum == pr.Number && prevState == "OPEN":
			ev.Name = config.EventPRMerged
		default:
			continue
		}
		out = append(out, ev)
	}
	for key := range h.saved.PRs {
		if strings.HasPrefix(key, prefix) && !now[key] {
			delete(h.saved.PRs, key)
			changed = true
		}
	}
	if changed {
		h.saveLocked()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Branch < out[j].Branch })
	return out
}

// ---- the server's side ----

// hookEvent fills in what the server knows about a project, a branch and
// its worktree.
func (s *Server) hookEventFor(name, projectID, branch string) hookEvent {
	ev := hookEvent{Name: name, ProjectID: projectID, Branch: branch}
	if projectID == "" || s.projects == nil {
		return ev
	}
	p, perr := s.projects.get(projectID)
	if perr != nil {
		return ev
	}
	ev.Project, ev.Root = p.snapshot().Name, p.root
	if branch != "" {
		if wt, ok := p.branchWorktree(branch); ok {
			ev.Worktree = wt.Path
		}
	}
	return ev
}

// hookAgent is an agent's state change: agent.done for a finished turn,
// agent.waiting for one now waiting on the person. A pane adopted after a
// reload comes back from no state, which is no change (finishedTurn).
func (s *Server) hookAgent(info proto.PaneInfo, prev string) {
	a := info.Agent
	if s.hooks == nil || a == nil || prev == "" || a.State == prev {
		return
	}
	var name string
	switch {
	case finishedTurn(prev, a.State):
		name = config.EventAgentDone
	case a.State == proto.AgentBlocked:
		name = config.EventAgentWaiting
	default:
		return
	}
	ev := s.hookEventFor(name, info.ProjectID, info.Branch)
	ev.Pane, ev.Agent, ev.Message = info.ID, a.Name, a.Message
	if info.Cwd != "" {
		ev.Worktree = info.Cwd
	}
	s.hooks.fire(ev)
}

// hookPRs is a project's pull requests as just read.
func (s *Server) hookPRs(p *project, prs map[string]proto.PRInfo) {
	if s.hooks == nil {
		return
	}
	for _, ev := range s.hooks.observePRs(p.id, prs) {
		full := s.hookEventFor(ev.Name, p.id, ev.Branch)
		full.PR, full.PRURL, full.PRTitle = ev.PR, ev.PRURL, ev.PRTitle
		s.hooks.fire(full)
	}
}

// hookWorktree is a worktree just made.
func (s *Server) hookWorktree(p *project, path, branch string) {
	if s.hooks == nil {
		return
	}
	ev := s.hookEventFor(config.EventWorktreeCreated, p.id, branch)
	ev.Worktree = path
	s.hooks.fire(ev)
}

// hookTaskFailed is a task that could not be started.
func (s *Server) hookTaskFailed(tp proto.TaskCreateParams, why string) {
	if s.hooks == nil {
		return
	}
	ev := s.hookEventFor(config.EventTaskFailed, tp.ProjectID, tp.Branch)
	ev.Agent, ev.Error = tp.Agent, why
	s.hooks.fire(ev)
}
