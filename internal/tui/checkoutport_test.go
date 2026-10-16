package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// portModel is a2Model with the feature worktree given ports, on a server
// that gives them.
func portModel(port int) *Model {
	m := a2Model()
	m.machines[0].c = a2Client(proto.CapWorktreeSetup)
	m.machines[0].projects[0].Worktrees[1].Port = port
	return m
}

func featRow() row {
	return row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"}
}

func previewOf(t *testing.T, msgs []any) previewDoneMsg {
	t.Helper()
	if len(msgs) != 1 {
		t.Fatalf("messages %#v", msgs)
	}
	p, ok := msgs[0].(previewDoneMsg)
	if !ok {
		t.Fatalf("not a preview: %#v", msgs[0])
	}
	return p
}

func runAny(cmd func() any) []any { return []any{cmd()} }

func TestCheckoutPortRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		m    func() *Model
		r    row
		want string
	}{
		"no project": {func() *Model { return portModel(41000) }, row{kind: kindMachine, machine: localMachine}, "select a branch or a pane in a git project"},
		"not a git project": {func() *Model {
			m := portModel(41000)
			m.machines[0].projects[0].Git = false
			return m
		}, featRow(), "select a branch or a pane in a git project"},
		"not checked out": {func() *Model { return portModel(41000) },
			row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "elsewhere"}, "isn't checked out anywhere yet"},
		"an old server": {func() *Model {
			m := portModel(41000)
			m.machines[0].c = a2Client()
			return m
		}, featRow(), "predates per-checkout ports"},
		"no ports yet": {func() *Model { return portModel(0) }, featRow(), "has no ports yet"},
	} {
		t.Run(name, func(t *testing.T) {
			m := tc.m()
			if cmd := m.openCheckoutPort(tc.r); cmd != nil {
				t.Fatal("a refusal ran something")
			}
			if !strings.Contains(m.flash, tc.want) {
				t.Fatalf("flash %q, want %q", m.flash, tc.want)
			}
		})
	}
}

func TestCheckoutPortOpensHere(t *testing.T) {
	m := portModel(41010)
	old := checkPreview
	t.Cleanup(func() { checkPreview = old })
	var asked string
	checkPreview = func(_ context.Context, link string) (int, string) { asked = link; return 200, "" }
	cmd := m.openCheckoutPort(featRow())
	if cmd == nil {
		t.Fatal("nothing ran")
	}
	p := previewOf(t, runAny(func() any { return cmd() }))
	if p.err != nil || p.url != "http://localhost:41010" || asked != p.url {
		t.Fatalf("preview %+v (asked %q)", p, asked)
	}

	// The main checkout counts too, from a pane in it.
	m.machines[0].projects[0].Worktrees[0].Port = 41000
	cmd = m.openCheckoutPort(row{kind: kindPane, machine: localMachine, paneID: "p1"})
	if p := previewOf(t, runAny(func() any { return cmd() })); p.url != "http://localhost:41000" {
		t.Fatalf("from a pane in the main checkout: %+v", p)
	}

	// Nothing listening: the link still comes back, with how to start a
	// server on the port.
	checkPreview = func(context.Context, string) (int, string) { return 0, "connection refused" }
	cmd = m.openCheckoutPort(featRow())
	p = previewOf(t, runAny(func() any { return cmd() }))
	if p.url != "http://localhost:41010" || p.err == nil || !strings.Contains(p.err.Error(), "$CONCH_PORT") {
		t.Fatalf("nothing answering: %+v", p)
	}
	a2Run(m.receivePreview(p))
	if lastClipboard != "http://localhost:41010" {
		t.Fatalf("the link wasn't kept: %q", lastClipboard)
	}
}

func TestCheckoutPortForwardsFromSSH(t *testing.T) {
	m := portModel(41020)
	mach := m.machines[0]
	mach.id, mach.label, mach.target = "box", "box", "dev@box"
	m.machines[0].panes = nil
	oldF, oldC := forwardPort, checkPreview
	t.Cleanup(func() { forwardPort, checkPreview = oldF, oldC })
	var gotTarget string
	var gotPort int
	forwardPort = func(_ context.Context, target string, port int) (int, error) {
		gotTarget, gotPort = target, port
		return 52000, nil
	}
	checkPreview = func(context.Context, string) (int, string) { return 200, "" }
	r := featRow()
	r.machine = "box"
	cmd := m.openCheckoutPort(r)
	if !strings.Contains(m.flash, "forwarding port 41020 from box") {
		t.Fatalf("flash %q", m.flash)
	}
	p := previewOf(t, runAny(func() any { return cmd() }))
	if gotTarget != "dev@box" || gotPort != 41020 || p.url != "http://localhost:52000" || p.err != nil {
		t.Fatalf("forward %s:%d, preview %+v", gotTarget, gotPort, p)
	}

	forwardPort = func(context.Context, string, int) (int, error) {
		return 0, errors.New("conch is not connected to dev@box right now")
	}
	cmd = m.openCheckoutPort(r)
	if p := previewOf(t, runAny(func() any { return cmd() })); p.err == nil || p.url != "" {
		t.Fatalf("a failed forward: %+v", p)
	}
}

func TestLKeyOpensThePreview(t *testing.T) {
	m := portModel(41010)
	old := checkPreview
	t.Cleanup(func() { checkPreview = old })
	checkPreview = func(context.Context, string) (int, string) { return 200, "" }
	m.expanded[projectNodeID(localMachine, "r1")] = true
	m.expanded[sectionID(localMachine, "r1", "branches")] = true
	m.showAll[scoped(localMachine, "r1")] = true
	m.rebuild()
	m.cursor = branchNodeID(localMachine, "r1", "feat")
	if indexOfRow(m.rows, m.cursor) < 0 {
		var ids []string
		for _, r := range m.rows {
			ids = append(ids, r.id)
		}
		t.Fatalf("no feat row among %q", ids)
	}
	_, cmd := m.Update(a2Key("L"))
	if cmd == nil {
		t.Fatalf("L ran nothing (flash %q)", m.flash)
	}
}
