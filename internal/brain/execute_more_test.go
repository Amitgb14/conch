package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// a6Call is one request the fake server received.
type a6Call struct {
	Method string
	Params json.RawMessage
}

// a6FakeServer answers client calls over an in-memory pipe. results maps a
// method to its JSON result; errs maps a method to an error message.
type a6FakeServer struct {
	mu      sync.Mutex
	calls   []a6Call
	results map[string]any
	errs    map[string]string
}

func (s *a6FakeServer) methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		out = append(out, c.Method)
	}
	return out
}

func (s *a6FakeServer) params(method string, v any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		if c.Method == method {
			return json.Unmarshal(c.Params, v) == nil
		}
	}
	return false
}

func a6Dial(t *testing.T, s *a6FakeServer) *client.Client {
	t.Helper()
	cliSide, srvSide := net.Pipe()
	conn := proto.NewConn(srvSide)
	go func() {
		for {
			msg, err := conn.Read()
			if err != nil {
				return
			}
			resp := proto.Message{ID: msg.ID}
			if msg.Method == proto.MethodHello {
				resp.Result = proto.Marshal(proto.HelloResult{})
			} else {
				s.mu.Lock()
				s.calls = append(s.calls, a6Call{Method: msg.Method, Params: msg.Params})
				if e, ok := s.errs[msg.Method]; ok {
					resp.Error = proto.Errorf(proto.ErrInternal, "%s", e)
				} else if r, ok := s.results[msg.Method]; ok {
					resp.Result = proto.Marshal(r)
				}
				s.mu.Unlock()
			}
			if conn.Write(resp) != nil {
				return
			}
		}
	}()
	c, err := client.New(cliSide, "a6-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); srvSide.Close() })
	return c
}

func a6ctx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestA6ExecuteStartTask(t *testing.T) {
	s := &a6FakeServer{results: map[string]any{proto.MethodTaskCreate: proto.PaneInfo{ID: "p7", Name: "task"}}}
	c := a6Dial(t, s)
	a := Action{Type: ActStartTask, Machine: "m1", Project: "r1", Prompt: "do it", Branch: "b", Base: "main", Agent: "codex"}
	res, err := Execute(a6ctx(t), c, a, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	if res.Machine != "m1" || res.Pane == nil || res.Pane.ID != "p7" {
		t.Fatalf("result: %+v", res)
	}
	var p proto.TaskCreateParams
	if !s.params(proto.MethodTaskCreate, &p) {
		t.Fatal("task.create not called")
	}
	if p.ProjectID != "r1" || p.Prompt != "do it" || p.Branch != "b" || p.Base != "main" || p.Agent != "codex" || p.Cols != 100 || p.Rows != 30 {
		t.Fatalf("params: %+v", p)
	}

	s2 := &a6FakeServer{errs: map[string]string{proto.MethodTaskCreate: "boom"}}
	if _, err := Execute(a6ctx(t), a6Dial(t, s2), a, 1, 1); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("task error: %v", err)
	}
}

func a6Projects() proto.ProjectList {
	return proto.ProjectList{Projects: []proto.ProjectInfo{
		{ID: "r0", Path: "/other"},
		{ID: "r1", Path: "/src/api", Worktrees: []proto.WorktreeInfo{
			{Path: "/src/api", Branch: "main"},
			{Path: "/src/api-wt/feat", Branch: "feat"},
		}},
	}}
}

func TestA6ExecuteStartAgentProjectDir(t *testing.T) {
	s := &a6FakeServer{results: map[string]any{
		proto.MethodProjectList: a6Projects(),
		proto.MethodPaneCreate:  proto.PaneInfo{ID: "p1"},
	}}
	c := a6Dial(t, s)
	res, err := Execute(a6ctx(t), c, Action{Type: ActStartAgent, Project: "r1", Agent: "claude", Prompt: "hi"}, 80, 24)
	if err != nil || res.Pane == nil || res.Pane.ID != "p1" {
		t.Fatalf("res %+v err %v", res, err)
	}
	var p proto.PaneCreateParams
	s.params(proto.MethodPaneCreate, &p)
	if p.Cwd != "/src/api" || p.Agent != "claude" || p.Prompt != "hi" || p.Cols != 80 || p.Rows != 24 {
		t.Fatalf("pane params: %+v", p)
	}
}

func TestA6ExecuteStartAgentExistingWorktree(t *testing.T) {
	s := &a6FakeServer{results: map[string]any{
		proto.MethodProjectList: a6Projects(),
		proto.MethodPaneCreate:  proto.PaneInfo{ID: "p2"},
	}}
	c := a6Dial(t, s)
	if _, err := Execute(a6ctx(t), c, Action{Type: ActStartAgent, Project: "r1", Branch: "feat"}, 0, 0); err != nil {
		t.Fatal(err)
	}
	var p proto.PaneCreateParams
	s.params(proto.MethodPaneCreate, &p)
	if p.Cwd != "/src/api-wt/feat" {
		t.Fatalf("cwd: %q", p.Cwd)
	}
	for _, m := range s.methods() {
		if m == proto.MethodWorktreeAdd {
			t.Fatal("worktree added for a checked-out branch")
		}
	}
}

func TestA6ExecuteStartAgentNewWorktree(t *testing.T) {
	s := &a6FakeServer{results: map[string]any{
		proto.MethodProjectList: a6Projects(),
		proto.MethodWorktreeAdd: proto.WorktreeResult{Path: "/src/api-wt/new"},
		proto.MethodPaneCreate:  proto.PaneInfo{ID: "p3"},
	}}
	c := a6Dial(t, s)
	if _, err := Execute(a6ctx(t), c, Action{Type: ActStartAgent, Project: "r1", Branch: "new", Base: "main"}, 0, 0); err != nil {
		t.Fatal(err)
	}
	var wt proto.WorktreeAddParams
	if !s.params(proto.MethodWorktreeAdd, &wt) || wt.ProjectID != "r1" || wt.Branch != "new" || wt.Base != "main" {
		t.Fatalf("worktree params: %+v", wt)
	}
	var p proto.PaneCreateParams
	s.params(proto.MethodPaneCreate, &p)
	if p.Cwd != "/src/api-wt/new" {
		t.Fatalf("cwd: %q", p.Cwd)
	}
}

func TestA6ExecuteStartAgentErrors(t *testing.T) {
	cases := []struct {
		name string
		s    *a6FakeServer
		a    Action
		want string
	}{
		{"list fails", &a6FakeServer{errs: map[string]string{proto.MethodProjectList: "nolist"}}, Action{Type: ActStartAgent, Project: "r1"}, "nolist"},
		{"missing project", &a6FakeServer{results: map[string]any{proto.MethodProjectList: a6Projects()}}, Action{Type: ActStartAgent, Project: "r9"}, "project r9 not found"},
		{"worktree fails", &a6FakeServer{results: map[string]any{proto.MethodProjectList: a6Projects()}, errs: map[string]string{proto.MethodWorktreeAdd: "nowt"}}, Action{Type: ActStartAgent, Project: "r1", Branch: "x"}, "nowt"},
		{"pane fails", &a6FakeServer{results: map[string]any{proto.MethodProjectList: a6Projects()}, errs: map[string]string{proto.MethodPaneCreate: "nopane"}}, Action{Type: ActStartAgent, Project: "r1"}, "nopane"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Execute(a6ctx(t), a6Dial(t, tc.s), tc.a, 0, 0)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestA6ExecuteSendPasteAndErrors(t *testing.T) {
	s := &a6FakeServer{}
	c := a6Dial(t, s)
	if _, err := Execute(a6ctx(t), c, Action{Type: ActSend, Pane: "p1", Text: "line1\nline2"}, 0, 0); err != nil {
		t.Fatal(err)
	}
	var st proto.PaneSendTextParams
	if !s.params(proto.MethodPaneSendText, &st) || !st.Paste || st.ID != "p1" {
		t.Fatalf("multi-line text should be pasted: %+v", st)
	}
	var sk proto.PaneSendKeysParams
	if !s.params(proto.MethodPaneSendKeys, &sk) || len(sk.Keys) != 1 || sk.Keys[0] != "enter" {
		t.Fatalf("keys: %+v", sk)
	}

	for _, m := range []string{proto.MethodPaneSendText, proto.MethodPaneSendKeys} {
		s := &a6FakeServer{errs: map[string]string{m: "fail-" + m}}
		if _, err := Execute(a6ctx(t), a6Dial(t, s), Action{Type: ActSend, Pane: "p1", Text: "x"}, 0, 0); err == nil || !strings.Contains(err.Error(), "fail-"+m) {
			t.Fatalf("%s error: %v", m, err)
		}
	}
	s3 := &a6FakeServer{errs: map[string]string{proto.MethodPaneClose: "noclose"}}
	if _, err := Execute(a6ctx(t), a6Dial(t, s3), Action{Type: ActClose, Pane: "p1"}, 0, 0); err == nil || !strings.Contains(err.Error(), "noclose") {
		t.Fatalf("close error: %v", err)
	}
}

func TestA6ExecuteFocusAndUnknown(t *testing.T) {
	s := &a6FakeServer{}
	c := a6Dial(t, s)
	if res, err := Execute(a6ctx(t), c, Action{Type: ActFocus, Machine: "m", Pane: "p1"}, 0, 0); err != nil || res.Pane != nil || res.Machine != "m" {
		t.Fatalf("focus: %+v %v", res, err)
	}
	if len(s.methods()) != 0 {
		t.Fatalf("focus should not call the server: %v", s.methods())
	}
	if _, err := Execute(a6ctx(t), c, Action{Type: "explode"}, 0, 0); err == nil || !strings.Contains(err.Error(), `unknown action "explode"`) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestA6MachineFrom(t *testing.T) {
	var branches []proto.BranchInfo
	for i := 0; i < 35; i++ {
		branches = append(branches, proto.BranchInfo{Name: fmt.Sprintf("b%d", i)})
	}
	projects := []proto.ProjectInfo{{
		ID: "r1", Name: "api", Path: "/api", Git: true, Base: "main",
		Worktrees: []proto.WorktreeInfo{{Branch: "b1"}},
		Branches:  branches,
	}, {ID: "r2", Name: "few", Branches: []proto.BranchInfo{{Name: "only"}}}}
	agents := []proto.AgentAvailability{{Name: "claude", Installed: true}, {Name: "codex"}, {Name: "gemini", Installed: true}}
	panes := []proto.PaneInfo{
		{ID: "p1", Name: "shell", State: proto.PaneRunning, Cwd: "/api", ProjectID: "r1", Branch: "b1"},
		{ID: "p2", Name: "claude", State: proto.PaneRunning, Title: "Fixing auth", Agent: &proto.AgentStatus{Name: "claude", State: "working"}},
		{ID: "p3", Name: "dead", State: "exited"},
	}
	m := MachineFrom("id", "label", true, agents, projects, panes, map[string]string{"p2": "editing"})
	if m.ID != "id" || m.Label != "label" || !m.Online {
		t.Fatalf("machine: %+v", m)
	}
	if strings.Join(m.Agents, ",") != "claude,gemini" {
		t.Fatalf("agents: %v", m.Agents)
	}
	if len(m.Projects) != 2 {
		t.Fatalf("projects: %+v", m.Projects)
	}
	p := m.Projects[0]
	if len(p.Branches) != 31 || p.Branches[1] != "b1 (worktree)" || p.Branches[0] != "b0" || p.Branches[30] != "… 5 more" {
		t.Fatalf("branches: %v", p.Branches)
	}
	if p.Base != "main" || !p.Git || p.Path != "/api" {
		t.Fatalf("project: %+v", p)
	}
	if len(m.Projects[1].Branches) != 1 {
		t.Fatalf("few: %+v", m.Projects[1])
	}
	if len(m.Panes) != 2 {
		t.Fatalf("exited panes should be dropped: %+v", m.Panes)
	}
	if m.Panes[0].Agent != "" || m.Panes[0].Project != "r1" || m.Panes[0].Branch != "b1" || m.Panes[0].Summary != "" {
		t.Fatalf("shell pane: %+v", m.Panes[0])
	}
	if m.Panes[1].Name != "Fixing auth" || m.Panes[1].Agent != "claude" || m.Panes[1].State != "working" || m.Panes[1].Summary != "editing" {
		t.Fatalf("agent pane: %+v", m.Panes[1])
	}
}

func TestA6Describe(t *testing.T) {
	w := world()
	cases := []struct {
		a    Action
		want string
	}{
		{Action{Type: ActStartTask, Machine: "local", Project: "r1", Agent: "codex", Prompt: "p"}, "Start codex in api on this computer (new branch): p"},
		{Action{Type: ActStartAgent, Machine: "local", Project: "r2", Agent: "claude"}, "Start claude in notes on this computer"},
		{Action{Type: ActStartAgent, Machine: "local", Project: "r1", Agent: "claude", Branch: "feat", Prompt: "go"}, "Start claude in api on this computer on feat: go"},
		{Action{Type: ActSend, Machine: "local", Pane: "p1", Text: "yes"}, "Send to fix login on this computer: yes"},
		{Action{Type: ActFocus, Machine: "local", Pane: "p1"}, "Show fix login on this computer"},
		{Action{Type: ActClose, Machine: "local", Pane: "p1"}, "Close fix login on this computer"},
		{Action{Type: ActClose, Machine: "gpu", Pane: "p1"}, "Close gpu-box"},
		{Action{Type: ActFocus, Machine: "nowhere", Pane: "p1"}, "Show nowhere"},
		{Action{Type: "weird", Machine: "local"}, "weird"},
	}
	for _, c := range cases {
		if got := w.Describe(c.a); got != c.want {
			t.Errorf("Describe(%+v) = %q, want %q", c.a, got, c.want)
		}
	}
}

func TestA6ValidateMore(t *testing.T) {
	w := world()
	if err := w.Validate(&Action{Type: ActFocus, Machine: "nope"}); err == nil || !strings.Contains(err.Error(), "unknown machine") {
		t.Fatalf("unknown machine: %v", err)
	}
	for _, typ := range []string{ActFocus, ActClose} {
		if err := w.Validate(&Action{Type: typ, Machine: "local", Pane: "p1"}); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
	}
	// A machine that reports no installed agents accepts any agent.
	w.Machines[0].Agents = nil
	a := Action{Type: ActStartAgent, Machine: "local", Project: "r2", Agent: "anything"}
	if err := w.Validate(&a); err != nil {
		t.Fatal(err)
	}
	// Whitespace-only prompts and texts are empty.
	if err := w.Validate(&Action{Type: ActStartTask, Machine: "local", Project: "r1", Prompt: "  \n"}); err == nil {
		t.Fatal("blank prompt accepted")
	}
	if err := w.Validate(&Action{Type: ActSend, Machine: "local", Pane: "p1", Text: " \t"}); err == nil {
		t.Fatal("blank text accepted")
	}
}
