package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/tracker"
)

// Integration files of the tickets and notify kinds, used where conch's
// own trackers and notifications are: a tickets file is a tracker beside
// Linear and Jira, named NAME:KEY (shortcut:123) wherever a ticket is
// taken, and a notify file is sent what the desktop is told. Their keys —
// a webhook's address, for a notify file — are read where every key is
// (this server's environment, then the Keychain) and never logged.

// connectFile readies an integration file with its key, the user for basic
// auth and the address the person gave for base_url = "ask"; why says what
// is missing when it cannot be.
func (s *Server) connectFile(f *integration.File, bases map[string]string) (*integration.Conn, string) {
	user := ""
	if f.Auth.UserEnv != "" {
		user = s.trackerSecret(f.Auth.UserEnv)
	}
	base := ""
	if f.BaseURL == "ask" {
		base = bases[f.Name]
		if base == "" && f.API == "" {
			return nil, fmt.Sprintf("give its API's address in config.toml, [integrations.base] %s = \"https://…\"", f.Name)
		}
	}
	conn, err := f.Connect(s.trackerSecret(f.KeyEnv), user, base)
	switch {
	case errors.Is(err, integration.ErrNoKey):
		return nil, fmt.Sprintf("set $%s where conch's server runs, or in the Keychain (service conch-mcp, account %s)", f.KeyEnv, f.KeyEnv)
	case err != nil:
		return nil, err.Error()
	}
	return conn, ""
}

// ticketFile is the tickets integration named, nil for none.
func ticketFile(name string) *integration.File {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == tracker.Linear || name == tracker.Jira {
		return nil
	}
	for _, f := range integration.Cached() {
		if f.Kind == integration.KindTickets && f.Name == name {
			return f
		}
	}
	return nil
}

// fileRef reads a ticket given as NAME:KEY for a tickets integration.
func fileRef(ref string) (*integration.File, string, bool) {
	name, key, ok := strings.Cut(strings.TrimSpace(ref), ":")
	key = strings.TrimSpace(key)
	if !ok || key == "" || strings.HasPrefix(key, "//") {
		return nil, "", false // an address, https://…
	}
	f := ticketFile(name)
	return f, key, f != nil
}

// trackerFile says whether a ticket method is for a tickets integration
// rather than Linear or Jira, and which, with the ticket's key: ref's
// NAME:, the tracker a list asks for, or the one the project is tied to
// by [[trackers.projects]] — whose keys are then taken alone.
func trackerFile(cfg config.TrackersCfg, project, root, ref, want string) (*integration.File, string, bool) {
	if ref == "" && want != "" {
		f := ticketFile(want)
		return f, "", f != nil
	}
	if f, key, ok := fileRef(ref); ok {
		return f, key, true
	}
	if strings.Contains(ref, ":") || want != "" {
		return nil, "", false // linear:ENG-1, an address, or a tracker of conch's own
	}
	if e, ok := cfg.ForProject(project, root); ok {
		if f := ticketFile(e.Tracker); f != nil {
			return f, strings.TrimSpace(ref), true
		}
	}
	return nil, "", false
}

// openFileTracker is a tickets integration as a tracker, for the project
// checked out at root ("" for none).
func (s *Server) openFileTracker(ctx context.Context, f *integration.File, root string) (tracker.Tracker, error) {
	var bases map[string]string
	if cfg, err := config.Load(); err == nil {
		bases = cfg.Integrations.Base
	}
	var rem integration.Remote
	if root != "" {
		rem, _ = integration.ParseRemote(gitx.RemoteURL(ctx, root))
	}
	if f.Project.ID != "" && rem.Host == "" {
		return nil, fmt.Errorf("%s: its tickets belong to a repository, and this project has no remote to name it by", f.DisplayName())
	}
	conn, why := s.connectFile(f, bases)
	if conn == nil {
		return nil, fmt.Errorf("%s: %s", f.DisplayName(), why)
	}
	return conn.Tracker(integration.Target{Remote: rem}), nil
}

// trackerTitle is a tracker's name as people write it: Linear, Jira, or a
// tickets integration's label.
func trackerTitle(name string) string {
	if f := ticketFile(name); f != nil {
		return f.DisplayName()
	}
	return tracker.Title(name)
}

// wireKey is a ticket's key as the protocol carries it: NAME:KEY for a
// tickets integration, so that whatever a client hands back — a task's
// ticket, a row's — names the tracker as well as the ticket. Linear's and
// Jira's keys say which already.
func wireKey(tr, key string) string {
	if ticketFile(tr) == nil || key == "" {
		return key
	}
	return tr + ":" + key
}

// notifyTimeout bounds one notification: a webhook that never answers
// must not hold a goroutine for ever.
const notifyTimeout = 30 * time.Second

// notifyFiles sends m through every notify integration whose key is set,
// each in the background. A file without its key is not set up, which is
// not worth a line in the log; a send that fails is, without the address.
func (s *Server) notifyFiles(m integration.Message) {
	var bases map[string]string
	if cfg, err := config.Load(); err == nil {
		bases = cfg.Integrations.Base
	}
	for _, f := range integration.Cached() {
		if f.Kind != integration.KindNotify {
			continue
		}
		conn, why := s.connectFile(f, bases)
		if conn == nil {
			if s.trackerSecret(f.KeyEnv) != "" {
				log.Printf("notify %s: %s", f.Name, why)
			}
			continue
		}
		s.notifyWG.Add(1)
		goSafe("notify", func() {
			defer s.notifyWG.Done()
			ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
			defer cancel()
			if err := conn.Send(ctx, m); err != nil {
				log.Printf("notify %s: %v", f.Name, err)
			}
		})
	}
}

// notifyAgent sends an agent's start of waiting, or its finish, as the
// desktop is told them: while [notify] is enabled and says so for that
// kind of change. A pane adopted after a reload comes back in a state it
// was already in, which is no news.
func (s *Server) notifyAgent(info proto.PaneInfo, prev string) {
	a := info.Agent
	if a == nil || prev == "" || a.State == prev || proto.IsReviewer(info) {
		return
	}
	waiting := a.State == proto.AgentBlocked
	if !waiting && !finishedTurn(prev, a.State) {
		return
	}
	cfg, err := config.Load()
	if err != nil || !cfg.Notify.Enabled || waiting && !cfg.Notify.Waiting || !waiting && !cfg.Notify.Done {
		return
	}
	var body string
	switch {
	case waiting && a.Message != "":
		body = info.DisplayName() + " is waiting: " + a.Message
	case waiting:
		body = info.DisplayName() + " is waiting for you"
	case a.Failed:
		body = info.DisplayName() + " stopped: " + a.Message
	default:
		body = info.DisplayName() + " finished"
	}
	m := integration.Message{Title: "conch · " + a.Name, Body: body}
	if info.ProjectID != "" {
		if p, perr := s.projects.get(info.ProjectID); perr == nil {
			pi := p.snapshot()
			m.Body += " (" + pi.Name
			if info.Branch != "" {
				m.Body += ", " + info.Branch
			}
			m.Body += ")"
			for _, b := range pi.Branches {
				if b.Name == info.Branch && b.PR != nil {
					m.URL = b.PR.URL
				}
			}
		}
	}
	s.notifyFiles(m)
}

// notifyMerged sends a pull request the autopilot merged.
func (s *Server) notifyMerged(project, branch string, number int, url string) {
	cfg, err := config.Load()
	if err != nil || !cfg.Notify.Enabled {
		return
	}
	s.notifyFiles(integration.Message{Title: "conch · merged",
		Body: fmt.Sprintf("#%d %s merged in %s, green", number, branch, project), URL: url})
}
