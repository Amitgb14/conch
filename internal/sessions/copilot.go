package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ---- GitHub Copilot CLI ----
//
// Copilot keeps each session in a folder of its own,
// <COPILOT_HOME or ~/.copilot>/session-state/<session id>/, which is not
// filed by project. Its events.jsonl is the session, one event a line —
// {id, parentId, timestamp, type, data} — beginning with session.start,
// whose data says where it ran (context.cwd, context.branch); what was
// typed is a user.message's data.content, and what came back an
// assistant.message's (copilot-sdk/generated/session-events.d.ts in the
// npm package, 1.0.112). The folder holds the rest of the session too —
// a plan, checkpoints, a database of its own — so a delete moves the
// folder.

func copilotHome(e Env) string {
	if d := e.get("COPILOT_HOME"); d != "" {
		return d
	}
	return filepath.Join(e.Home, ".copilot")
}

func copilot(e Env, dirs []string) []Session {
	files, _ := filepath.Glob(filepath.Join(copilotHome(e), "session-state", "*", "events.jsonl"))
	var out []Session
	for _, f := range files {
		if s := parseCached(f, parseCopilot); s != nil && withinAny(s.Dir, dirs) {
			out = append(out, *s)
		}
	}
	return out
}

// copilotEvent is one line of events.jsonl, with the fields conch reads.
type copilotEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Data      struct {
		SessionID string    `json:"sessionId"`
		StartTime time.Time `json:"startTime"`
		Context   struct {
			Cwd    string `json:"cwd"`
			Branch string `json:"branch"`
		} `json:"context"`
		Content string `json:"content"`
		// Source says where a user message came from when it was not
		// typed: a skill's hidden prompt, another agent.
		Source string `json:"source"`
	} `json:"data"`
}

func parseCopilot(path string, st os.FileInfo) *Session {
	if time.Since(st.ModTime()) > maxAge {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	s := &Session{Agent: "copilot", ID: filepath.Base(filepath.Dir(path)), Updated: st.ModTime(), Path: path}
	lines(f, 400, func(b []byte) bool {
		var ev copilotEvent
		if json.Unmarshal(b, &ev) != nil {
			return true
		}
		switch ev.Type {
		case "session.start":
			if ev.Data.SessionID != "" {
				s.ID = ev.Data.SessionID
			}
			s.Dir, s.Branch, s.Started = ev.Data.Context.Cwd, ev.Data.Context.Branch, ev.Data.StartTime
			if s.Started.IsZero() {
				s.Started = ev.Timestamp
			}
		case "user.message":
			if ev.Data.Source == "" && realPrompt(ev.Data.Content) {
				s.Title = title(ev.Data.Content)
				return false
			}
		}
		return true
	})
	if s.Dir == "" || s.Title == "" {
		return nil // nothing was said: not worth resuming
	}
	return s
}

func copilotTranscript(path string) ([]Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var turns []Turn
	lines(f, 1<<22, func(b []byte) bool {
		var ev copilotEvent
		if json.Unmarshal(b, &ev) != nil {
			return true
		}
		switch {
		case ev.Type == "user.message" && ev.Data.Source == "":
			turns = addTurn(turns, "user", ev.Data.Content, ev.Timestamp)
		case ev.Type == "assistant.message":
			// An answer split at a reasoning boundary comes as several
			// messages; addTurn makes them one.
			turns = addTurn(turns, "assistant", ev.Data.Content, ev.Timestamp)
		}
		return true
	})
	return turns, nil
}
