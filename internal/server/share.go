package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sessions"
)

// ---- session.search ----

func (s *Server) searchSessions(p proto.SessionSearchParams) (proto.SessionList, *proto.Error) {
	if strings.TrimSpace(p.Query) == "" {
		return proto.SessionList{}, proto.Errorf(proto.ErrBadRequest, "session.search needs a query")
	}
	dirs, _, perr := s.sessionDirs("session.search", p.ProjectID, p.Dir)
	if perr != nil {
		return proto.SessionList{}, perr
	}
	list, perr := s.listSessions(proto.SessionListParams{ProjectID: p.ProjectID, Dir: p.Dir, Limit: 1000})
	if perr != nil {
		return proto.SessionList{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	env := sessions.CurrentEnv()
	hits := sessions.Match(ctx, env, sessions.List(env, dirs, 1000), p.Query)
	limit := p.Limit
	if limit <= 0 {
		limit = 100
	}
	out := proto.SessionList{Sessions: []proto.SessionInfo{}}
	for _, si := range list.Sessions {
		snip, ok := hits[si.Agent+"|"+si.ID]
		if !ok || si.ID == "" {
			continue
		}
		si.Snippet = snip
		out.Sessions = append(out.Sessions, si)
		if len(out.Sessions) == limit {
			break
		}
	}
	return out, nil
}

// ---- session.share ----

// handoffDir is where shared conversations are written, inside the
// session's checkout so every agent may read them.
const handoffDir = ".conch/handoff"

func (s *Server) shareSession(p proto.SessionShareParams) (proto.SessionShareResult, *proto.Error) {
	var res proto.SessionShareResult
	switch {
	case p.ID == "":
		return res, proto.Errorf(proto.ErrBadRequest, "this run has no saved conversation to share")
	case p.To == "" && p.PaneID == "":
		return res, proto.Errorf(proto.ErrBadRequest, "session.share needs an agent to start or a pane to send to")
	case p.To != "" && p.PaneID != "":
		return res, proto.Errorf(proto.ErrBadRequest, "session.share takes an agent to start or a pane, not both")
	}
	if st, err := os.Stat(p.Dir); err != nil || !st.IsDir() {
		return res, proto.Errorf(proto.ErrBadRequest, "%s no longer exists (a removed worktree?)", p.Dir)
	}

	// Check the receiving end before writing anything.
	var target *entry
	if p.PaneID != "" {
		s.mu.Lock()
		target = s.panes[p.PaneID]
		s.mu.Unlock()
		if target == nil {
			return res, proto.Errorf(proto.ErrNotFound, "no pane %s", p.PaneID)
		}
		info := target.info()
		if info.State != proto.PaneRunning {
			return res, proto.Errorf(proto.ErrBadRequest, "pane %s has exited", p.PaneID)
		}
		if info.Agent == nil {
			return res, proto.Errorf(proto.ErrBadRequest, "pane %s isn't running an agent; typing the prompt into a shell would run it", p.PaneID)
		}
	} else if _, ok := s.adapters.Get(p.To); !ok {
		return res, proto.Errorf(proto.ErrBadRequest, "conch can't launch %q", p.To)
	}

	var found *sessions.Session
	env := sessions.CurrentEnv()
	for _, f := range sessions.List(env, []string{p.Dir}, 0) {
		if f.Agent == p.Agent && f.ID == p.ID {
			found = &f
			break
		}
	}
	if found == nil {
		return res, proto.Errorf(proto.ErrNotFound, "no %s session %s in %s", p.Agent, p.ID, p.Dir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	turns, err := sessions.Transcript(ctx, env, *found)
	if err != nil {
		return res, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if len(turns) == 0 {
		return res, proto.Errorf(proto.ErrBadRequest, "the %s session has no messages to share", sessions.AgentName(p.Agent))
	}
	path, err := writeHandoff(p.Dir, *found, sessions.Handoff(*found, turns))
	if err != nil {
		return res, proto.Errorf(proto.ErrInternal, "write the conversation: %v", err)
	}
	res.Path = path

	if target != nil {
		ref := path
		if rel, err := filepath.Rel(target.dir, path); err == nil && !strings.HasPrefix(rel, "..") {
			ref = rel
		}
		if err := s.submitPrompt(target, handoffPrompt(p.Agent, ref)); err != nil {
			return res, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		res.Pane = target.info()
		log.Printf("shared %s session %s with pane %s (%s)", p.Agent, p.ID, p.PaneID, path)
		return res, nil
	}
	info, perr := s.create(proto.PaneCreateParams{Agent: p.To, Prompt: handoffPrompt(p.Agent, filepath.Join(handoffDir, filepath.Base(path))),
		Cwd: p.Dir, Cols: p.Cols, Rows: p.Rows})
	if perr != nil {
		return res, perr
	}
	res.Pane = info
	log.Printf("shared %s session %s with %s in pane %s (%s)", p.Agent, p.ID, p.To, info.ID, path)
	return res, nil
}

// submitDelay separates a pasted message from the Enter that submits it:
// agents take an Enter inside a paste as a newline.
const submitDelay = 150 * time.Millisecond

// submitPrompt pastes text into an agent's input and submits it.
func (s *Server) submitPrompt(e *entry, text string) error {
	if err := e.p.SendText(text, true); err != nil {
		return err
	}
	time.AfterFunc(submitDelay, func() { _ = e.p.SendKeys([]string{"enter"}) })
	s.userInput(e)
	return nil
}

// ---- agent.broadcast ----

func (s *Server) broadcastMessage(p proto.AgentBroadcastParams) (proto.AgentBroadcastResult, *proto.Error) {
	res := proto.AgentBroadcastResult{Results: []proto.BroadcastOutcome{}}
	if strings.TrimSpace(p.Text) == "" {
		return res, proto.Errorf(proto.ErrBadRequest, "agent.broadcast needs a message")
	}
	if len(p.IDs) == 0 {
		return res, proto.Errorf(proto.ErrBadRequest, "agent.broadcast needs panes to send to")
	}
	text := strings.TrimSpace(p.Text)
	seen := map[string]bool{}
	sent := 0
	for _, id := range p.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		out := proto.BroadcastOutcome{ID: id}
		s.mu.Lock()
		e := s.panes[id]
		s.mu.Unlock()
		switch info := entryInfo(e); {
		case e == nil:
			out.Error = "no such pane"
		case info.State != proto.PaneRunning:
			out.Error = "exited"
		case info.Agent == nil:
			out.Error = "not running an agent"
		default:
			if err := s.submitPrompt(e, text); err != nil {
				out.Error = err.Error()
			} else {
				out.Sent = true
				sent++
			}
		}
		res.Results = append(res.Results, out)
	}
	log.Printf("broadcast to %d of %d pane(s)", sent, len(res.Results))
	return res, nil
}

func entryInfo(e *entry) proto.PaneInfo {
	if e == nil {
		return proto.PaneInfo{}
	}
	return e.info()
}

// handoffPrompt asks an agent to pick up a shared conversation.
func handoffPrompt(from, path string) string {
	return fmt.Sprintf("Continue the work from an earlier %s session. Its conversation is saved in %s. "+
		"Read that file first, then briefly summarise what was done and what remains before changing anything.",
		sessions.AgentName(from), path)
}

// writeHandoff saves a handoff document in dir and keeps .conch out of git.
func writeHandoff(dir string, s sessions.Session, doc string) (string, error) {
	folder := filepath.Join(dir, handoffDir)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	name := s.Agent + "-" + safeName(s.ID) + ".md"
	path := filepath.Join(folder, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(doc), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := excludeFromGit(dir, ".conch/"); err != nil {
		log.Printf("handoff: keeping .conch out of git in %s: %v", dir, err)
	}
	return path, nil
}

// safeName keeps an ID usable as a file name.
func safeName(id string) string {
	b := []byte(id)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			b[i] = '_'
		}
	}
	if len(b) == 0 || string(b) == "." || string(b) == ".." {
		return "session"
	}
	return string(b)
}

// excludeFromGit adds pattern to the repository's local exclude file
// (.git/info/exclude, never committed) when dir is in a git checkout.
func excludeFromGit(dir, pattern string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--git-path", "info/exclude")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil // not a git checkout
		}
		return err
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		pattern = "\n" + pattern
	}
	_, err = f.WriteString(pattern + "\n")
	return err
}
