package server

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
)

// maxCILog is how much of a failed run's log is sent back: the end, where
// the error is. A log of a big test suite runs to megabytes.
const maxCILog = 64 << 10

// ciLog reads what a branch's failed CI run printed, so the client can
// hand it to the branch's agent: GitHub Actions through gh on this
// machine — the one with the checkout and the login — and, when Actions
// has no failure, the CI integrations the project uses (CircleCI, GitLab
// CI, …), with their keys from this server's environment or Keychain.
func (pm *projectManager) ciLog(lp proto.BranchCILogParams) (proto.BranchCILog, *proto.Error) {
	p, perr := pm.gitProject(lp.ProjectID)
	if perr != nil {
		return proto.BranchCILog{}, perr
	}
	if lp.Branch == "" {
		return proto.BranchCILog{}, proto.Errorf(proto.ErrBadRequest, "no branch given")
	}
	p.mu.Lock()
	pr := p.prs[lp.Branch]
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	run, err := ghx.FailedLog(ctx, p.root, lp.Branch, pr.Head)
	switch {
	case err == nil:
		return proto.BranchCILog{Workflow: run.Workflow, URL: run.URL, Log: tail(run.Log, maxCILog),
			Source: integration.SourceActions, Service: "GitHub Actions"}, nil
	case !errors.Is(err, ghx.ErrNoFailedRun):
		return proto.BranchCILog{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	notes := []string{fmt.Sprintf("no failed GitHub Actions run on %s", lp.Branch)}
	for _, ci := range pm.ciFor(ctx, p, lp.Branch, pr) {
		lg, note := ci.failedLog(ctx)
		if note == "" {
			return lg, nil
		}
		notes = append(notes, note)
	}
	notes = append(notes, unreadable(pr.Runs)...)
	if len(notes) == 1 {
		// Nothing else to say: today's message, word for word.
		return proto.BranchCILog{}, proto.Errorf(proto.ErrNotFound,
			"no failed GitHub Actions run on %s: the failing check is another service's, or it was re-run — open the pull request to see it", lp.Branch)
	}
	return proto.BranchCILog{}, proto.Errorf(proto.ErrNotFound, "%s", strings.Join(notes, "; "))
}

// branchCIRerun re-runs what failed in a branch's newest failed run: on GitHub
// Actions through gh, else through the first CI integration with one.
func (pm *projectManager) branchCIRerun(lp proto.BranchCILogParams) (proto.BranchCIRerun, *proto.Error) {
	p, perr := pm.gitProject(lp.ProjectID)
	if perr != nil {
		return proto.BranchCIRerun{}, perr
	}
	if lp.Branch == "" {
		return proto.BranchCIRerun{}, proto.Errorf(proto.ErrBadRequest, "no branch given")
	}
	p.mu.Lock()
	pr := p.prs[lp.Branch]
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	run, err := ghx.RerunFailed(ctx, p.root, lp.Branch, pr.Head)
	switch {
	case err == nil:
		return proto.BranchCIRerun{Source: integration.SourceActions, Service: "GitHub Actions", Run: fmt.Sprint(run.ID), URL: run.URL}, nil
	case !errors.Is(err, ghx.ErrNoFailedRun):
		return proto.BranchCIRerun{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	notes := []string{fmt.Sprintf("no failed GitHub Actions run on %s", lp.Branch)}
	for _, ci := range pm.ciFor(ctx, p, lp.Branch, pr) {
		r, note := ci.rerun(ctx)
		if note == "" {
			return r, nil
		}
		notes = append(notes, note)
	}
	notes = append(notes, unreadable(pr.Runs)...)
	return proto.BranchCIRerun{}, proto.Errorf(proto.ErrNotFound, "%s", strings.Join(notes, "; "))
}

// ciSource is one CI integration, ready to ask about a branch.
type ciSource struct {
	f      *integration.File
	conn   *integration.Conn
	target integration.Target
	// why says what is wrong when conn is nil.
	why string
}

func (ci ciSource) label() string { return ci.f.DisplayName() }

// ciFor is the CI integrations to ask about branch: those the checkout or
// its remote shows it uses, and those a check of its pull request came
// from — in name order, each connected with its key or saying why not.
func (pm *projectManager) ciFor(ctx context.Context, p *project, branch string, pr proto.PRInfo) []ciSource {
	rem, _ := integration.ParseRemote(gitx.RemoteURL(ctx, p.root))
	files := integration.Cached()
	var bases map[string]string
	if cfg, err := config.Load(); err == nil {
		bases = cfg.Integrations.Base
	}
	use := map[string]*integration.File{}
	for _, f := range files {
		if f.Kind == integration.KindCI && f.Uses(p.root, rem) {
			use[f.Name] = f
		}
	}
	for _, c := range pr.Runs {
		if src, ok := integration.CheckSource(c.Name, c.URL, files); ok {
			for _, f := range files {
				// A deploy file's check is read by deployFor.
				if f.Name == src && f.Kind == integration.KindCI {
					use[f.Name] = f
				}
			}
		}
	}
	names := make([]string, 0, len(use))
	for n := range use {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []ciSource
	for _, n := range names {
		f := use[n]
		ci := ciSource{f: f, target: integration.Target{Remote: rem, Branch: branch, Head: pr.Head}}
		switch {
		case rem.Host == "":
			ci.why = "the project has no remote to name it by"
		default:
			ci.conn, ci.why = pm.s.connectFile(f, bases)
		}
		out = append(out, ci)
	}
	return out
}

// failedRun is the newest failed run at the pull request's head.
func (ci ciSource) failedRun(ctx context.Context) (integration.Run, string) {
	if ci.conn == nil {
		return integration.Run{}, ci.label() + ": " + ci.why
	}
	runs, err := ci.conn.Runs(ctx, ci.target)
	if err != nil {
		return integration.Run{}, ci.label() + ": " + err.Error()
	}
	for _, r := range runs {
		if r.State == "fail" && (ci.target.Head == "" || r.Commit == "" || r.Commit == ci.target.Head) {
			return r, ""
		}
	}
	return integration.Run{}, fmt.Sprintf("%s: no failed run on %s", ci.label(), ci.target.Branch)
}

func (ci ciSource) failedLog(ctx context.Context) (proto.BranchCILog, string) {
	run, note := ci.failedRun(ctx)
	if note != "" {
		return proto.BranchCILog{}, note
	}
	lg, err := ci.conn.FailedLog(ctx, ci.target, run.ID)
	switch {
	case errors.Is(err, integration.ErrNothing):
		return proto.BranchCILog{}, fmt.Sprintf("%s: run %s failed and left no log of a failed job", ci.label(), firstNonEmpty(run.Name, run.ID))
	case err != nil:
		return proto.BranchCILog{}, ci.label() + ": " + err.Error()
	}
	return proto.BranchCILog{Workflow: run.Name, URL: run.URL, Log: tail(lg.Text, maxCILog),
		Source: ci.f.Name, Service: ci.label(), Job: lg.Job}, ""
}

func (ci ciSource) rerun(ctx context.Context) (proto.BranchCIRerun, string) {
	if !ci.f.Rerun.Has() {
		return proto.BranchCIRerun{}, ci.label() + ": its integration cannot re-run"
	}
	run, note := ci.failedRun(ctx)
	if note != "" {
		return proto.BranchCIRerun{}, note
	}
	if err := ci.conn.Rerun(ctx, ci.target, run.ID); err != nil {
		return proto.BranchCIRerun{}, ci.label() + ": " + err.Error()
	}
	return proto.BranchCIRerun{Source: ci.f.Name, Service: ci.label(), Run: run.ID, URL: run.URL}, ""
}

// unreadable says, for each failing check of a service conch knows but
// has no integration for, what would read it.
func unreadable(checks []proto.PRCheck) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range checks {
		if c.State != "fail" || c.Readable || c.Source == "" || c.Source == integration.SourceActions || seen[c.Source] {
			continue
		}
		seen[c.Source] = true
		out = append(out, fmt.Sprintf("%s: install its integration to read its log", integration.Label(c.Source, nil)))
	}
	return out
}

// tail is the end of s, at most n bytes.
func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// integrationsOf is the services a project uses: what its checkout, its
// remote and its pull requests' checks show, with whether something here
// reads each and whether its key is set where this server runs.
func (pm *projectManager) integrationsOf(projectID string) (proto.IntegrationDetect, *proto.Error) {
	p, perr := pm.gitProject(projectID)
	if perr != nil {
		return proto.IntegrationDetect{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rem, _ := integration.ParseRemote(gitx.RemoteURL(ctx, p.root))
	var checks []integration.CheckRef
	p.mu.Lock()
	for _, pr := range p.prs {
		for _, c := range pr.Runs {
			checks = append(checks, integration.CheckRef{Name: c.Name, URL: c.URL})
		}
	}
	p.mu.Unlock()
	files := integration.Cached()
	out := proto.IntegrationDetect{Integrations: []proto.IntegrationUse{}, Team: teamOf(p.root)}
	found := integration.Detected(p.root, rem, checks, files)
	if out.Team != nil && out.Team.State == "used" {
		found = integration.WithTeam(found, out.Team.Names, files)
	}
	for _, d := range found {
		u := proto.IntegrationUse{Name: d.Name, Label: d.Label, Kind: d.Kind, Why: d.Why, Installed: d.Installed, Team: d.Why == integration.TeamFile}
		switch d.Name {
		case "github", integration.SourceActions:
			u.Connected = true // through gh, whose login is its own
		default:
			for _, f := range files {
				if f.Name == d.Name {
					u.KeyEnv = f.KeyEnv
					u.Connected = pm.s.trackerSecret(f.KeyEnv) != ""
				}
			}
		}
		out.Integrations = append(out.Integrations, u)
	}
	for _, u := range pm.settingsUses(p.snapshot().Name, p.root) {
		// Settings know a tracker's key and scope better than a name in
		// the team file does.
		if i := slices.IndexFunc(out.Integrations, func(o proto.IntegrationUse) bool { return o.Name == u.Name }); i >= 0 {
			u.Team = out.Integrations[i].Team
			out.Integrations[i] = u
		} else {
			out.Integrations = append(out.Integrations, u)
		}
	}
	return out, nil
}
