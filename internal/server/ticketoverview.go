package server

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/tracker"
)

// Tickets were seen a project at a time: New Task's picker, or conch
// ticket ls in one checkout. The overview is every project's open tickets
// in one answer — its GitHub issues, and the Linear or Jira tickets its
// [[trackers.projects]] entry names — each with what conch is doing about
// it, which the server already knows and nobody had joined: a task for it
// queued, a branch named for it with its pull request, an agent on that
// branch. A tracker set up with no project entry at all is a group of its
// own, so setting up Linear alone still shows something.

// overviewLimit is the tickets a source lists unless asked otherwise.
const overviewLimit = 50

// overviewWorkers bounds the projects read at once: each is a gh call or
// two and a tracker request.
const overviewWorkers = 4

func (s *Server) ticketOverview(op proto.TicketOverviewParams) (proto.TicketOverview, *proto.Error) {
	cfg, err := config.Load()
	if err != nil {
		return proto.TicketOverview{}, proto.Errorf(proto.ErrBadRequest, "config.toml: %v", err)
	}
	limit := op.Limit
	if limit <= 0 || limit > proto.TicketCountMax {
		limit = overviewLimit
	}
	want := map[string]bool{}
	for _, id := range op.ProjectIDs {
		want[id] = true
	}
	var ps []*project
	for _, info := range s.projects.list() {
		if len(want) > 0 && !want[info.ID] {
			continue
		}
		if p, perr := s.projects.gitProject(info.ID); perr == nil {
			ps = append(ps, p)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// gh's login once, for which issues are yours.
	login := ""
	if len(ps) > 0 {
		login, _ = ghx.Whoami(ctx, ps[0].root)
	}
	sched := s.schedules.list().Items
	panes := s.list()

	out := proto.TicketOverview{Projects: make([]proto.TicketProject, len(ps))}
	sem := make(chan struct{}, overviewWorkers)
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Add(1)
		go func(i int, p *project) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out.Projects[i] = s.projectTickets(ctx, cfg.Trackers, p, login, limit, sched, panes)
		}(i, p)
	}
	wg.Wait()
	if len(want) == 0 && len(cfg.Trackers.Projects) == 0 {
		// A tracker set up for no project in particular: its own group.
		for _, name := range tracker.Configured(cfg.Trackers, s.trackerSecret) {
			g := proto.TicketProject{Name: tracker.Title(name), Tickets: []proto.TicketRow{}}
			tr, err := tracker.Open(cfg.Trackers, name, s.trackerSecret)
			s.trackerTickets(ctx, tr, err, name, tracker.Scope(cfg.Trackers, "", "", name), nil, limit, sched, panes, &g)
			out.Projects = append(out.Projects, g)
		}
	}
	if len(want) == 0 {
		out.Projects = append(out.Projects, s.unboundFileTickets(ctx, cfg.Trackers, limit, sched, panes)...)
	}
	return out, nil
}

// unboundFileTickets are the tickets integrations set up — their key
// there — that belong to no repository and no [[trackers.projects]]
// entry names: a group each, as Linear on its own is.
func (s *Server) unboundFileTickets(ctx context.Context, cfg config.TrackersCfg, limit int, sched []proto.ScheduledItem, panes []proto.PaneInfo) []proto.TicketProject {
	named := map[string]bool{}
	for _, e := range cfg.Projects {
		named[strings.ToLower(strings.TrimSpace(e.Tracker))] = true
	}
	var out []proto.TicketProject
	for _, f := range integration.Cached() {
		if f.Kind != integration.KindTickets || f.Project.ID != "" || named[f.Name] || s.trackerSecret(f.KeyEnv) == "" {
			continue
		}
		g := proto.TicketProject{Name: f.DisplayName(), Tickets: []proto.TicketRow{}}
		tr, err := s.openFileTracker(ctx, f, "")
		s.trackerTickets(ctx, tr, err, f.Name, "", nil, limit, sched, panes, &g)
		out = append(out, g)
	}
	return out
}

// projectTickets is one project's GitHub issues and its tracker's tickets.
func (s *Server) projectTickets(ctx context.Context, cfg config.TrackersCfg, p *project, login string, limit int, sched []proto.ScheduledItem, panes []proto.PaneInfo) proto.TicketProject {
	info := p.snapshot()
	g := proto.TicketProject{ProjectID: p.id, Name: info.Name, Tickets: []proto.TicketRow{}}
	rows, err := ghx.IssueRows(ctx, p.root, limit)
	switch {
	case err == nil:
		g.Sources = append(g.Sources, "github")
		for _, r := range rows {
			t := proto.TicketRow{Source: "github", Key: fmt.Sprint(r.Number), Number: r.Number, Title: r.Title, URL: r.URL, Status: "open", Labels: r.Labels}
			for _, a := range r.Assignees {
				t.Mine = t.Mine || (login != "" && a == login)
			}
			t.Doing = doingFor(info, p.id, func(it proto.TaskCreateParams) bool { return it.Issue == r.Number },
				issueBranch(r.Number), sched, panes)
			g.Tickets = append(g.Tickets, t)
		}
	case errors.Is(err, ghx.ErrNoGH):
		g.Errors = append(g.Errors, err.Error())
	case ghx.NoRepository(err):
		// No GitHub remote: no issues, and nothing wrong.
	default:
		g.Errors = append(g.Errors, "GitHub: "+err.Error())
	}
	tied := ""
	if e, ok := cfg.ForProject(info.Name, p.root); ok {
		name := strings.ToLower(strings.TrimSpace(e.Tracker))
		tied = name
		area := e.Key
		if area == "" {
			area = tracker.Scope(cfg, info.Name, p.root, name)
		}
		var tr tracker.Tracker
		var err error
		if f := ticketFile(name); f != nil {
			tr, err = s.openFileTracker(ctx, f, p.root)
		} else {
			tr, err = tracker.Open(cfg, name, s.trackerSecret)
		}
		s.trackerTickets(ctx, tr, err, name, area, &info, limit, sched, panes, &g)
	}
	// A tracker the repository itself shows it uses — GitLab's issues for
	// a GitLab remote — once its key is set; without the key it is not
	// set up, and saying so for every such project would be noise.
	rem, _ := integration.ParseRemote(gitx.RemoteURL(ctx, p.root))
	for _, f := range integration.Cached() {
		if f.Kind != integration.KindTickets || f.Name == tied || !f.Uses(p.root, rem) || s.trackerSecret(f.KeyEnv) == "" {
			continue
		}
		tr, err := s.openFileTracker(ctx, f, p.root)
		s.trackerTickets(ctx, tr, err, f.Name, "", &info, limit, sched, panes, &g)
	}
	return g
}

