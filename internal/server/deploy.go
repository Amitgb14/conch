package server

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
)

// A branch's deploy previews (Vercel, Netlify): the deploys a deploy
// integration has of it, what a failed build printed — sent to the
// branch's agent as a failed CI run is — building one again, and
// releasing one to production, which only the person may ask for (scope.go
// refuses it from every agent, and the autopilot never calls it).

// previewsFor is how long a branch's previews are kept: they are asked for
// at every redraw of a branch, and each ask is two calls to the service.
const previewsFor = 15 * time.Second

// deploySource is one deploy integration, ready to ask about a branch.
type deploySource struct {
	f      *integration.File
	conn   *integration.Conn
	target integration.Target
	// head is the pull request's head, which a failed build sent to the
	// agent must have built.
	head string
	// why says what is wrong when conn is nil.
	why string
}

func (d deploySource) label() string { return d.f.DisplayName() }

// deployFor is the deploy integrations to ask about branch: those the
// checkout or its remote shows it uses, and those a check of its pull
// request came from — in name order, each connected with its key or
// saying why not. With a pull request only the deploys at its head are
// asked for (the files' sha={head}); without one, the branch's history.
func (pm *projectManager) deployFor(ctx context.Context, p *project, branch string, pr proto.PRInfo) []deploySource {
	rem, _ := integration.ParseRemote(gitx.RemoteURL(ctx, p.root))
	files := integration.Cached()
	var bases map[string]string
	var chosen []string
	if cfg, err := config.Load(); err == nil {
		bases = cfg.Integrations.Base
		chosen = cfg.Integrations.Chosen(p.snapshot().Name, p.root)
	}
	use := map[string]*integration.File{}
	for _, f := range files {
		// Its files or remote show it, or the person said so: a service
		// that links the repository from its own side leaves no file.
		if f.Kind == integration.KindDeploy && (f.Uses(p.root, rem) || slices.Contains(chosen, f.Name)) {
			use[f.Name] = f
		}
	}
	for _, c := range pr.Runs {
		src, _ := integration.CheckSource(c.Name, c.URL, files)
		for _, f := range files {
			if src != "" && f.Name == src && f.Kind == integration.KindDeploy {
				use[f.Name] = f
			}
		}
	}
	names := make([]string, 0, len(use))
	for n := range use {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []deploySource
	for _, n := range names {
		f := use[n]
		d := deploySource{f: f, target: integration.Target{Remote: rem, Branch: branch, Head: pr.Head}, head: pr.Head}
		switch {
		case rem.Host == "":
			d.why = "the project has no remote to name it by"
		default:
			d.conn, d.why = pm.s.connectFile(f, bases)
		}
		out = append(out, d)
	}
	return out
}

// deploySourceNamed is the one of the branch's deploy integrations named
// source, or a not-found that lists them.
func (pm *projectManager) deploySourceNamed(ctx context.Context, p *project, branch, source string, pr proto.PRInfo) (deploySource, *proto.Error) {
	all := pm.deployFor(ctx, p, branch, pr)
	var names []string
	for _, d := range all {
		if d.f.Name == source {
			if d.conn == nil {
				return d, proto.Errorf(proto.ErrBadRequest, "%s: %s", d.label(), d.why)
			}
			return d, nil
		}
		names = append(names, d.f.Name)
	}
	if len(names) == 0 {
		return deploySource{}, proto.Errorf(proto.ErrNotFound, "no deploy integration is used by this project")
	}
	return deploySource{}, proto.Errorf(proto.ErrNotFound, "%q is not a deploy integration this project uses (it uses %s)", source, strings.Join(names, ", "))
}

// branchOf is the project, the branch's pull request and the checks
// every deploy method starts with.
func (pm *projectManager) branchOf(projectID, branch string) (*project, proto.PRInfo, *proto.Error) {
	p, perr := pm.gitProject(projectID)
	if perr != nil {
		return nil, proto.PRInfo{}, perr
	}
	if branch == "" {
		return nil, proto.PRInfo{}, proto.Errorf(proto.ErrBadRequest, "no branch given")
	}
	p.mu.Lock()
	pr := p.prs[branch]
	p.mu.Unlock()
	return p, pr, nil
}

// previewCache keeps each branch's answer for previewsFor.
type previewCache struct {
	mu   sync.Mutex
	kept map[string]previewsKept
}

type previewsKept struct {
	at  time.Time
	out proto.BranchPreviews
}

func (c *previewCache) get(key string) (proto.BranchPreviews, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k, ok := c.kept[key]
	if !ok || time.Since(k.at) > previewsFor {
		return proto.BranchPreviews{}, false
	}
	return k.out, true
}

func (c *previewCache) put(key string, out proto.BranchPreviews) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kept == nil {
		c.kept = map[string]previewsKept{}
	}
	c.kept[key] = previewsKept{at: time.Now(), out: out}
}

