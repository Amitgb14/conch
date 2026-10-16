package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/charmbracelet/lipgloss"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestLimitHit(t *testing.T) {
	now := time.Now()
	mach := newMachine(localMachine, "local", "")
	if _, hit := mach.limitHit("claude", now); hit {
		t.Fatal("no limits known, yet hit")
	}
	soon, later := now.Add(time.Hour), now.Add(3*time.Hour)
	mach.setLimits(proto.PlanLimits{Agent: "claude",
		FiveHour: &proto.LimitWindow{UsedPct: 100, ResetsAt: soon},
		Week:     &proto.LimitWindow{UsedPct: 40, ResetsAt: now.Add(72 * time.Hour)}})
	if at, hit := mach.limitHit("claude", now); !hit || !at.Equal(soon) {
		t.Fatalf("5-hour full: %v %v", at, hit)
	}
	// Two full windows: it can carry on only once both have reset.
	mach.setLimits(proto.PlanLimits{Agent: "claude",
		FiveHour: &proto.LimitWindow{UsedPct: 100, ResetsAt: soon},
		Windows:  []proto.NamedWindow{{Key: "seven_day_fable", UsedPct: 101, ResetsAt: later}}})
	if at, hit := mach.limitHit("claude", now); !hit || !at.Equal(later) {
		t.Fatalf("two full: %v %v", at, hit)
	}
	// A window that has already reset, or isn't full, or has no reset time.
	mach.setLimits(proto.PlanLimits{Agent: "claude",
		FiveHour: &proto.LimitWindow{UsedPct: 100, ResetsAt: now.Add(-time.Minute)},
		Week:     &proto.LimitWindow{UsedPct: 99.9, ResetsAt: later},
		Spend:    &proto.LimitWindow{UsedPct: 100}})
	if _, hit := mach.limitHit("claude", now); hit {
		t.Fatal("hit with nothing full and live")
	}
	if _, hit := mach.limitHit("codex", now); hit {
		t.Fatal("another agent's limits counted")
	}
}

// limitModel is the queue model with its finished Codex (p2, on feat) out
// of plan, on a server that schedules.
func limitModel(t *testing.T) (*Model, *machine, *a1Peer) {
	t.Helper()
	m, _ := a2QueueModel()
	mach := m.machines[0]
	c, peer := a1FakeClient(t, proto.CapSchedule)
	mach.c = c
	mach.setLimits(proto.PlanLimits{Agent: "codex", FiveHour: &proto.LimitWindow{UsedPct: 100, ResetsAt: time.Now().Add(2 * time.Hour)}})
	return m, mach, peer
}

func TestQueueOffersToContinueAfterTheLimit(t *testing.T) {
	m, mach, peer := limitModel(t)
	var row queueItem
	for _, it := range m.queueItems() {
		if it.paneID == "p2" {
			row = it
		}
	}
	if row.limited.IsZero() || !strings.Contains(row.detail, "stopped at its plan limit") || !strings.Contains(row.detail, "C continues it then") {
		t.Fatalf("row %+v", row)
	}
	cmd := m.continueAfterLimit(mach.id, "p2")
	if cmd == nil {
		t.Fatalf("nothing scheduled: %q", m.flash)
	}
	if msg, ok := cmd().(flashMsg); !ok || !strings.Contains(string(msg), "continues at") {
		t.Fatalf("got %#v", msg)
	}
	var it proto.ScheduledItem
	sentParams(t, peer, proto.MethodScheduleAdd, &it)
	resets := mach.limits["codex"].FiveHour.ResetsAt
	if it.Pane != "p2" || it.Agent != "codex" || !it.At.Equal(resets.Add(time.Minute)) || !strings.Contains(it.Text, "Carry on") {
		t.Fatalf("scheduled %+v", it)
	}

	// Once it is on the schedule, the row says when, and C won't add another.
	mach.schedule = []proto.ScheduledItem{{ID: "s1", At: it.At, Pane: "p2", Agent: "codex", Text: it.Text, Created: time.Now()}}
	for _, q := range m.queueItems() {
		if q.paneID == "p2" && q.scheduled == "" && !strings.Contains(q.detail, "continues at") {
			t.Fatalf("row %+v", q)
		}
	}
	if cmd := m.continueAfterLimit(mach.id, "p2"); cmd != nil || !strings.Contains(m.flash, "already has a message") {
		t.Fatalf("twice: %q", m.flash)
	}
}

func TestContinueAfterLimitRefusals(t *testing.T) {
	m, mach, _ := limitModel(t)
	mach.limits = nil
	if cmd := m.continueAfterLimit(mach.id, "p2"); cmd != nil || !strings.Contains(m.flash, "has not hit a plan limit") {
		t.Fatalf("no limit: %q", m.flash)
	}
	if cmd := m.continueAfterLimit(mach.id, "p4"); cmd != nil || !strings.Contains(m.flash, "no agent there") {
		t.Fatalf("a shell: %q", m.flash)
	}
	old, oldMach, _ := limitModel(t)
	oldMach.c = a2Client()
	if cmd := old.continueAfterLimit(oldMach.id, "p2"); cmd != nil || !strings.Contains(old.flash, "predates scheduling") {
		t.Fatalf("old server: %q", old.flash)
	}
}

