package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func sentCount(peer *a1Peer, method string) int {
	n := 0
	for _, msg := range peer.snapshot() {
		if msg.Method == method {
			n++
		}
	}
	return n
}

// A server with the autopilot does the sending back and the merging, so
// the TUI does neither — or both would act on one turn.
func TestAutopilotServerTakesOver(t *testing.T) {
	m, mach, peer := autoMergeModel(t, proto.CapPRMerge, proto.CapAutopilot)
	m.cfg.Verify.AutoFix = 3
	runBatch(m.autoFixCI(mach.id, ciProject("pending"), ciProject("fail")))
	runBatch(m.autoMergeCI(mach.id, ciProject("pending"), ciProject("pass")))
	m.autoMerges = map[string]*autoMergeWait{verifyKey(mach.id, "r1", "wip"): {}}
	runBatch(m.autoMergeRetry(mach.id))
	if mergedPR(peer) != nil || sentCount(peer, proto.MethodBranchCILog) != 0 || sentCount(peer, proto.MethodAgentPrompt) != 0 {
		t.Fatalf("the TUI acted beside the server: %v", peer.snapshot())
	}
}

// The policy goes to every server that takes one, at connection and when
// Settings changes it; an older server is left to the TUI.
func TestAutopilotPolicyIsHandedOver(t *testing.T) {
	m, mach, peer := fixModel(t, proto.CapAutopilot)
	m.cfg.Verify.AutoFix, m.cfg.Verify.AutoMerge = 2, true
	runBatch(m.pushAutopilot(mach.id))
	var pol proto.AutopilotPolicy
	sentParams(t, peer, proto.MethodAutopilotSet, &pol)
	if pol != (proto.AutopilotPolicy{AutoFix: 2, AutoMerge: true}) {
		t.Fatalf("policy %+v", pol)
	}

	m.cfg.Verify.AutoMerge = false
	runBatch(m.pushAutopilotAll())
	pol = proto.AutopilotPolicy{}
	sentParams(t, peer, proto.MethodAutopilotSet, &pol)
	if pol.AutoMerge {
		t.Fatalf("after a change %+v", pol)
	}

	old, oldMach, oldPeer := fixModel(t)
	if cmd := old.pushAutopilot(oldMach.id); cmd != nil {
		t.Fatal("handed a policy to a server that predates it")
	}
	if n := sentCount(oldPeer, proto.MethodAutopilotSet); n != 0 {
		t.Fatalf("sent %d", n)
	}
	oldMach.c = nil
	if cmd := old.pushAutopilot(oldMach.id); cmd != nil {
		t.Fatal("handed a policy to nobody")
	}

	// A refusal is said, not swallowed.
	peer.setError(proto.MethodAutopilotSet, "nope")
	if msgs := runBatch(m.pushAutopilot(mach.id)); len(msgs) != 1 {
		t.Fatalf("refusal: %v", msgs)
	} else if e, ok := msgs[0].(errMsg); !ok || !strings.Contains(e.err.Error(), "nope") {
		t.Fatalf("refusal: %#v", msgs[0])
	}
}

// Settings' toggles hand the new policy over as they save it.
func TestAutopilotSettingsPush(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	m, mach, peer := fixModel(t, proto.CapAutopilot)
	s := &settings{tab: gitTab}
	for _, it := range s.items(m) {
		if it.label == "A green pull request merges itself" {
			runBatch(it.run(m))
		}
	}
	var pol proto.AutopilotPolicy
	sentParams(t, peer, proto.MethodAutopilotSet, &pol)
	if !pol.AutoMerge || !m.cfg.Verify.AutoMerge {
		t.Fatalf("toggled: %+v", pol)
	}
	_ = mach
}

// A check that ends is reported to a server with the autopilot — pass or
// fail, since either moves a hold on a merge — and not counted here.
func TestAutopilotCheckReported(t *testing.T) {
	m, mach, peer := fixModel(t, proto.CapAgentPrompt, proto.CapAutopilot)
	exited := proto.PaneInfo{ID: "check1", State: proto.PaneExited, ExitCode: 2}
	runBatch(m.autoFix(mach.id, exited)) // auto_fix 0: still reported
	var ck proto.AutopilotCheck
	sentParams(t, peer, proto.MethodAutopilotCheck, &ck)
	if ck != (proto.AutopilotCheck{ProjectID: "r1", Branch: "wip", Pane: "check1", Command: "go test ./...", Exit: 2}) {
		t.Fatalf("reported %+v", ck)
	}
	if sentCount(peer, proto.MethodAgentPrompt) != 0 || len(m.verifyFixes) != 0 {
		t.Fatal("the TUI sent the failure itself")
	}
	if cmd := m.autoFix(mach.id, proto.PaneInfo{ID: "p9", ExitCode: 1}); cmd != nil {
		t.Fatal("a stranger pane was reported")
	}
}

// What the server did is said; a merge also refreshes what is on screen
// of that branch, as one asked for here does.
func TestAutopilotNote(t *testing.T) {
	m, mach, _ := fixModel(t, proto.CapAutopilot)
	m.autopilotNote(mach.id, proto.AutopilotNote{ProjectID: "r1", Branch: "wip", Text: "wip is green but not merged: its pull request is a draft"})
	if m.flash != "wip is green but not merged: its pull request is a draft" || m.flashIsErr {
		t.Fatalf("note: %q %v", m.flash, m.flashIsErr)
	}
	m.autopilotNote(mach.id, proto.AutopilotNote{ProjectID: "r1", Branch: "wip", Text: "GitHub would not", Error: true})
	if !m.flashIsErr {
		t.Fatal("an error note was not an error")
	}
	m.autopilotNote(mach.id, proto.AutopilotNote{ProjectID: "r1", Branch: "wip", Text: "merged #7 on GitHub (merge), green", Merged: true})
	if m.flash != "merged #7 on GitHub (merge), green" || m.flashIsErr {
		t.Fatalf("merged: %q", m.flash)
	}
	// Through the event, as the server sends it.
	runBatch(m.handleEvent(mach, eventMsg(t, proto.EventAutopilotNote, proto.AutopilotNote{ProjectID: "r1", Branch: "wip", Text: "from the server"})))
	if m.flash != "from the server" {
		t.Fatalf("event: %q", m.flash)
	}
}

// Settings says where the autopilot runs, and names a machine whose
// server leaves it to the TUI.
func TestAutopilotWhere(t *testing.T) {
	m, _, _ := fixModel(t, proto.CapAutopilot)
	if got := autopilotWhere(m); !strings.Contains(got, "carry on with conch closed") || strings.Contains(got, "predates") {
		t.Fatalf("all new: %q", got)
	}
	old, _, _ := fixModel(t)
	if got := autopilotWhere(old); !strings.Contains(got, "predates that") {
		t.Fatalf("an old server: %q", got)
	}
}