// trackerTickets adds the open tickets in area of tr — the tracker name
// opened, or why it could not be — to g.
func (s *Server) trackerTickets(ctx context.Context, tr tracker.Tracker, err error, name, area string, info *proto.ProjectInfo, limit int, sched []proto.ScheduledItem, panes []proto.PaneInfo, g *proto.TicketProject) {
	// tickets is never null on the wire: Conch.app reads an array, and a
	// null fails the whole Tickets window rather than this group.
	if g.Tickets == nil {
		g.Tickets = []proto.TicketRow{}
	}
	src := name
	if area != "" {
		src += " · " + area
	}
	g.Sources = append(g.Sources, src)
	if err != nil {
		g.Errors = append(g.Errors, err.Error())
		return
	}
	list, err := tr.List(ctx, tracker.ListOptions{Scope: area, Limit: limit})
	if err != nil {
		g.Errors = append(g.Errors, areaError(name, area, err))
		return
	}
	mine := map[string]bool{}
	if ms, err := tr.List(ctx, tracker.ListOptions{Scope: area, Assigned: true, Limit: limit}); err == nil {
		for _, t := range ms {
			mine[t.Key] = true
		}
	}
	var pi proto.ProjectInfo
	id := ""
	if info != nil {
		pi, id = *info, g.ProjectID
	}
	for _, t := range list {
		key := t.Key
		row := proto.TicketRow{Source: name, Key: wireKey(name, key), Title: t.Title, URL: t.URL, Status: t.Status, Mine: mine[key]}
		if id != "" {
			row.Doing = doingFor(pi, id, func(it proto.TaskCreateParams) bool {
				return strings.EqualFold(it.Ticket, key) || strings.HasSuffix(strings.ToLower(it.Ticket), ":"+strings.ToLower(key))
			},
				keyBranch(key), sched, panes)
		}
		g.Tickets = append(g.Tickets, row)
	}
}

// issueBranch matches the branches an issue task names (conch/issue-12-…,
// and its attempts beneath it), as intake does.
func issueBranch(n int) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(^|/)issue-%d(-|/|$)`, n))
}

// keyBranch matches the branches a ticket task names: its key, lower case,
// at the start of a part of the name (conch/eng-142-retry-uploads).
func keyBranch(key string) *regexp.Regexp {
	return regexp.MustCompile(`(^|/)` + regexp.QuoteMeta(strings.ToLower(key)) + `(-|/|$)`)
}

// doingFor is what conch is doing about a ticket: its agent's state, its
// pull request, a task for it queued, in that order.
func doingFor(info proto.ProjectInfo, projectID string, isTask func(proto.TaskCreateParams) bool, branch *regexp.Regexp, sched []proto.ScheduledItem, panes []proto.PaneInfo) proto.TicketDoing {
	var d proto.TicketDoing
	for _, it := range sched {
		if it.Task != nil && it.Task.ProjectID == projectID && isTask(*it.Task) {
			d.State, d.Schedule, d.Note = proto.DoingQueued, it.ID, it.Waiting
			break
		}
	}
	for _, br := range info.Branches {
		if !branch.MatchString(br.Name) {
			continue
		}
		d.Branch = br.Name
		if br.PR != nil {
			pr := *br.PR
			// An open pull request is what the ticket is waiting on: a
			// merged or closed one on another of its branches does not
			// replace it, or the state said "pr" beside a merged one.
			if pr.State == "OPEN" || d.PR == nil || d.PR.State != "OPEN" {
				d.PR = &pr
			}
			switch pr.State {
			case "OPEN":
				d.State = proto.DoingPR
			case "MERGED":
				if d.State == "" {
					d.State = proto.DoingMerged
				}
			}
		}
		for _, pane := range panes {
			if pane.ProjectID != projectID || pane.Branch != br.Name || pane.Agent == nil || pane.State != proto.PaneRunning || proto.IsReviewer(pane) {
				continue
			}
			d.Pane = pane.ID
			switch pane.Agent.State {
			case proto.AgentBlocked:
				d.State = proto.DoingWaiting
			case proto.AgentWorking:
				d.State = proto.DoingWorking
			default:
				if d.State == "" || d.State == proto.DoingMerged {
					d.State = proto.DoingIdle
				}
			}
		}
		if d.State == proto.DoingWaiting || d.State == proto.DoingWorking {
			break
		}
	}
	return d
}
