package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
)

func TestParseEtime(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"00:07":       7 * time.Second,
		"1:02":        62 * time.Second,
		"01:00:00":    time.Hour,
		"2-03:04:05":  2*24*time.Hour + 3*time.Hour + 4*time.Minute + 5*time.Second,
		"  12:00 ":    12 * time.Minute,
		"10-00:00:00": 240 * time.Hour,
	} {
		got, ok := parseEtime(in)
		if !ok || got != want {
			t.Errorf("parseEtime(%q) = %v %v, want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "x", "1:xx", "-", "1-", "1:2:3:4", "-1:00", "a-00:00"} {
		if _, ok := parseEtime(bad); ok {
			t.Errorf("parseEtime(%q) took it", bad)
		}
	}
}

func TestParsePsTable(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	out := "  1     0 10-00:00:00 /sbin/launchd\n" +
		" 42     1    01:00 claude --resume x\n" +
		" 43    42    00:09 /bin/zsh -c -l source /a/snap.sh && eval 'npm  run dev' \\< /dev/null\n" +
		"bad\n" +
		" 44 x 00:01 nope\n" +
		" 45    42 ?? odd etime\n" +
		" 46    42 00:03\n" +
		"\n"
	got := parsePsTable([]byte(out), now)
	if len(got) != 5 {
		t.Fatalf("got %d rows: %+v", len(got), got)
	}
	p := got[43]
	if p.ppid != 42 || !p.since.Equal(now.Add(-9*time.Second)) || p.args != "/bin/zsh -c -l source /a/snap.sh && eval 'npm  run dev' \\< /dev/null" {
		t.Fatalf("row 43: %+v", p)
	}
	if !got[45].since.IsZero() || got[45].args != "odd etime" {
		t.Fatalf("an etime that is not one is not known: %+v", got[45])
	}
	if got[46].args != "" || got[46].ppid != 42 {
		t.Fatalf("no args: %+v", got[46])
	}
	if len(parsePsTable(nil, now)) != 0 {
		t.Fatal("nothing is nothing")
	}
}

func TestJobCommand(t *testing.T) {
	for in, want := range map[string]string{
		// Claude Code's own wrapper around a background Bash call.
		"/bin/zsh -c -l source /Users/x/.claude/shell-snapshots/snapshot-zsh-1.sh && setopt NO_EXTENDED_GLOB 2>/dev/null || true && eval 'npm run dev' \\< /dev/null && pwd -P >| /tmp/claude-1-cwd": "npm run dev",
		"bash -c eval 'echo '\\''hi'\\'' && sleep 9' < /dev/null": "echo 'hi' && sleep 9",
		"  python3 -m http.server 8000  ":                         "python3 -m http.server 8000",
		"eval 'unclosed":                                          "eval 'unclosed",
		"":                                                        "",
		"printf \x1b]52;c;aGk=\x07 x":                             "printf ·]52;c;aGk=· x",
	} {
		if got := jobCommand(in); got != want {
			t.Errorf("jobCommand(%q) = %q, want %q", in, got, want)
		}
	}
	long := jobCommand("sleep " + strings.Repeat("x", 500))
	if r := []rune(long); len(r) != jobCommandMax || r[len(r)-1] != '…' {
		t.Fatalf("long command not cut to %d: %d %q", jobCommandMax, len(r), long)
	}
}

func bgManifest() *detect.Manifest {
	return &detect.Manifest{Agent: "claude", ProcessNames: []string{"claude"}, Helpers: []string{"my-helper"}}
}

func TestAgentJobs(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	old := now.Add(-time.Minute)
	procs := map[int]psRow{
		10: {pid: 10, ppid: 1, since: old, args: "-zsh"},                                        // the pane's shell
		11: {pid: 11, ppid: 10, since: old, args: "claude"},                                     // the agent
		12: {pid: 12, ppid: 11, since: old, args: "/bin/zsh -c eval 'npm run dev' < /dev/null"}, // a job
		13: {pid: 13, ppid: 12, since: old, args: "node vite"},                                  // its child, part of it
		14: {pid: 14, ppid: 11, since: now.Add(-2 * time.Second), args: "git status"},           // too young
		15: {pid: 15, ppid: 11, since: old, args: "npx -y @modelcontextprotocol/server-github"}, // an MCP server
		16: {pid: 16, ppid: 11, since: old, args: "caffeinate -i -t 300"},                       // Claude's own
		17: {pid: 17, ppid: 11, since: old, args: "/usr/local/bin/gopls serve"},                 // a language server
		18: {pid: 18, ppid: 11, since: old, args: "my-helper --x"},                              // the manifest's helper
		19: {pid: 19, ppid: 10, since: old, args: "python3 -m http.server"},                     // the person's, beside the agent
		20: {pid: 20, ppid: 11, since: old, args: "pytest -f"},                                  // a second job
		21: {pid: 21, ppid: 11, since: time.Time{}, args: "unknown age"},                        // never seen old
		22: {pid: 22, ppid: 11, since: old, args: "conch hook claude Stop"},                     // conch's hook
		23: {pid: 23, ppid: 11, since: old, args: "/opt/x/typescript-language-server --stdio"},  // another
		24: {pid: 24, ppid: 11, since: old, args: "claude --child"},                             // the agent again
		25: {pid: 25, ppid: 24, since: old, args: "sleep 1000"},                                 // a job below it
	}
	ports := map[int][]int{13: {5173}, 19: {8000}}
	jobs := agentJobs(10, 11, bgManifest(), procs, ports, now)
	var pids []int
	for _, j := range jobs {
		pids = append(pids, j.PID)
	}
	if !slices.Equal(pids, []int{12, 20, 25}) {
		t.Fatalf("jobs %v, want [12 20 25]", pids)
	}
	if jobs[0].Command != "npm run dev" || !slices.Equal(jobs[0].Ports, []int{5173}) || !jobs[0].Since.Equal(old) {
		t.Fatalf("first job %+v", jobs[0])
	}
	if len(jobs[1].Ports) != 0 {
		t.Fatalf("ports of a job that has none: %v", jobs[1].Ports)
	}

	// The agent's pid is found by its name when what the detector last saw
	// in front is something else, or gone.
	for _, hint := range []int{0, 999, 19} {
		if got := agentJobs(10, hint, bgManifest(), procs, ports, now); len(got) != 3 {
			t.Fatalf("hint %d: %d jobs", hint, len(got))
		}
	}
	// The pane's program being the agent itself.
	if got := agentJobs(11, 0, bgManifest(), procs, ports, now); len(got) != 3 {
		t.Fatalf("agent as the pane's program: %d jobs", len(got))
	}
	// No agent under the pane, no manifest, nothing at all.
	if got := agentJobs(19, 0, bgManifest(), procs, ports, now); got != nil {
		t.Fatalf("no agent: %v", got)
	}
	if got := agentJobs(10, 11, nil, procs, ports, now); got != nil {
		t.Fatalf("no manifest: %v", got)
	}
	if got := agentJobs(10, 11, bgManifest(), nil, nil, now); got != nil {
		t.Fatalf("no processes: %v", got)
	}
	// A process table with a loop in it (pids reused mid-read) ends.
	loop := map[int]psRow{
		11: {pid: 11, ppid: 12, since: old, args: "claude"},
		12: {pid: 12, ppid: 11, since: old, args: "claude"},
	}
	if got := agentJobs(11, 11, bgManifest(), loop, nil, now); len(got) != 0 {
		t.Fatalf("loop: %v", got)
	}
}

func TestJobsKey(t *testing.T) {
	now := time.Now()
	a := agentJobs(10, 11, bgManifest(), map[int]psRow{
		10: {pid: 10, ppid: 1, args: "sh"}, 11: {pid: 11, ppid: 10, args: "claude"},
		12: {pid: 12, ppid: 11, since: now.Add(-time.Hour), args: "sleep 99"},
	}, map[int][]int{12: {80}}, now)
	if jobsKey(a) == jobsKey(nil) || jobsKey(a) != jobsKey(slices.Clone(a)) {
		t.Fatal("key does not tell jobs apart")
	}
	b := slices.Clone(a)
	b[0].Ports = []int{81}
	if jobsKey(a) == jobsKey(b) {
		t.Fatal("a port moving is a change")
	}
}

// bgPane is a pane conch takes for a Claude Code in a done state, with a
// process table of the test's own in which its program is that agent.
// The program is sleep itself, not `sh -c sleep`: a shell that does not
// exec its last command (Ubuntu's dash) stays in front, and conch never
// takes a shell in front for an agent.
func bgPane(t *testing.T, s *Server) (proto.PaneInfo, *entry) {
	t.Helper()
	res, perr := s.dispatch(&client{subs: map[string]*subscription{}}, proto.Message{Method: proto.MethodPaneCreate,
		Params: proto.Marshal(proto.PaneCreateParams{Command: []string{"sleep", "30"}, Cols: 40, Rows: 5})})
	if perr != nil {
		t.Fatal(perr)
	}
	info := res.(proto.PaneInfo)
	e := a5Entry(t, s, info.ID)
	e.evalMu.Lock()
	e.mu.Lock()
	e.tracker = detect.NewTracker(s.manifests, "claude")
	e.mu.Unlock()
	e.evalMu.Unlock()
	a5WaitFor(t, "the agent taken for claude", func() bool {
		s.observe(e)
		a := e.info().Agent
		return a != nil && a.Name == "claude" && a.State != detect.StateWorking
	})
	return info, e
}

func bgFakes(t *testing.T, table func() (map[int]psRow, error), ports map[int][]int) *int {
	t.Helper()
	oldParents, oldListening, oldTable := processParents, listeningByPid, processTable
	t.Cleanup(func() { processParents, listeningByPid, processTable = oldParents, oldListening, oldTable })
	calls := new(int)
	processTable = func(context.Context) (map[int]psRow, error) { *calls++; return table() }
	processParents = func(context.Context) (map[int]int, error) {
		rows, err := table()
		if err != nil {
			return nil, err
		}
		out := map[int]int{}
		for pid, r := range rows {
			out[pid] = r.ppid
		}
		return out, nil
	}
	listeningByPid = func(context.Context) (map[int][]int, error) { return ports, nil }
	return calls
}

func bgReport(t *testing.T, s *Server, id, event string) {
	t.Helper()
	if _, perr := s.dispatch(&client{}, proto.Message{Method: proto.MethodAgentReport,
		Params: proto.Marshal(proto.AgentReportParams{ID: id, Agent: "claude", Event: event})}); perr != nil {
		t.Fatal(perr)
	}
}

func TestBackgroundJobsOfAnIdleAgent(t *testing.T) {
	a5IsolateEnv(t)
	s, _ := a5Server(t)
	ev := a5Listen(t, s)
	info, e := bgPane(t, s)
	job := info.PID + 100000
	old := time.Now().Add(-time.Minute)
	var tableErr error
	calls := bgFakes(t, func() (map[int]psRow, error) {
		return map[int]psRow{
			info.PID: {pid: info.PID, ppid: 1, since: old, args: "claude"},
			job:      {pid: job, ppid: info.PID, since: old, args: "/bin/zsh -c eval 'npm run dev' < /dev/null"},
		}, tableErr
	}, map[int][]int{job: {5173}})

	s.lookListening(context.Background())
	a := e.info().Agent
	if a == nil || a.Background != 1 || len(a.Jobs) != 1 || a.Jobs[0].PID != job || a.Jobs[0].Command != "npm run dev" ||
		!slices.Equal(a.Jobs[0].Ports, []int{5173}) {
		t.Fatalf("agent %+v", a)
	}
	a5WaitFor(t, "pane.updated with the job", func() bool {
		ev.mu.Lock()
		defer ev.mu.Unlock()
		for _, m := range ev.msgs {
			var p proto.PaneInfo
			if m.Event == proto.EventPaneUpdated && json.Unmarshal(m.Data, &p) == nil && p.ID == info.ID &&
				p.Agent != nil && p.Agent.Background == 1 {
				return true
			}
		}
		return false
	})

	// A failed look leaves what was found.
	tableErr = errors.New("ps broke")
	s.lookListening(context.Background())
	if a := e.info().Agent; a.Background != 1 {
		t.Fatalf("a failed look changed the jobs: %+v", a)
	}
	tableErr = nil

	// Working, its children are the work: none shown, none looked for.
	bgReport(t, s, info.ID, "UserPromptSubmit")
	if a := e.info().Agent; a.State != detect.StateWorking || a.Background != 0 || a.Jobs != nil {
		t.Fatalf("working agent %+v", a)
	}
	n := *calls
	s.lookListening(context.Background())
	if *calls != n {
		t.Fatal("looked for jobs of an agent that is working")
	}
	// Done again: the job is looked for as the turn ends, not at the next
	// look, so what announces the end — a notification — can say it,
	// with the ports the last look found.
	bgReport(t, s, info.ID, "Stop")
	if a := e.info().Agent; a.Background != 1 || len(a.Jobs) != 1 || !slices.Equal(a.Jobs[0].Ports, []int{5173}) {
		t.Fatalf("done agent %+v", a)
	}
	if *calls != n+1 {
		t.Fatalf("%d looks at the end of the turn, want 1", *calls-n)
	}

	// The pane stops being an agent: no agent, nothing said of jobs.
	b, err := json.Marshal(proto.PaneInfo{Agent: &proto.AgentStatus{Name: "claude"}})
	if err != nil || strings.Contains(string(b), "background") || strings.Contains(string(b), "jobs") {
		t.Fatalf("an agent with no jobs says so on the wire: %s", b)
	}
}

func TestStopBackgroundJob(t *testing.T) {
	a5IsolateEnv(t)
	s, _ := a5Server(t)
	a5Listen(t, s)
	info, e := bgPane(t, s)
	sleeper := exec.Command("sleep", "60")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sleeper.Wait() }()
	t.Cleanup(func() { sleeper.Process.Kill() })
	job := sleeper.Process.Pid
	old := time.Now().Add(-time.Minute)
	rows := map[int]psRow{
		info.PID: {pid: info.PID, ppid: 1, since: old, args: "claude"},
		job:      {pid: job, ppid: info.PID, since: old, args: "sleep 60"},
	}
	bgFakes(t, func() (map[int]psRow, error) { return rows, nil }, nil)
	s.lookListening(context.Background())
	if a := e.info().Agent; a.Background != 1 {
		t.Fatalf("agent %+v", a)
	}
	stop := func(id string, pid int) *proto.Error {
		_, perr := s.dispatch(&client{}, proto.Message{Method: proto.MethodAgentBackgroundStop,
			Params: proto.Marshal(proto.AgentBackgroundStopParams{ID: id, PID: pid})})
		return perr
	}
	// Only a job of that pane's agent: not the agent, not any pid.
	for _, pid := range []int{info.PID, os.Getpid(), 0, -1} {
		if perr := stop(info.ID, pid); perr == nil {
			t.Fatalf("stopped pid %d", pid)
		}
	}
	if perr := stop("nope", job); perr == nil {
		t.Fatal("stopped a job of a pane that does not exist")
	}
	select {
	case <-done:
		t.Fatal("the job ended on a refused stop")
	case <-time.After(50 * time.Millisecond):
	}
	if perr := stop(info.ID, job); perr != nil {
		t.Fatal(perr)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the job is still running")
	}
	if a := e.info().Agent; a.Background != 0 {
		t.Fatalf("a stopped job still shown: %+v", a)
	}
	// A pid that has since left the pane — ended, or reused — is not
	// signalled even if the last look named it.
	e.mu.Lock()
	e.jobs = []proto.BackgroundJob{{PID: job, Command: "sleep 60"}}
	e.mu.Unlock()
	delete(rows, job)
	if perr := stop(info.ID, job); perr == nil {
		t.Fatal("signalled a pid no longer below the pane")
	}
}
