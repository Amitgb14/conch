package main

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

var scopedAgent = proto.CallerInfo{Pane: "p4", Agent: "claude", Scoped: true, ID: "laptop/p4@1", Label: "p4 on laptop"}

// a4Local is this machine's server answering pane.caller with who (or
// perr); old leaves scoping out of its capabilities.
func a4Local(t *testing.T, who proto.CallerInfo, perr *proto.Error, old bool) *a4Server {
	t.Helper()
	srv := startA4Server(t, config.SocketPath())
	if old {
		srv.setHello(func(n int) proto.HelloResult {
			h := currentHello(n)
			h.Capabilities = []string{"pane.v1", proto.CapPaneScope}
			return h
		})
	}
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneCaller {
			if perr != nil {
				return nil, perr
			}
			return who, nil
		}
		return nil, nil
	})
	return srv
}

// a4Remote is the other machine's server, and a client of it.
func a4Remote(t *testing.T, old bool) (*a4Server, *client.Client) {
	t.Helper()
	srv := startA4Server(t, "")
	if old {
		srv.setHello(func(n int) proto.HelloResult {
			h := currentHello(n)
			h.Capabilities = []string{"pane.v1", "project.v1"}
			return h
		})
	}
	c, err := client.Dial(srv.sock, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return srv, c
}

func called(srv *a4Server, method string) bool {
	for _, m := range srv.methods() {
		if m == method {
			return true
		}
	}
	return false
}

func TestActForHereScopedAgent(t *testing.T) {
	a4Env(t)
	a4Local(t, scopedAgent, nil, false)
	far, c := a4Remote(t, false)
	if err := actForHere(c, "gpu-box"); err != nil {
		t.Fatal(err)
	}
	var p proto.ActForParams
	if !far.params(t, proto.MethodActFor, &p) || p != (proto.ActForParams{ID: "laptop/p4@1", Label: "p4 on laptop", Agent: "claude"}) {
		t.Fatalf("act_for: %+v", p)
	}
}

// Nothing would keep the agent to its work on a server without scoping,
// so it isn't driven at all.
func TestActForHereOldRemote(t *testing.T) {
	a4Env(t)
	a4Local(t, scopedAgent, nil, false)
	far, c := a4Remote(t, true)
	err := actForHere(c, "gpu-box")
	if err == nil || !strings.Contains(err.Error(), "the claude agent in p4 may not drive gpu-box: its conch server predates scoping") ||
		!strings.Contains(err.Error(), "conch machine upgrade gpu-box") {
		t.Fatalf("old remote: %v", err)
	}
	if called(far, proto.MethodActFor) {
		t.Fatal("asked an old server anyway")
	}
}

// Nobody to scope: a terminal pane, outside every pane, no server here,
// or a server here from before scoping. The remote is left as it was.
func TestActForHereNobodyToScope(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T){
		"terminal pane":     func(t *testing.T) { a4Local(t, proto.CallerInfo{Pane: "p2"}, nil, false) },
		"outside the panes": func(t *testing.T) { a4Local(t, proto.CallerInfo{}, nil, false) },
		"no server here":    func(t *testing.T) {},
		"older server here": func(t *testing.T) { a4Local(t, scopedAgent, nil, true) },
	} {
		t.Run(name, func(t *testing.T) {
			a4Env(t)
			setup(t)
			far, c := a4Remote(t, true) // even an old remote is fine then
			if err := actForHere(c, "gpu-box"); err != nil {
				t.Fatal(err)
			}
			if called(far, proto.MethodActFor) {
				t.Fatal("scoped anyway")
			}
		})
	}
}

// A server here that can't say who is running fails the command rather
// than let it through unscoped.
func TestActForHereLocalFails(t *testing.T) {
	a4Env(t)
	a4Local(t, proto.CallerInfo{}, proto.Errorf(proto.ErrInternal, "broken"), false)
	far, c := a4Remote(t, false)
	if err := actForHere(c, "gpu-box"); err == nil || !strings.Contains(err.Error(), "asking this machine's conch who is running: internal: broken") {
		t.Fatalf("local fails: %v", err)
	}
	if called(far, proto.MethodActFor) {
		t.Fatal("went on to the remote")
	}
}