// forgetBranch drops a branch's answers (keys starting with prefix),
// after something was asked of one of its deploys: the next look should
// see the new build.
func (c *previewCache) forgetBranch(prefix string) {
	c.mu.Lock()
	for k := range c.kept {
		if strings.HasPrefix(k, prefix) {
			delete(c.kept, k)
		}
	}
	c.mu.Unlock()
}

// branchPreviews answers branch.previews.
func (pm *projectManager) branchPreviews(lp proto.BranchCILogParams) (proto.BranchPreviews, *proto.Error) {
	p, pr, perr := pm.branchOf(lp.ProjectID, lp.Branch)
	if perr != nil {
		return proto.BranchPreviews{}, perr
	}
	key := p.id + "|" + lp.Branch + "|" + pr.Head
	if cfg, err := config.Load(); err == nil {
		// Turning a project's switch is seen at once, not after the moment.
		key += "|" + strings.Join(cfg.Integrations.Chosen(p.snapshot().Name, p.root), ",")
	}
	if out, ok := pm.previews.get(key); ok {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out := proto.BranchPreviews{Integrations: []proto.DeploySource{}}
	var shas []string
	for _, d := range pm.deployFor(ctx, p, lp.Branch, pr) {
		src := proto.DeploySource{Source: d.f.Name, Service: d.label(), Why: d.why, Head: d.head,
			BuildLog: integration.HasBuildLog(d.f), Redeploy: d.f.Redeploy.Has(), Promote: d.f.Promote.Has()}
		if src.Promote {
			r := integration.ReleaseOf(d.f)
			src.Release = &proto.DeployRelease{Verb: r.Verb, What: r.What, Note: r.Note, Rebuilds: r.Rebuilds}
		}
		if d.conn != nil {
			deps, err := d.conn.Previews(ctx, d.target)
			switch {
			case errors.Is(err, integration.ErrUnlinked):
				src.Missing = integration.Unlinked(d.f, d.target.Remote)
			case err != nil:
				src.Why = err.Error()
			}
			for _, dep := range deps {
				info := deployInfo(d.f, dep)
				info.AtHead = d.head != "" && sameCommit(d.head, dep.Commit)
				src.Deploys = append(src.Deploys, info)
				shas = append(shas, dep.Commit)
			}
		}
		out.Integrations = append(out.Integrations, src)
	}
	if subjects := commitSubjects(ctx, p.root, lp.Branch, shas); len(subjects) > 0 {
		for i := range out.Integrations {
			for j := range out.Integrations[i].Deploys {
				d := &out.Integrations[i].Deploys[j]
				d.Subject = subjects[d.Commit]
			}
		}
	}
	// An answer that could not ask is not kept: a key given a moment
	// later should be used at once.
	keep := true
	for _, src := range out.Integrations {
		keep = keep && src.Why == ""
	}
	if keep {
		pm.previews.put(key, out)
	}
	return out, nil
}

// sameCommit: one sha is the other, or its start (a service may give a
// short one).
func sameCommit(a, b string) bool {
	return a != "" && b != "" && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a))
}

func deployInfo(f *integration.File, d integration.Deploy) proto.DeployInfo {
	info := proto.DeployInfo{ID: d.ID, Name: d.Name, State: d.State, URL: d.URL, Environment: d.Environment,
		Commit: d.Commit, Error: d.Error, Inspect: d.Inspect}
	if !d.Started.IsZero() {
		t := d.Started
		info.Started = &t
	}
	if !d.Finished.IsZero() {
		t := d.Finished
		info.Finished = &t
	}
	info.Promotable, info.NotPromotable = integration.CanRelease(f, d)
	return info
}

