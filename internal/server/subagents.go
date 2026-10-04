package server

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// Claude Code runs the agents its Agent tool starts inside its own process,
// with no pane of their own, so the only sign of them is a SubagentStart
// hook and, when they finish, a SubagentStop. A pane keeps the ones still
// running, so the tree can list them under the agent that started them.

// maxSubagents bounds what a pane keeps: a SubagentStop that never comes —
// an agent killed in the middle of a task — must not grow the list for ever.
const maxSubagents = 16

// subagents is what a pane knows of its agent's subagents, guarded by the
// entry's mu.
type subagents struct {
	list []proto.Subagent
	// session is the agent session they were started in: a new one (/clear,
	// a resume) has none of them.
	session string
}

// step applies a hook event: a start adds one, a stop removes it, and the
// session ending or another starting drops them all. Compaction starts the
// same session again and keeps them.
func (sa *subagents) step(rp proto.AgentReportParams, now time.Time) {
	switch rp.Event {
	case "SubagentStart":
		if rp.AgentID == "" {
			return
		}
		if rp.SessionID != "" && rp.SessionID != sa.session {
			sa.list, sa.session = nil, rp.SessionID
		}
		for _, a := range sa.list {
			if a.ID == rp.AgentID {
				return
			}
		}
		sa.list = append(sa.list, proto.Subagent{ID: rp.AgentID, Type: rp.AgentType,
			Description: strings.TrimSpace(rp.TaskDescription), Since: now})
		if n := len(sa.list) - maxSubagents; n > 0 {
			sa.list = append([]proto.Subagent(nil), sa.list[n:]...) // the oldest is likeliest gone
		}
	case "SubagentStop":
		for i, a := range sa.list {
			if a.ID == rp.AgentID {
				sa.list = append(sa.list[:i:i], sa.list[i+1:]...)
				break
			}
		}
	case "SessionEnd":
		sa.list = nil
	case "SessionStart":
		if rp.SessionID != sa.session {
			sa.list, sa.session = nil, rp.SessionID
		}
	}
	if len(sa.list) == 0 {
		sa.list = nil
	}
}

// undescribed lists the subagents whose task is not known yet.
func (sa *subagents) undescribed() []string {
	var ids []string
	for _, a := range sa.list {
		if a.Description == "" {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

// describe fills in the tasks found.
func (sa *subagents) describe(found map[string]string) {
	for i, a := range sa.list {
		if d := found[a.ID]; d != "" && a.Description == "" {
			sa.list[i].Description = d
		}
	}
}

// key says what clients are shown of them, for noticing a change.
func (sa *subagents) key() string {
	var b strings.Builder
	for _, a := range sa.list {
		b.WriteString(a.ID + "\x00" + a.Type + "\x00" + a.Description + "\x00")
	}
	return b.String()
}

// subagentTasks looks up the tasks of the pane's subagents that have none
// yet, the one this report starts included. Claude may write a subagent's
// task only after its SubagentStart, so the next report tries again. The
// caller holds e.evalMu, which guards e.transcript.
func (s *Server) subagentTasks(e *entry, rp proto.AgentReportParams) map[string]string {
	if e.transcript == nil {
		return nil
	}
	e.mu.Lock()
	ids := e.subagents.undescribed()
	e.mu.Unlock()
	if rp.Event == "SubagentStart" && rp.TaskDescription == "" {
		ids = append(ids, rp.AgentID)
	}
	var found map[string]string
	for _, id := range ids {
		if d := subagentTask(e.transcript.Path(), id); d != "" {
			if found == nil {
				found = map[string]string{}
			}
			found[id] = d
		}
	}
	return found
}

// subagentTask reads the task a Claude subagent was given. Claude keeps it
// beside the subagent's transcript, under the session's own:
// <session>.jsonl, then <session>/subagents/agent-<id>.meta.json. The file
// is not documented, so anything missing or unexpected is only no
// description, and the subagent is listed by its type.
func subagentTask(transcript, id string) string {
	if transcript == "" || id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return ""
	}
	dir := strings.TrimSuffix(transcript, ".jsonl")
	if dir == transcript {
		return ""
	}
	f, err := os.Open(filepath.Join(dir, "subagents", "agent-"+id+".meta.json"))
	if err != nil {
		return ""
	}
	defer f.Close()
	var meta struct {
		Description string `json:"description"`
	}
	if json.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&meta) != nil {
		return ""
	}
	return strings.TrimSpace(meta.Description)
}