// The remote refusing to be held (it shouldn't) fails the command too.
func TestActForHereRemoteRefuses(t *testing.T) {
	a4Env(t)
	a4Local(t, scopedAgent, nil, false)
	far, c := a4Remote(t, false)
	far.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodActFor {
			return nil, proto.Errorf(proto.ErrBadRequest, "already acts for someone")
		}
		return nil, nil
	})
	if err := actForHere(c, "gpu-box"); err == nil || !strings.Contains(err.Error(), "already acts for someone") {
		t.Fatalf("remote refuses: %v", err)
	}
}

// The whole of `conch -m`: through ssh to the other machine's server, the
// connection is held there before the command's own call goes out, and a
// server there without scoping is never sent the command.
func TestA4MachineFlagCarriesScope(t *testing.T) {
	for _, old := range []bool{false, true} {
		t.Run(map[bool]string{false: "scoping there", true: "older server there"}[old], func(t *testing.T) {
			a4Env(t)
			f := newA4SSH(t)
			f.setProbe(t, a4CurrentProbe())
			far := startA4Server(t, "")
			if old {
				far.setHello(func(n int) proto.HelloResult {
					h := currentHello(n)
					h.Capabilities = []string{"pane.v1", "project.v1"}
					return h
				})
			}
			far.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
				if msg.Method == proto.MethodPaneRead {
					return proto.PaneReadResult{Lines: []string{"over there"}}, nil
				}
				return nil, nil
			})
			t.Setenv("A4_BRIDGE_SOCK", far.sock)
			saved, err := remote.SaveMachine(remote.Machine{Target: "dev@gpu.lab"})
			if err != nil {
				t.Fatal(err)
			}
			a4Local(t, scopedAgent, nil, false)
			oldFlag := machineFlag
			machineFlag = saved.ID
			t.Cleanup(func() { machineFlag = oldFlag })

			out, _ := a4Capture(t, "", func() { err = runRead([]string{"p1"}) })
			calls := strings.Join(far.methods(), ",")
			if old {
				if err == nil || !strings.Contains(err.Error(), "predates scoping") || strings.Contains(calls, proto.MethodPaneRead) {
					t.Fatalf("old: %v, calls %s", err, calls)
				}
				return
			}
			if err != nil || out != "over there\n" {
				t.Fatalf("read: %q %v", out, err)
			}
			if !strings.HasPrefix(calls, proto.MethodActFor+","+proto.MethodPaneRead) {
				t.Fatalf("calls there: %s", calls)
			}
		})
	}
}

// -m local is this machine's server, which sees the caller itself: there
// is nothing to carry, and it is not asked.
func TestA4MachineLocalAsksNothing(t *testing.T) {
	a4Env(t)
	srv := a4Local(t, scopedAgent, nil, false)
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneRead {
			return proto.PaneReadResult{Lines: []string{"here"}}, nil
		}
		return scopedAgent, nil
	})
	old := machineFlag
	machineFlag = "local"
	t.Cleanup(func() { machineFlag = old })
	var err error
	out, _ := a4Capture(t, "", func() { err = runRead([]string{"p1"}) })
	if err != nil || out != "here\n" {
		t.Fatalf("read: %q %v", out, err)
	}
	if called(srv, proto.MethodPaneCaller) || called(srv, proto.MethodActFor) {
		t.Fatalf("asked: %v", srv.methods())
	}
}

// This machine's server going away mid-question fails the command: it
// can't be told the command is unscoped.
func TestActForHereLocalHangsUp(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneCaller {
			conn.Close()
		}
		return nil, nil
	})
	far, c := a4Remote(t, false)
	if err := actForHere(c, "gpu-box"); err == nil {
		t.Fatal("went on unscoped")
	}
	if called(far, proto.MethodActFor) {
		t.Fatal("declared anyway")
	}
}
