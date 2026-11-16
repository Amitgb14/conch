package desktop

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/fleet"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/server"
)

// TestBridgeContract pins the wire shapes three apps are written against.
// A key renamed here breaks Conch.app, the Linux app and the Windows app
// at once, in three languages, silently — so it has to fail a test first.
func TestBridgeContract(t *testing.T) {
	since := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		v    any
		want string
	}{
		{"message", Message{Type: TypeResult, ID: "7", OK: true, Data: json.RawMessage(`{}`)},
			`{"type":"result","id":"7","ok":true,"data":{}}`},
		{"patch message", Message{Type: TypePatch, Seq: 3, Data: json.RawMessage(`{}`)},
			`{"type":"patch","seq":3,"data":{}}`},
		{"intent", Message{Type: TypeIntent, ID: "1", Intent: IntentPaneNew, Data: json.RawMessage(`{}`)},
			`{"type":"intent","id":"1","intent":"pane.new","data":{}}`},
		{"error", Message{Type: TypeResult, ID: "2", Error: "no"},
			`{"type":"result","id":"2","error":"no"}`},
		{"app hello", AppHello{App: "conch-windows", Version: "0.1", OS: "windows", Raw: true, Wrap: []string{"wsl.exe", "-d", "Ubuntu"}},
			`{"app":"conch-windows","version":"0.1","os":"windows","raw":true,"wrap":["wsl.exe","-d","Ubuntu"]}`},
		{"bridge hello", BridgeHello{Protocol: 1, Conch: "0.2.1", Capabilities: []string{"desktop.v1"}},
			`{"protocol":1,"conch":"0.2.1","capabilities":["desktop.v1"]}`},
		{"empty state", State{Machines: []Machine{}, Projects: []Project{}, Panes: []Pane{}},
			`{"machines":[],"projects":[],"panes":[]}`},
		{"machine", Machine{ID: "busybox", Label: "busybox", State: "offline", Detail: "no route"},
			`{"id":"busybox","label":"busybox","state":"offline","detail":"no route"}`},
		{"project", Project{Key: "local:x1", Machine: "local", ID: "x1", Name: "conch", Path: "/src/conch"},
			`{"key":"local:x1","machine":"local","id":"x1","name":"conch","path":"/src/conch"}`},
		{"pane", Pane{Key: "local:p3", Machine: "local", ID: "p3", Name: "claude", Title: "t", Project: "local:x1", Branch: "b",
			State: "exited", ExitCode: 2, Agent: &Agent{Name: "claude", State: AgentWaiting, Since: since, Failed: true}},
			`{"key":"local:p3","machine":"local","id":"p3","name":"claude","title":"t","project":"local:x1","branch":"b","state":"exited","exit_code":2,"agent":{"name":"claude","state":"waiting","since":"2026-11-15T12:00:00Z","failed":true}}`},
		{"bare pane", Pane{Key: "local:p1", Machine: "local", ID: "p1", State: "running"},
			`{"key":"local:p1","machine":"local","id":"p1","name":"","state":"running"}`},
		{"patch", Patch{Panes: &Changes[Pane]{Remove: []string{"local:p1"}}},
			`{"panes":{"remove":["local:p1"]}}`},
		{"notify", Notify{Kind: "waiting", Pane: "local:p3", Title: "claude is waiting for you", Body: "b"},
			`{"kind":"waiting","pane":"local:p3","title":"claude is waiting for you","body":"b"}`},
		{"pane.new", PaneNew{Machine: "busybox", Agent: "claude", Prompt: "hi", Command: []string{"sh"}, Cwd: "/w", Name: "n"},
			`{"machine":"busybox","agent":"claude","prompt":"hi","command":["sh"],"cwd":"/w","name":"n"}`},
		{"pane ref", PaneRef{Pane: "local:p1"}, `{"pane":"local:p1"}`},
		{"attach", Attach{Argv: []string{"conch", "attach", "p1"}, Raw: true}, `{"argv":["conch","attach","p1"],"raw":true}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Errorf("got  %s\nwant %s", got, c.want)
			}
		})
	}
	if Protocol != 1 || !reflect.DeepEqual(Capabilities, []string{"desktop.v1"}) {
		t.Errorf("protocol %d, capabilities %q: an app reads these to decide what to use", Protocol, Capabilities)
	}
}

func TestDiff(t *testing.T) {
	a := State{
		Machines: []Machine{{ID: "local", State: "online"}},
		Panes:    []Pane{{Key: "local:p1", State: "running"}, {Key: "local:p2", State: "running"}},
	}
	if _, changed := diff(a, a); changed {
		t.Error("the same state is a change")
	}
	b := State{
		Machines: []Machine{{ID: "local", State: "online"}, {ID: "box", State: "connecting"}},
		Panes:    []Pane{{Key: "local:p1", State: "exited"}, {Key: "local:p3", State: "running"}},
	}
	p, changed := diff(a, b)
	if !changed {
		t.Fatal("no change seen")
	}
	if p.Projects != nil {
		t.Errorf("projects did not change but came as %+v", p.Projects)
	}
	if got := p.Machines; got == nil || len(got.Set) != 1 || got.Set[0].ID != "box" || got.Remove != nil {
		t.Errorf("machines = %+v", got)
	}
	var set []string
	for _, x := range p.Panes.Set {
		set = append(set, x.Key)
	}
	if !reflect.DeepEqual(set, []string{"local:p1", "local:p3"}) || !reflect.DeepEqual(p.Panes.Remove, []string{"local:p2"}) {
		t.Errorf("panes set %q remove %q", set, p.Panes.Remove)
	}
	// Everything gone.
	p, _ = diff(b, State{})
	if p.Machines == nil || len(p.Machines.Remove) != 2 || len(p.Panes.Remove) != 2 {
		t.Errorf("emptied: %+v %+v", p.Machines, p.Panes)
	}
}

func TestNotices(t *testing.T) {
	agent := func(state string, failed bool) *Agent { return &Agent{Name: "claude", State: state, Failed: failed} }
	pane := func(key, name string, a *Agent) Pane { return Pane{Key: key, Name: name, Agent: a} }
	a := State{Panes: []Pane{
		pane("local:p1", "", agent(AgentWorking, false)),
		pane("local:p2", "fixer", agent(AgentWorking, false)),
		pane("local:p3", "", agent(AgentWorking, false)),
		pane("local:p4", "", agent(AgentWaiting, false)),
		pane("local:p5", "", nil),
	}}
	b := State{Panes: []Pane{
		pane("local:p1", "", agent(AgentWaiting, false)),
		pane("local:p2", "fixer", agent(AgentDone, false)),
		pane("local:p3", "", agent(AgentWorking, true)),
		pane("local:p4", "", agent(AgentWaiting, false)), // still waiting: said once
		pane("local:p5", "", agent(AgentWaiting, false)), // an agent just detected
		pane("local:p6", "", agent(AgentWaiting, false)), // a pane just seen
	}}
	got := notices(a, b)
	want := []Notify{
		{Kind: "waiting", Pane: "local:p1", Title: "claude is waiting for you"},
		{Kind: "done", Pane: "local:p2", Title: "fixer is done"},
		{Kind: "failed", Pane: "local:p3", Title: "claude failed"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("notices\n got %+v\nwant %+v", got, want)
	}
	if n := notices(b, b); n != nil {
		t.Errorf("no change, yet %+v", n)
	}
}

func TestAttachCommand(t *testing.T) {
	b := &bridge{o: Options{Conch: "/opt/conch"}, hello: &AppHello{}}
	got, err := b.attach("local:p4")
	if err != nil || !reflect.DeepEqual(got.Argv, []string{"/opt/conch", "attach", "p4"}) || got.Raw {
		t.Errorf("local: %+v %v", got, err)
	}
	// A bare pane is this computer's, as everywhere else.
	if got, _ := b.attach("p4"); !reflect.DeepEqual(got.Argv, []string{"/opt/conch", "attach", "p4"}) {
		t.Errorf("bare: %q", got.Argv)
	}
	b.hello = &AppHello{Raw: true, Wrap: []string{"wsl.exe", "-d", "Ubuntu"}}
	got, err = b.attach("busybox:p2")
	want := []string{"wsl.exe", "-d", "Ubuntu", "/opt/conch", "-m", "busybox", "attach", "-raw", "p2"}
	if err != nil || !reflect.DeepEqual(got.Argv, want) || !got.Raw {
		t.Errorf("remote, wrapped: %+v %v", got, err)
	}
	// The wrap is copied, not appended to in place.
	if len(b.hello.Wrap) != 3 {
		t.Errorf("wrap changed: %q", b.hello.Wrap)
	}
	for _, bad := range []string{"", "local:", "Box:p1", "local:p1; rm -rf ~", "a:b:c"} {
		if _, err := b.attach(bad); err == nil {
			t.Errorf("%q gave an attach command", bad)
		}
	}
}

// offline is a machine list with one machine that cannot be reached.
type offline struct{}

func (offline) Machines() []fleet.Machine {
	return []fleet.Machine{{ID: "box", Label: "box", State: fleet.Offline, Detail: "connection refused"}}
}

// session is a bridge served over pipes, read and written by a test.
type session struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Reader
	done chan error
}

func serve(t *testing.T, o Options) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{t: t, in: inW, out: bufio.NewReader(outR), done: make(chan error, 1)}
	go func() {
		err := Serve(context.Background(), inR, outW, o)
		outW.Close()
		s.done <- err
	}()
	t.Cleanup(func() {
		inW.Close()
		go io.Copy(io.Discard, outR) // let a last write finish
		select {
		case <-s.done:
		case <-time.After(10 * time.Second):
			t.Error("the bridge did not end when its input did")
		}
	})
	return s
}

func (s *session) send(m Message) {
	s.t.Helper()
	b, _ := json.Marshal(m)
	if _, err := s.in.Write(append(b, '\n')); err != nil {
		s.t.Fatal(err)
	}
}

func (s *session) intent(id, intent string, v any) {
	data, _ := json.Marshal(v)
	s.send(Message{Type: TypeIntent, ID: id, Intent: intent, Data: data})
}

// next reads messages until one passes keep, failing after a timeout.
func (s *session) next(keep func(Message) bool) Message {
	s.t.Helper()
	got := make(chan Message, 1)
	errs := make(chan error, 1)
	go func() {
		for {
			line, err := s.out.ReadBytes('\n')
			if err != nil {
				errs <- err
				return
			}
			var m Message
			if err := json.Unmarshal(line, &m); err != nil {
				errs <- err
				return
			}
			if keep(m) {
				got <- m
				return
			}
		}
	}()
	select {
	case m := <-got:
		return m
	case err := <-errs:
		s.t.Fatalf("reading the bridge: %v", err)
	case <-time.After(15 * time.Second):
		s.t.Fatal("timed out waiting for the bridge")
	}
	return Message{}
}

func ofType(typ string) func(Message) bool { return func(m Message) bool { return m.Type == typ } }

func resultFor(id string) func(Message) bool {
	return func(m Message) bool { return m.Type == TypeResult && m.ID == id }
}

func TestBridgeWantsHelloFirst(t *testing.T) {
	s := serve(t, Options{Machines: offline{}, Conch: "conch"})
	s.intent("1", IntentPaneAttach, PaneRef{Pane: "local:p1"})
	if r := s.next(resultFor("1")); r.Error != errNoHello.Error() || r.OK {
		t.Errorf("before hello: %+v", r)
	}
	s.send(Message{Type: TypeHello, Data: json.RawMessage(`"not an object"`)})
	if r := s.next(ofType(TypeResult)); !strings.HasPrefix(r.Error, "hello:") {
		t.Errorf("a bad hello: %+v", r)
	}
	s.in.Write([]byte("not json\n"))
	if r := s.next(ofType(TypeResult)); !strings.HasPrefix(r.Error, "not a message") {
		t.Errorf("a line that is not JSON: %+v", r)
	}
	s.send(Message{Type: TypeHello, Data: json.RawMessage(`{"app":"test"}`)})
	var h BridgeHello
	json.Unmarshal(s.next(ofType(TypeHello)).Data, &h)
	if h.Protocol != Protocol || !reflect.DeepEqual(h.Capabilities, Capabilities) {
		t.Errorf("hello: %+v", h)
	}
	var st State
	json.Unmarshal(s.next(ofType(TypeSnapshot)).Data, &st)
	if len(st.Machines) != 1 || st.Machines[0].Detail != "connection refused" || len(st.Panes) != 0 {
		t.Errorf("snapshot with a machine offline: %+v", st)
	}
	// Asking an offline machine says why it cannot.
	s.intent("2", IntentPaneNew, PaneNew{Machine: "box"})
	if r := s.next(resultFor("2")); !strings.Contains(r.Error, "connection refused") {
		t.Errorf("pane.new on an offline machine: %+v", r)
	}
	s.intent("3", IntentPaneClose, PaneRef{Pane: "nowhere:p1"})
	if r := s.next(resultFor("3")); !strings.Contains(r.Error, `no machine "nowhere"`) {
		t.Errorf("pane.close on no machine: %+v", r)
	}
	s.intent("4", "pane.fly", nil)
	if r := s.next(resultFor("4")); !strings.Contains(r.Error, "unknown intent") {
		t.Errorf("an unknown intent: %+v", r)
	}
	s.send(Message{Type: TypeSnapshot, ID: "5"})
	if r := s.next(resultFor("5")); !strings.Contains(r.Error, "takes intents") {
		t.Errorf("a snapshot sent to the bridge: %+v", r)
	}
	s.intent("6", IntentPaneClose, json.RawMessage(`[1]`))
	if r := s.next(resultFor("6")); r.Error == "" {
		t.Errorf("malformed data: %+v", r)
	}
}

// startServer runs a real conch server on a short socket path (macOS caps
// them near 104 bytes), in a home of its own.
func startServer(t *testing.T) string {
	t.Helper()
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Setenv("HOME", dir)
	t.Setenv("CONCH_HOME", filepath.Join(dir, ".conch"))
	t.Setenv("SHELL", "/bin/sh")
	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	t.Cleanup(func() { srv.StopAndWait(); os.RemoveAll(dir) })
	for range 200 {
		if c, err := client.Dial(sock, "probe"); err == nil {
			c.Close()
			return sock
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return ""
}

// TestBridgeOverARealServer is the whole round: hello, the snapshot, a
// pane started through the bridge arriving as a patch, the command that
// would show it, and closing it taking it away again.
func TestBridgeOverARealServer(t *testing.T) {
	sock := startServer(t)
	local, err := client.Dial(sock, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	pool := fleet.New(fleet.Options{
		Local:   func() (*client.Client, error) { return local, nil },
		Catalog: func() ([]remote.Machine, error) { return nil, nil },
	})
	t.Cleanup(pool.Close)

	s := serve(t, Options{Machines: pool, Conch: "/bin/conch", Version: "test", Every: 50 * time.Millisecond})
	s.send(Message{Type: TypeHello, Data: json.RawMessage(`{"app":"test","wrap":["wsl.exe"]}`)})
	s.next(ofType(TypeHello))
	var st State
	json.Unmarshal(s.next(ofType(TypeSnapshot)).Data, &st)
	if len(st.Machines) != 1 || st.Machines[0].ID != fleet.Local || st.Machines[0].State != fleet.Online || len(st.Panes) != 0 {
		t.Fatalf("snapshot: %+v", st)
	}

	s.intent("n", IntentPaneNew, PaneNew{Command: []string{"/bin/sh", "-c", "sleep 30"}, Name: "sleeper"})
	r := s.next(resultFor("n"))
	var made PaneRef
	json.Unmarshal(r.Data, &made)
	if !r.OK || !strings.HasPrefix(made.Pane, "local:p") {
		t.Fatalf("pane.new: %+v", r)
	}
	s.next(func(m Message) bool {
		if m.Type != TypePatch {
			return false
		}
		var p Patch
		json.Unmarshal(m.Data, &p)
		return p.Panes != nil && len(p.Panes.Set) == 1 && p.Panes.Set[0].Key == made.Pane && p.Panes.Set[0].Name == "sleeper"
	})

	s.intent("a", IntentPaneAttach, made)
	r = s.next(resultFor("a"))
	var a Attach
	json.Unmarshal(r.Data, &a)
	_, id, _ := fleet.SplitPaneID(made.Pane)
	if want := []string{"wsl.exe", "/bin/conch", "attach", id}; !r.OK || !reflect.DeepEqual(a.Argv, want) {
		t.Errorf("pane.attach: %+v %q", r, a.Argv)
	}

	// The result and the patch it causes come in either order: the
	// refresh may see the pane gone before the close returns.
	s.intent("c", IntentPaneClose, made)
	closed, removed := false, false
	for !closed || !removed {
		s.next(func(m Message) bool {
			switch {
			case m.Type == TypeResult && m.ID == "c":
				if !m.OK {
					t.Fatalf("pane.close: %+v", m)
				}
				closed = true
				return true
			case m.Type == TypePatch:
				var p Patch
				json.Unmarshal(m.Data, &p)
				if p.Panes != nil && reflect.DeepEqual(p.Panes.Remove, []string{made.Pane}) {
					removed = true
					return true
				}
			}
			return false
		})
	}
}