func TestScheduledItemsInTheQueue(t *testing.T) {
	m, mach, peer := limitModel(t)
	now := time.Now()
	mach.schedule = []proto.ScheduledItem{
		{ID: "s1", At: now.Add(time.Hour), Pane: "p2", Agent: "codex", Text: "carry on", Waiting: "the agent is working", Created: now},
		{ID: "s2", At: now.Add(48 * time.Hour), EveryMinutes: 1440, Task: &proto.TaskCreateParams{ProjectID: "r1", Prompt: "bump deps"}, LastError: "dirty", Created: now},
	}
	qv := &queueView{}
	out := a2Plain(qv.render(*m, 200, 40))
	for _, want := range []string{"message to Codex at", "carry on · waiting: the agent is working", "task at", "every day: bump deps · last run failed: dirty", "later"} {
		if !strings.Contains(out, want) {
			t.Fatalf("queue lacks %q:\n%s", want, out)
		}
	}
	// x on one takes it off the schedule, rather than hiding it.
	list := m.queueLinesFor("")
	for i, l := range list {
		if l.header == "" && l.item.scheduled == "s2" {
			qv.sel = i
		}
	}
	qv.selKey = ""
	_, cmd := qv.key(m, a2Key("x"))
	if cmd == nil {
		t.Fatal("x did nothing")
	}
	cmd()
	var it proto.ScheduledItem
	sentParams(t, peer, proto.MethodScheduleRemove, &it)
	if it.ID != "s2" {
		t.Fatalf("removed %q", it.ID)
	}
	// C on a row that has nothing to do with limits says so.
	if _, cmd := qv.key(m, a2Key("C")); cmd != nil || !strings.Contains(m.flash, "C continues an agent that stopped") {
		t.Fatalf("C elsewhere: %q", m.flash)
	}
	// The event replaces the list.
	m.handleEvent(mach, proto.Message{Event: proto.EventScheduleChanged, Data: proto.Marshal(proto.ScheduleList{})})
	if len(mach.schedule) != 0 {
		t.Fatalf("event kept %v", mach.schedule)
	}
	if got := everyWords(30); got != "30 minutes" {
		t.Fatalf("every 30: %q", got)
	}
	if got := everyWords(120); got != "2 hours" {
		t.Fatalf("every 120: %q", got)
	}
	if got := everyWords(10080); got != "week" {
		t.Fatalf("every week: %q", got)
	}
}

// The task dialog's When: empty or now starts it, queue holds it for room,
// a time schedules it — each through the server's schedule.
func TestTaskDialogWhen(t *testing.T) {
	m, mach, peer := limitModel(t)
	proj := mach.projects[0]
	d := newTaskDialog(*m, localMachine, proj)
	if len(d.fields) != 6 || !strings.Contains(d.fields[5].in.Placeholder, "queue") {
		t.Fatalf("When: %d fields, %q", len(d.fields), d.fields[len(d.fields)-1].in.Placeholder)
	}
	msgs := a2Run(d.submit(m, []string{"Bump deps", "", "", "", "", "queue"}))
	if len(msgs) != 1 || !strings.Contains(string(msgs[0].(flashMsg)), "queued") {
		t.Fatalf("queue: %#v", msgs)
	}
	var it proto.ScheduledItem
	sentParams(t, peer, proto.MethodScheduleAdd, &it)
	if !it.Queue || it.Task == nil || it.Task.Prompt != "Bump deps" || it.Task.ProjectID != "r1" {
		t.Fatalf("queued %+v", it)
	}
	msgs = a2Run(d.submit(m, []string{"Nightly", "", "", "", "", "+2h"}))
	if len(msgs) != 1 || !strings.Contains(string(msgs[0].(flashMsg)), "scheduled for") {
		t.Fatalf("+2h: %#v", msgs)
	}
	it = proto.ScheduledItem{}
	sentParams(t, peer, proto.MethodScheduleAdd, &it)
	if it.Queue || time.Until(it.At) < 119*time.Minute {
		t.Fatalf("scheduled %+v", it)
	}
	for _, tc := range []struct{ v []string }{
		{[]string{"x", "", "", "", "", "soon"}},
		{[]string{"x", "", "", "", "2", "queue"}},
		{[]string{"x", "", "", "claude,codex", "", "+1h"}},
	} {
		if msg := a2ErrText(a2Run(d.submit(m, tc.v))); msg == "" {
			t.Errorf("%v was taken", tc.v)
		}
	}
	// An older server is said, not tried.
	mach.c = a2Client()
	if msg := a2ErrText(a2Run(d.submit(m, []string{"x", "", "", "", "", "queue"}))); !strings.Contains(msg, "predates scheduling") {
		t.Fatalf("old server: %q", msg)
	}
}

