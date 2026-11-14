package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/tracker"
)

// Tickets are issues kept in Linear or Jira rather than GitHub. The server
// reaches the tracker, not the client, for the same reason it runs gh for
// an issue: the key is set up on the machine the project is on, in its
// config.toml and environment — and, on a Mac, its Keychain, which is
// where Conch.app keeps such a key for a server it started.

// maxComments caps the comments a prompt is given, as maxIssueBody caps
// the body: the newest are what matter least to a fresh start, and a long
// thread is still there in the tracker.
const maxComments = 8 << 10

// keychainKey reads a tracker key's variable from the Keychain, through the
// same cache the library's tokens use; a variable so a test can say what
// the Keychain holds without timing a security process under load.
var keychainKey = func(s *Server, name string) string { return s.secrets.lookup(name) }

// trackerSecret reads a key's variable: this server's environment, then the
// Keychain.
func (s *Server) trackerSecret(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	if keychainHas() {
		return keychainKey(s, name)
	}
	return ""
}

// openTracker is the tracker for ref (a ticket) or want (a tracker's name,
// for a list), as the project at p — nil for none — is set up, with the
// team or project its tickets are listed from.
func (s *Server) openTracker(p *project, ref, want string) (tracker.Tracker, string, *proto.Error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", proto.Errorf(proto.ErrBadRequest, "config.toml: %v", err)
	}
	name, root := "", ""
	if p != nil {
		name, root = p.snapshot().Name, p.root
	}
	var which, scope string
	if want = strings.ToLower(strings.TrimSpace(want)); want != "" && ref == "" {
		which, scope = want, tracker.Scope(cfg.Trackers, name, root, want)
	} else if which, scope, err = tracker.Choose(cfg.Trackers, name, root, ref, s.trackerSecret); err != nil {
		return nil, "", proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	tr, err := tracker.Open(cfg.Trackers, which, s.trackerSecret)
	if err != nil {
		return nil, "", proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	return tr, scope, nil
}

// ticketProject is the project a ticket method names, nil for none.
func (s *Server) ticketProject(id string) (*project, *proto.Error) {
	if id == "" {
		return nil, nil
	}
	return s.projects.get(id)
}

func trackerCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

func ticketInfo(t tracker.Ticket) proto.TicketInfo {
	out := proto.TicketInfo{Tracker: t.Tracker, Key: t.Key, Title: t.Title, URL: t.URL, Status: t.Status, Body: t.Body}
	for _, c := range t.Comments {
		out.Comments = append(out.Comments, proto.TicketComment{Author: c.Author, Body: c.Body})
	}
	for _, l := range t.Links {
		out.Links = append(out.Links, proto.TicketLink{Title: l.Title, URL: l.URL})
	}
	return out
}

func (s *Server) tickets(tp proto.TicketsParams) (proto.TicketList, *proto.Error) {
	p, perr := s.ticketProject(tp.ProjectID)
	if perr != nil {
		return proto.TicketList{}, perr
	}
	tr, scope, perr := s.openTracker(p, "", tp.Tracker)
	if perr != nil {
		return proto.TicketList{}, perr
	}
	ctx, cancel := trackerCtx()
	defer cancel()
	list, err := tr.List(ctx, tracker.ListOptions{Scope: scope, Assigned: tp.Assigned, Limit: tp.Limit})
	if err != nil {
		return proto.TicketList{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	out := proto.TicketList{Tracker: tr.Name(), Tickets: []proto.TicketInfo{}}
	for _, t := range list {
		out.Tickets = append(out.Tickets, ticketInfo(t))
	}
	return out, nil
}

// readTicket opens the tracker for ref and reads the ticket.
func (s *Server) readTicket(p *project, ref string) (tracker.Tracker, tracker.Ticket, *proto.Error) {
	tr, _, perr := s.openTracker(p, ref, "")
	if perr != nil {
		return nil, tracker.Ticket{}, perr
	}
	_, key, _ := tracker.ParseRef(ref) // Choose has parsed it already
	ctx, cancel := trackerCtx()
	defer cancel()
	t, err := tr.Get(ctx, key)
	if err != nil {
		return nil, tracker.Ticket{}, proto.Errorf(proto.ErrBadRequest, "%s: %v", key, err)
	}
	return tr, t, nil
}

func (s *Server) ticketGet(gp proto.TicketGetParams) (proto.TicketInfo, *proto.Error) {
	p, perr := s.ticketProject(gp.ProjectID)
	if perr != nil {
		return proto.TicketInfo{}, perr
	}
	_, t, perr := s.readTicket(p, gp.Ticket)
	if perr != nil {
		return proto.TicketInfo{}, perr
	}
	return ticketInfo(t), nil
}

// ticketUpdate comments, moves and links, in that order, and stops at the
// first that fails — saying what was done before it, since a tracker has
// no way to take back a comment already posted.
func (s *Server) ticketUpdate(up proto.TicketUpdateParams) (proto.TicketInfo, *proto.Error) {
	if strings.TrimSpace(up.Comment) == "" && strings.TrimSpace(up.Status) == "" && strings.TrimSpace(up.LinkURL) == "" {
		return proto.TicketInfo{}, proto.Errorf(proto.ErrBadRequest, "nothing to change: give a comment, a status or a link")
	}
	p, perr := s.ticketProject(up.ProjectID)
	if perr != nil {
		return proto.TicketInfo{}, perr
	}
	tr, _, perr := s.openTracker(p, up.Ticket, "")
	if perr != nil {
		return proto.TicketInfo{}, perr
	}
	_, key, _ := tracker.ParseRef(up.Ticket)
	ctx, cancel := trackerCtx()
	defer cancel()
	var done []string
	fail := func(what string, err error) (proto.TicketInfo, *proto.Error) {
		if len(done) > 0 {
			return proto.TicketInfo{}, proto.Errorf(proto.ErrBadRequest, "%s: %s, but %s failed: %v", key, strings.Join(done, " and "), what, err)
		}
		return proto.TicketInfo{}, proto.Errorf(proto.ErrBadRequest, "%s: %v", key, err)
	}
	if c := strings.TrimSpace(up.Comment); c != "" {
		if err := tr.Comment(ctx, key, c); err != nil {
			return fail("commenting", err)
		}
		done = append(done, "commented")
	}
	if st := strings.TrimSpace(up.Status); st != "" {
		if _, err := tr.SetStatus(ctx, key, st); err != nil {
			return fail("moving it", err)
		}
		done = append(done, "moved it")
	}
	if u := strings.TrimSpace(up.LinkURL); u != "" {
		if err := tr.Link(ctx, key, u, strings.TrimSpace(up.LinkTitle)); err != nil {
			return fail("linking", err)
		}
		done = append(done, "linked it")
	}
	t, err := tr.Get(ctx, key)
	if err != nil {
		// Done, but not read back: say what is known.
		return proto.TicketInfo{Tracker: tr.Name(), Key: key, Status: up.Status}, nil
	}
	return ticketInfo(t), nil
}

// ticketTask fills a task's prompt and branch from the ticket it names.
func (s *Server) ticketTask(p *project, tp *proto.TaskCreateParams) *proto.Error {
	_, t, perr := s.readTicket(p, tp.Ticket)
	if perr != nil {
		return perr
	}
	tp.Prompt = ticketPrompt(t, tp.Prompt)
	if tp.Branch == "" {
		tp.Branch = gitx.BranchFromPrompt(p.snapshot().Name, t.Key+" "+t.Title)
	}
	return nil
}

// ticketPrompt is what the agent is told about a ticket.
func ticketPrompt(t tracker.Ticket, extra string) string {
	name := tracker.Title(t.Tracker)
	var b strings.Builder
	fmt.Fprintf(&b, "Work on %s ticket %s: %s\n", name, t.Key, strings.TrimSpace(t.Title))
	if t.URL != "" {
		fmt.Fprintf(&b, "%s\n", t.URL)
	}
	body := strings.TrimSpace(t.Body)
	if len(body) > maxIssueBody {
		body = strings.ToValidUTF8(body[:maxIssueBody], "") + "\n\n… (the rest of the ticket is in " + name + ")"
	}
	if body != "" {
		b.WriteString("\n" + body + "\n")
	}
	var comments strings.Builder
	for i, c := range t.Comments {
		text := strings.TrimSpace(c.Body)
		if text == "" {
			continue
		}
		line := "- " + text
		if c.Author != "" {
			line = "- " + c.Author + ": " + text
		}
		if comments.Len()+len(line) > maxComments {
			fmt.Fprintf(&comments, "… (%d more comments are in %s)\n", len(t.Comments)-i, name)
			break
		}
		comments.WriteString(line + "\n")
	}
	if comments.Len() > 0 {
		b.WriteString("\nComments:\n" + comments.String())
	}
	if extra = strings.TrimSpace(extra); extra != "" {
		b.WriteString("\nAlso: " + extra + "\n")
	}
	fmt.Fprintf(&b, "\nWhen it is done, say so in a sentence; a pull request for it should name %s in its title, so %s links the two.", t.Key, name)
	return b.String()
}
