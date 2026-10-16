package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/when"
)

// What a machine's server will do later (conch agent prompt -at, conch task
// -at/-every) shows in the review queue, and an agent stopped by its plan
// limit can be told to carry on when the limit resets — C on its row — by
// the server, so it happens with the TUI closed. That is the common case:
// a limit that resets at three in the morning.

type scheduleMsg struct {
	machine string
	gen     int
	list    proto.ScheduleList
}

// loadSchedule asks a machine what it has scheduled.
func (mach *machine) loadSchedule() tea.Cmd {
	c, id, gen := mach.c, mach.id, mach.gen
	if c == nil || len(c.MissingCapabilities([]string{proto.CapSchedule})) > 0 {
		return nil
	}
	return func() tea.Msg {
		var list proto.ScheduleList
		if err := callCtx(c, proto.MethodScheduleList, nil, &list); err != nil {
			return nil
		}
		return scheduleMsg{machine: id, gen: gen, list: list}
	}
}

// limitHit says whether an agent's plan is used up on a machine, and when
// it is usable again: the latest reset among the windows that are full,
// since all of them have to reset before it can carry on.
func (mach *machine) limitHit(agent string, now time.Time) (time.Time, bool) {
	l, ok := mach.limits[agent]
	if !ok {
		return time.Time{}, false
	}
	var resets time.Time
	hit := false
	check := func(w *proto.LimitWindow) {
		if lw := liveWindow(w, now); lw != nil && lw.UsedPct >= 100 && !lw.ResetsAt.IsZero() {
			hit = true
			if lw.ResetsAt.After(resets) {
				resets = lw.ResetsAt
			}
		}
	}
	check(l.FiveHour)
	check(l.Week)
	check(l.Spend)
	for _, w := range l.Windows {
		check(&proto.LimitWindow{UsedPct: w.UsedPct, ResetsAt: w.ResetsAt})
	}
	return resets, hit
}

// scheduledFor is the message scheduled for a pane, if any.
func (mach *machine) scheduledFor(pane string) *proto.ScheduledItem {
	for i := range mach.schedule {
		if it := &mach.schedule[i]; it.Pane == pane && it.Task == nil {
			return it
		}
	}
	return nil
}

// continueAfterLimit has the server tell an agent stopped by its plan
// limit to carry on, a minute after the limit resets.
func (m *Model) continueAfterLimit(machine, pane string) tea.Cmd {
	mach := m.machine(machine)
	p := m.pane(machine, pane)
	if mach == nil || p == nil || p.Agent == nil {
		m.setFlash("no agent there to continue", true)
		return nil
	}
	resets, hit := mach.limitHit(p.Agent.Name, time.Now())
	switch {
	case !hit:
		m.setFlash(p.DisplayName()+" has not hit a plan limit; there is nothing to wait for", true)
		return nil
	case !m.hasCapability(machine, proto.CapSchedule):
		m.setFlash("the server there predates scheduling; reload it (conch server reload)", true)
		return nil
	case mach.scheduledFor(pane) != nil:
		m.setFlash(p.DisplayName()+" already has a message waiting for it; x on that row takes it away", true)
		return nil
	}
	at := resets.Add(time.Minute)
	it := proto.ScheduledItem{At: at, Pane: pane, Agent: p.Agent.Name, Text: "Your plan limit has reset. Carry on with the task where you left off.",
		Note: "after its plan limit resets"}
	var out proto.ScheduledItem
	name := p.DisplayName()
	return m.callOn(machine, proto.MethodScheduleAdd, it, &out, func() tea.Msg {
		return flashMsg(fmt.Sprintf("%s continues at %s, once its limit has reset — with conch closed too", name, at.Local().Format("15:04")))
	})
}