// A branch heading for a merge conflict says so where it is looked at: its
// queue row and the top of its changes, and a click still lands on a file.
func TestMergeRiskIsShown(t *testing.T) {
	m, _ := a2QueueModel()
	mach := m.machines[0]
	for i, w := range mach.projects[0].Worktrees {
		if w.Branch == "feat" {
			mach.projects[0].Worktrees[i].Overlaps = []proto.Overlap{{Branch: "wip", Files: []string{"auth.go", "login.go", "x.go"}}}
			mach.projects[0].Worktrees[i].Conflicts = []string{"auth.go"}
		}
	}
	risk := m.branchRisk(mach.id, "r1", "feat")
	if risk != "conflicts with main in auth.go · also changed on wip: auth.go, login.go and 1 more" {
		t.Fatalf("risk %q", risk)
	}
	if m.branchRisk(mach.id, "r1", "wip") != "" || m.branchRisk(mach.id, "nope", "feat") != "" {
		t.Fatal("risk where there is none")
	}
	qv := &queueView{}
	if out := a2Plain(qv.render(*m, 220, 30)); !strings.Contains(out, "⚠ conflicts with main in auth.go") {
		t.Fatalf("queue:\n%s", out)
	}
	cv := &changesView{machine: mach.id, projectID: "r1", branch: "feat"}
	cv.receive(changesMsg{projectID: "r1", branch: "feat", data: proto.Changes{ProjectID: "r1", Branch: "feat", Worktree: "/src/api-feat",
		Files: []proto.FileChange{{Path: "auth.go", Code: "M"}}}})
	lines := cv.render(*m, 80, 20)
	top := cv.filesTop(*m)
	if !strings.Contains(a2Plain(lines), "⚠ conflicts with main") || !strings.Contains(a2Plain(lines[top:top+1]), "auth.go") {
		t.Fatalf("top %d:\n%s", top, a2Plain(lines))
	}
	for _, l := range cv.render(*m, 30, 20) {
		if w := ansi.StringWidth(l); w > 30 {
			t.Fatalf("a line of %d in 30 columns: %q", w, l)
		}
	}
}

// When "after REF" holds the task until that work has finished; a server
// without it would start the task at once, so it is refused here.
func TestTaskDialogWhenAfter(t *testing.T) {
	m, mach, _ := limitModel(t)
	proj := mach.projects[0]
	d := newTaskDialog(*m, localMachine, proj)
	if !strings.Contains(d.fields[5].in.Placeholder, "after BRANCH") {
		t.Fatalf("When: %q", d.fields[5].in.Placeholder)
	}
	if msg := a2ErrText(a2Run(d.submit(m, []string{"x", "", "", "", "", "after api/model"}))); !strings.Contains(msg, "predates waiting") {
		t.Fatalf("old server: %q", msg)
	}
	c, peer := a1FakeClient(t, proto.CapSchedule, proto.CapTaskAfter)
	mach.c = c
	msgs := a2Run(d.submit(m, []string{"Add the endpoint", "", "", "", "", "after  api/model "}))
	if len(msgs) != 1 || string(msgs[0].(flashMsg)) != "waits for api/model to finish, then starts from its branch — with conch closed too" {
		t.Fatalf("after: %#v", msgs)
	}
	var it proto.ScheduledItem
	sentParams(t, peer, proto.MethodScheduleAdd, &it)
	if it.After != "api/model" || it.Queue || it.Task == nil || it.Task.Prompt != "Add the endpoint" || it.At.IsZero() {
		t.Fatalf("scheduled %+v", it)
	}
	msgs = a2Run(d.submit(m, []string{"Docs", "", "main", "", "", "after s1a2b3c4"}))
	if len(msgs) != 1 || !strings.Contains(string(msgs[0].(flashMsg)), "then starts from main") {
		t.Fatalf("after with a base: %#v", msgs)
	}
	for _, v := range []string{"after", "after   "} {
		if msg := a2ErrText(a2Run(d.submit(m, []string{"x", "", "", "", "", v}))); !strings.Contains(msg, "after what?") {
			t.Errorf("%q: %q", v, msg)
		}
	}

	// Its row says what it waits for: the branch, once known.
	now := time.Now()
	mach.schedule = []proto.ScheduledItem{
		{ID: "s1", At: now, After: "s0", Task: &proto.TaskCreateParams{Prompt: "add a client"}, Waiting: "waiting for s0 to start, then finish", Created: now},
		{ID: "s2", At: now, After: "s0", AfterBranch: "api/model", Task: &proto.TaskCreateParams{Prompt: "add docs"}, Created: now},
	}
	out := a2Plain((&queueView{}).render(*m, 200, 40))
	for _, want := range []string{"task after s0: add a client · waiting: waiting for s0 to start", "task after api/model: add docs"} {
		if !strings.Contains(out, want) {
			t.Fatalf("queue lacks %q:\n%s", want, out)
		}
	}
	// Narrow, the rows still fit.
	for _, w := range []int{20, 39} {
		for _, l := range (&queueView{}).render(*m, w, 20) {
			if lw := lipgloss.Width(l); lw > w {
				t.Fatalf("width %d: a line of %d: %q", w, lw, l)
			}
		}
	}
}