// commitSubjects is each commit's subject that the checkout has, by the
// sha the service gave: one git log of the branch, not one call each.
func commitSubjects(ctx context.Context, root, branch string, shas []string) map[string]string {
	want := false
	for _, s := range shas {
		want = want || s != ""
	}
	if !want {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "log", "-n", "300", "--format=%H %s", branch, "--")
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		sha, subject, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		for _, s := range shas {
			if len(s) >= 7 && strings.HasPrefix(sha, s) {
				out[s] = subject
			}
		}
	}
	return out
}

// branchDeployLog answers branch.deploy_log: a deploy's build log as a
// failed CI run's is said, so the prompt to the agent is the same one.
// With no deploy, the newest failed build at the pull request's head.
func (pm *projectManager) branchDeployLog(dp proto.BranchDeployParams) (proto.BranchCILog, *proto.Error) {
	p, pr, perr := pm.branchOf(dp.ProjectID, dp.Branch)
	if perr != nil {
		return proto.BranchCILog{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if dp.Source != "" {
		d, perr := pm.deploySourceNamed(ctx, p, dp.Branch, dp.Source, pr)
		if perr != nil {
			return proto.BranchCILog{}, perr
		}
		lg, note := d.failedLog(ctx, dp.Deploy)
		if note != "" {
			return proto.BranchCILog{}, proto.Errorf(proto.ErrNotFound, "%s", note)
		}
		return lg, nil
	}
	if dp.Deploy != "" {
		return proto.BranchCILog{}, proto.Errorf(proto.ErrBadRequest, "a deploy is named with its integration (source)")
	}
	lg, notes := pm.deployFailure(ctx, p, dp.Branch, pr)
	if notes == nil {
		return lg, nil
	}
	if len(notes) == 0 {
		return proto.BranchCILog{}, proto.Errorf(proto.ErrNotFound, "no deploy integration is used by this project")
	}
	return proto.BranchCILog{}, proto.Errorf(proto.ErrNotFound, "%s", strings.Join(notes, "; "))
}

// deployFailure is the first failed build at the head among the branch's
// deploy integrations; nil notes when one was read, else what each said.
func (pm *projectManager) deployFailure(ctx context.Context, p *project, branch string, pr proto.PRInfo) (proto.BranchCILog, []string) {
	notes := []string{}
	for _, d := range pm.deployFor(ctx, p, branch, pr) {
		lg, note := d.failedLog(ctx, "")
		if note == "" {
			return lg, nil
		}
		notes = append(notes, note)
	}
	return proto.BranchCILog{}, notes
}

// failedDeploy is the deploy named, or the newest failed one at the head.
func (d deploySource) failedDeploy(ctx context.Context, id string) (integration.Deploy, string) {
	if d.conn == nil {
		return integration.Deploy{}, d.label() + ": " + d.why
	}
	deps, err := d.conn.Previews(ctx, d.target)
	if errors.Is(err, integration.ErrUnlinked) {
		return integration.Deploy{}, d.label() + ": " + integration.Unlinked(d.f, d.target.Remote)
	}
	if err != nil {
		return integration.Deploy{}, d.label() + ": " + err.Error()
	}
	for _, dep := range deps {
		if id != "" {
			if dep.ID == id {
				return dep, ""
			}
			continue
		}
		if dep.State == "failed" && (d.head == "" || dep.Commit == "" || sameCommit(d.head, dep.Commit)) {
			return dep, ""
		}
	}
	if id != "" {
		return integration.Deploy{}, fmt.Sprintf("%s: no deploy %s on %s", d.label(), id, d.target.Branch)
	}
	return integration.Deploy{}, fmt.Sprintf("%s: no failed deploy on %s", d.label(), d.target.Branch)
}

// failedLog is a deploy's build log, said as a failed run's: Workflow is
// the deploy's name (else its id), URL the service's page for it. A build
// that left no log (Netlify gives an error message only, and sometimes
// none) is sent its error instead; with neither there is nothing to send.
func (d deploySource) failedLog(ctx context.Context, id string) (proto.BranchCILog, string) {
	dep, note := d.failedDeploy(ctx, id)
	if note != "" {
		return proto.BranchCILog{}, note
	}
	lg := proto.BranchCILog{Workflow: firstNonEmpty(dep.Name, dep.ID), URL: firstNonEmpty(dep.Inspect, dep.URL),
		Source: d.f.Name, Service: d.label(), Job: "deploy " + dep.ID}
	if !d.f.FailedLog.Has() {
		if dep.Error == "" {
			return proto.BranchCILog{}, d.label() + ": its integration cannot read a build's log"
		}
		lg.Log = dep.Error + "\n"
		return lg, ""
	}
	text, err := d.conn.FailedLog(ctx, d.target, dep.ID)
	switch {
	case errors.Is(err, integration.ErrNothing) && dep.Error != "":
		lg.Log = dep.Error + "\n"
	case errors.Is(err, integration.ErrNothing):
		return proto.BranchCILog{}, fmt.Sprintf("%s: deploy %s left no log and no error", d.label(), dep.ID)
	case err != nil:
		return proto.BranchCILog{}, d.label() + ": " + err.Error()
	default:
		lg.Log = tail(text.Text, maxCILog)
	}
	return lg, ""
}

// branchRedeploy builds a deploy again.
func (pm *projectManager) branchRedeploy(dp proto.BranchDeployParams) (proto.BranchDeployDone, *proto.Error) {
	return pm.deployAct(dp, "redeploy")
}

// branchPromote releases a ready deploy to production. scope.go keeps it
// from every agent; the TUI and the app ask the person first.
func (pm *projectManager) branchPromote(dp proto.BranchDeployParams) (proto.BranchDeployDone, *proto.Error) {
	return pm.deployAct(dp, "promote")
}

func (pm *projectManager) deployAct(dp proto.BranchDeployParams, what string) (proto.BranchDeployDone, *proto.Error) {
	p, pr, perr := pm.branchOf(dp.ProjectID, dp.Branch)
	if perr != nil {
		return proto.BranchDeployDone{}, perr
	}
	if dp.Source == "" || strings.TrimSpace(dp.Deploy) == "" {
		return proto.BranchDeployDone{}, proto.Errorf(proto.ErrBadRequest, "a deploy and its integration (source) must be named")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d, perr := pm.deploySourceNamed(ctx, p, dp.Branch, dp.Source, pr)
	if perr != nil {
		return proto.BranchDeployDone{}, perr
	}
	done := proto.BranchDeployDone{Source: d.f.Name, Service: d.label(), Deploy: dp.Deploy}
	var err error
	switch what {
	case "redeploy":
		if !d.f.Redeploy.Has() {
			return done, proto.Errorf(proto.ErrBadRequest, "%s: its integration cannot redeploy", d.label())
		}
		err = d.conn.Redeploy(ctx, d.target, dp.Deploy)
	case "promote":
		// What is released is checked against the service's own rule
		// first: Netlify will not publish a deploy preview, and nothing
		// but a ready build goes to production.
		dep, note := d.failedDeploy(ctx, dp.Deploy)
		if note != "" {
			return done, proto.Errorf(proto.ErrNotFound, "%s", note)
		}
		if ok, why := integration.CanRelease(d.f, dep); !ok {
			if why == "" {
				why = fmt.Sprintf("deploy %s is %s, not ready", dep.ID, firstNonEmpty(dep.State, "of no state"))
				if !d.f.Promote.Has() {
					why = "its integration cannot release to production"
				}
			}
			return done, proto.Errorf(proto.ErrBadRequest, "%s: %s", d.label(), why)
		}
		err = d.conn.Promote(ctx, d.target, dp.Deploy)
	}
	pm.previews.forgetBranch(p.id + "|" + dp.Branch + "|")
	if err != nil {
		return done, proto.Errorf(proto.ErrBadRequest, "%s: %v", d.label(), err)
	}
	return done, nil
}
