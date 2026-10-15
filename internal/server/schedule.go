package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// The server keeps a list of things to do later: a message for an agent
// once its plan limit resets, a task to start overnight, one to start every
// morning. The server, not the TUI, keeps it, since the TUI is often closed
// at the time — that is the point of asking for later. It is a file
// (schedule.json), so a reload or a restart keeps it.
//
// A message is never typed into an agent that is working or waiting on a
// question: it waits, and says so, until the agent is free. A pane that is
// gone, or now runs a different agent, drops the message rather than
// deliver it to something that never asked.

const (
	scheduleTick = 15 * time.Second
	// scheduleRetry is how long a due message waits for a busy agent
	// before it is tried again.
	scheduleRetry = time.Minute
	// scheduleMax keeps a runaway script from filling the file.
	scheduleMax = 200
	// scheduleMinEvery is the shortest repeat: anything shorter is a loop,
	// not a schedule.
	scheduleMinEvery = 5
	// maxWIP bounds a queue's limit: more agents than this at once is not
	// work anybody reviews.
	maxWIP = 20
)

type scheduleBook struct {
	file string

	mu    sync.Mutex
	items []proto.ScheduledItem
}

func loadSchedule(configDir string) *scheduleBook {
	b := &scheduleBook{file: filepath.Join(configDir, "schedule.json")}
	raw, err := os.ReadFile(b.file)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("schedule: %v", err)
		}
		return b
	}
	var saved proto.ScheduleList
	if err := json.Unmarshal(raw, &saved); err != nil {
		log.Printf("schedule: %s: %v", b.file, err)
		return b
	}
	for _, it := range saved.Items {
		if checkItem(&it) == nil && it.ID != "" {
			b.items = append(b.items, it)
		}
	}
	return b
}

// checkItem says what is wrong with an item, tidying what it can.
func checkItem(it *proto.ScheduledItem) *proto.Error {
	it.Text = strings.TrimSpace(it.Text)
	switch {
	case it.At.IsZero():
		return proto.Errorf(proto.ErrBadRequest, "a scheduled item needs a time")
	case it.Task == nil && (it.Pane == "" || it.Text == ""):
		return proto.Errorf(proto.ErrBadRequest, "a scheduled item is a message (a pane and its text) or a task")
	case it.Task != nil && it.Pane != "":
		return proto.Errorf(proto.ErrBadRequest, "a scheduled item is a message or a task, not both")
	case it.Task != nil && strings.TrimSpace(it.Task.Prompt) == "" && it.Task.Issue <= 0:
		return proto.Errorf(proto.ErrBadRequest, "a scheduled task needs a prompt")
	case it.EveryMinutes < 0, it.EveryMinutes > 0 && it.EveryMinutes < scheduleMinEvery:
		return proto.Errorf(proto.ErrBadRequest, "repeat every %d minutes or more", scheduleMinEvery)
	case it.Queue && it.Task == nil:
		return proto.Errorf(proto.ErrBadRequest, "only a task can be queued")
	case it.Queue && it.EveryMinutes > 0:
		return proto.Errorf(proto.ErrBadRequest, "a queued task runs once; repeat it with a schedule instead")
	case it.WIP < 0 || it.WIP > maxWIP:
		return proto.Errorf(proto.ErrBadRequest, "a project can run 1 to %d agents before its queue waits", maxWIP)
	}
	if it.Queue && it.WIP == 0 {
		it.WIP = proto.DefaultWIP
	}
	return nil
}

func (b *scheduleBook) list() proto.ScheduleList {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.listLocked()
}

func (b *scheduleBook) listLocked() proto.ScheduleList {
	out := append([]proto.ScheduledItem{}, b.items...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return proto.ScheduleList{Items: out}
}

func (b *scheduleBook) saveLocked() {
	raw, _ := json.MarshalIndent(b.listLocked(), "", "  ")
	tmp := b.file + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		log.Printf("schedule: save: %v", err)
		return
	}
	if err := os.Rename(tmp, b.file); err != nil {
		log.Printf("schedule: save: %v", err)
	}
}

func newScheduleID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "s" + hex.EncodeToString(b[:])
}

func (s *Server) scheduleAdd(it proto.ScheduledItem) (proto.ScheduledItem, *proto.Error) {
	if perr := checkItem(&it); perr != nil {
		return it, perr
	}
	if it.Pane != "" {
		e, perr := s.get(it.Pane)
		if perr != nil {
			return it, perr
		}
		info := e.info()
		if info.Agent == nil {
			return it, proto.Errorf(proto.ErrBadRequest, "pane %s is not running an agent", it.Pane)
		}
		if it.Agent == "" {
			it.Agent = info.Agent.Name
		}
		it.PaneCreated = info.Created
	}
	if it.Task != nil {
		if _, perr := s.projects.get(it.Task.ProjectID); perr != nil {
			return it, perr
		}
	}
	it.ID, it.Created = newScheduleID(), time.Now()
	it.LastRun, it.LastError, it.Waiting = time.Time{}, "", ""
	b := s.schedules
	b.mu.Lock()
	if len(b.items) >= scheduleMax {
		b.mu.Unlock()
		return it, proto.Errorf(proto.ErrBadRequest, "%d things are scheduled already; remove some first", scheduleMax)
	}
	b.items = append(b.items, it)
	b.saveLocked()
	list := b.listLocked()
	b.mu.Unlock()
	s.broadcast(proto.EventScheduleChanged, list)
	return it, nil
}