// scheduleTask hands a task to the machine's server for later: "queue"
// holds it until the project has room, "after REF" until that branch,
// pane or scheduled task has finished, anything else is a time (when.Parse).
func (m *Model) scheduleTask(machine string, task proto.TaskCreateParams, later string) tea.Cmd {
	if !m.hasCapability(machine, proto.CapSchedule) {
		return func() tea.Msg {
			return errMsg{errString("the server there predates scheduling; start it now, or reload the server (conch server reload)")}
		}
	}
	it := proto.ScheduledItem{At: time.Now(), Task: &task}
	done := func() tea.Msg {
		return flashMsg(fmt.Sprintf("queued: it starts when fewer than %d agents are running in the project", proto.DefaultWIP))
	}
	ref, isAfter := strings.CutPrefix(later, "after ")
	switch {
	case later == "queue":
		it.Queue = true
	case isAfter || later == "after":
		ref = strings.TrimSpace(ref)
		if ref == "" || later == "after" {
			return func() tea.Msg {
				return errMsg{errString("When: after what? Give a branch, a pane or a scheduled task: after api/model")}
			}
		}
		if !m.hasCapability(machine, proto.CapTaskAfter) {
			return func() tea.Msg {
				return errMsg{errString("the server there predates waiting for other work; reload it (conch server reload)")}
			}
		}
		it.After = ref
		from := "its branch"
		if task.Base != "" {
			from = task.Base
		}
		done = func() tea.Msg {
			return flashMsg("waits for " + ref + " to finish, then starts from " + from + " — with conch closed too")
		}
	default:
		at, err := when.Parse(later, time.Now())
		if err != nil {
			return func() tea.Msg { return errMsg{fmt.Errorf("When: %w", err)} }
		}
		it.At = at
		done = func() tea.Msg {
			return flashMsg("scheduled for " + at.Local().Format("Mon 15:04") + " — it starts with conch closed too")
		}
	}
	var out proto.ScheduledItem
	return m.callOn(machine, proto.MethodScheduleAdd, it, &out, done)
}

// removeScheduled takes an item off a machine's schedule.
func (m *Model) removeScheduled(machine, id string) tea.Cmd {
	return m.callOn(machine, proto.MethodScheduleRemove, proto.ScheduledItem{ID: id}, nil, func() tea.Msg {
		return flashMsg("taken off the schedule")
	})
}

// scheduledDetail is an item in words, for its queue row.
func scheduledDetail(it proto.ScheduledItem, now time.Time) string {
	when := it.At.Local().Format("15:04")
	if it.At.Sub(now) > 20*time.Hour {
		when = it.At.Local().Format("Mon 15:04")
	}
	var s string
	switch {
	case it.After != "" && it.Task != nil:
		after := it.AfterBranch
		if after == "" {
			after = it.After
		}
		s = "task after " + after + ": " + oneLine(it.Task.Prompt)
	case it.Queue:
		s = fmt.Sprintf("queued, %d at a time: %s", it.WIP, oneLine(it.Task.Prompt))
	case it.Task != nil:
		s = "task at " + when + ": " + oneLine(it.Task.Prompt)
	default:
		s = "message to " + agentLabel(it.Agent) + " at " + when + ": " + oneLine(it.Text)
	}
	if it.EveryMinutes > 0 {
		s = strings.Replace(s, " at "+when, fmt.Sprintf(" at %s, every %s", when, everyWords(it.EveryMinutes)), 1)
	}
	switch {
	case it.Waiting != "":
		s += " · waiting: " + it.Waiting
	case it.LastError != "":
		s += " · last run failed: " + it.LastError
	}
	return s
}

func everyWords(min int) string {
	every := func(n int, unit string) string {
		if n == 1 {
			return unit
		}
		return count(n, unit)
	}
	switch {
	case min%(7*24*60) == 0:
		return every(min/(7*24*60), "week")
	case min%(24*60) == 0:
		return every(min/(24*60), "day")
	case min%60 == 0:
		return every(min/60, "hour")
	}
	return every(min, "minute")
}