func (s *Server) scheduleRemove(id string) *proto.Error {
	b := s.schedules
	b.mu.Lock()
	for i, it := range b.items {
		if it.ID == id {
			b.items = append(b.items[:i], b.items[i+1:]...)
			b.saveLocked()
			list := b.listLocked()
			b.mu.Unlock()
			s.broadcast(proto.EventScheduleChanged, list)
			return nil
		}
	}
	b.mu.Unlock()
	return proto.Errorf(proto.ErrNotFound, "nothing scheduled as %q", id)
}

// runSchedule fires what is due until the server stops.
func (s *Server) runSchedule(quit <-chan struct{}) {
	t := time.NewTicker(scheduleTick)
	defer t.Stop()
	for {
		s.fireDue(time.Now())
		select {
		case <-quit:
			return
		case <-t.C:
		}
	}
}

// fireDue does what is due at now. The book is not held while an item
// runs: starting a task runs git and can take seconds.
func (s *Server) fireDue(now time.Time) {
	b := s.schedules
	b.mu.Lock()
	var due []proto.ScheduledItem
	for _, it := range b.items {
		if !it.At.After(now) {
			due = append(due, it)
		}
	}
	b.mu.Unlock()
	if len(due) == 0 {
		return
	}
	for _, it := range due {
		next, keep := s.fire(it, now)
		b.mu.Lock()
		for i := range b.items {
			if b.items[i].ID != it.ID {
				continue
			}
			if keep {
				b.items[i] = next
			} else {
				b.items = append(b.items[:i], b.items[i+1:]...)
			}
			break
		}
		b.mu.Unlock()
	}
	b.mu.Lock()
	b.saveLocked()
	list := b.listLocked()
	b.mu.Unlock()
	s.broadcast(proto.EventScheduleChanged, list)
}

// fire runs one item and says what becomes of it: kept (moved on, to try
// again or to repeat) or dropped.
func (s *Server) fire(it proto.ScheduledItem, now time.Time) (proto.ScheduledItem, bool) {
	if it.Queue {
		if running := s.runningAgents(it.Task.ProjectID); running >= it.WIP {
			it.Waiting = fmt.Sprintf("%d agents are running in the project; it starts when fewer than %d are", running, it.WIP)
			return it, true
		}
	}
	if it.Task != nil {
		tp := *it.Task
		if it.EveryMinutes > 0 {
			// Each run is new work on a branch of its own.
			name := tp.Branch
			if name == "" {
				if p, perr := s.projects.get(tp.ProjectID); perr == nil {
					name = gitx.BranchFromPrompt(p.snapshot().Name, tp.Prompt)
				}
			}
			tp.Branch = name + "-" + now.Format("20060102-1504")
			tp.Name = "" // a name names one pane
		}
		info, perr := s.createTask(tp)
		it.LastRun, it.LastError, it.Waiting = now, "", ""
		if perr != nil {
			it.LastError = perr.Message
			log.Printf("schedule %s: task: %s", it.ID, perr.Message)
		} else {
			log.Printf("schedule %s: started task in %s", it.ID, info.ID)
		}
		return repeat(it, now)
	}

	e, perr := s.get(it.Pane)
	if perr != nil {
		log.Printf("schedule %s: pane %s is gone; message dropped", it.ID, it.Pane)
		return it, false
	}
	info := e.info()
	switch {
	case info.State != proto.PaneRunning || info.Agent == nil || (it.Agent != "" && info.Agent.Name != it.Agent) ||
		(!it.PaneCreated.IsZero() && !info.Created.Equal(it.PaneCreated)):
		log.Printf("schedule %s: pane %s no longer runs %s; message dropped", it.ID, it.Pane, it.Agent)
		return it, false
	case info.Agent.State == proto.AgentWorking:
		it.Waiting, it.At = "the agent is working", now.Add(scheduleRetry)
		return it, true
	case info.Agent.State == proto.AgentBlocked:
		it.Waiting, it.At = "the agent is waiting on a question of its own", now.Add(scheduleRetry)
		return it, true
	}
	_, perr = s.promptAgent(proto.AgentPromptParams{ID: it.Pane, Text: it.Text})
	if perr != nil && perr.Code == proto.ErrAgentBlocked {
		it.Waiting, it.At = "the agent is waiting on a question of its own", now.Add(scheduleRetry)
		return it, true
	}
	it.LastRun, it.Waiting, it.LastError = now, "", ""
	if perr != nil {
		it.LastError = perr.Message
		log.Printf("schedule %s: message to %s: %s", it.ID, it.Pane, perr.Message)
	} else {
		log.Printf("schedule %s: sent the message to %s", it.ID, it.Pane)
	}
	return repeat(it, now)
}

// runningAgents counts the agents running in a project — reviewers left
// out, since they review work rather than add to it.
func (s *Server) runningAgents(projectID string) int {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.panes))
	for _, e := range s.panes {
		entries = append(entries, e)
	}
	s.mu.Unlock()
	n := 0
	for _, e := range entries {
		info := e.info()
		agent := info.Agent != nil || e.launched != ""
		if info.State == proto.PaneRunning && agent && info.ProjectID == projectID && !strings.HasPrefix(info.Name, "review · ") {
			n++
		}
	}
	return n
}

// repeat moves a repeating item on past now; a one-off is done.
func repeat(it proto.ScheduledItem, now time.Time) (proto.ScheduledItem, bool) {
	if it.EveryMinutes <= 0 {
		return it, false
	}
	every := time.Duration(it.EveryMinutes) * time.Minute
	for !it.At.After(now) {
		it.At = it.At.Add(every)
	}
	return it, true
}
