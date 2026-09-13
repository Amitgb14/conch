package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

const (
	// projectTick is how often projects are checked for git changes. A check
	// is only a few stat calls unless something changed.
	projectTick = time.Second
	// Uncommitted edits don't touch git metadata, so worktree status is also
	// re-read periodically: often while panes work in the project.
	statusIntervalActive = 5 * time.Second
	statusIntervalIdle   = 30 * time.Second
	gitTimeout           = 30 * time.Second
	// Pull requests come from the network, so they refresh less often.
	prIntervalActive = time.Minute
	prIntervalIdle   = 5 * time.Minute
	prRetryError     = 10 * time.Minute
	prRetryNoGitHub  = time.Hour
	maxGitRefreshes  = 2 // concurrent project refreshes
)

// projectManager tracks projects and keeps their git state fresh.
type projectManager struct {
	s    *Server
	file string // catalog of added projects

	mu       sync.Mutex
	projects map[string]*project
	order    []string

	gitSem chan struct{}
}

type project struct {
	id     string
	root   string
	common string // git common dir; "" for plain folders

	mu         sync.Mutex
	info       proto.ProjectInfo
	stamp      string
	lastStatus time.Time
	refreshing bool
	pending    bool // refresh requested (e.g. an agent just used a tool)

	prs        map[string]proto.PRInfo // by head branch
	prStatus   string
	prAt       time.Time // last fetch
	prNext     time.Time // no fetch before this (after errors)
	prFetching bool
	prPending  bool
}

type catalog struct {
	Projects []catalogEntry `json:"projects"`
}

type catalogEntry struct {
	Path  string    `json:"path"`
	Added time.Time `json:"added"`
}

func newProjectManager(s *Server, configDir string) *projectManager {
	return &projectManager{
		s:        s,
		file:     filepath.Join(configDir, "projects.json"),
		projects: map[string]*project{},
		gitSem:   make(chan struct{}, maxGitRefreshes),
	}
}

func projectID(root string) string {
	sum := sha1.Sum([]byte(root))
	return "r" + hex.EncodeToString(sum[:])[:7]
}

// load restores the catalog. Projects whose folder is gone are dropped.
func (pm *projectManager) load() {
	b, err := os.ReadFile(pm.file)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("projects: %v", err)
		}
		return
	}
	var cat catalog
	if err := json.Unmarshal(b, &cat); err != nil {
		log.Printf("projects: %s: %v", pm.file, err)
		return
	}
	for _, e := range cat.Projects {
		if _, err := pm.add(e.Path, false); err != nil {
			log.Printf("projects: dropping %s: %v", e.Path, err)
		}
	}
}

func (pm *projectManager) save() {
	pm.mu.Lock()
	cat := catalog{Projects: []catalogEntry{}}
	for _, id := range pm.order {
		p := pm.projects[id]
		cat.Projects = append(cat.Projects, catalogEntry{Path: p.root, Added: time.Now()})
	}
	pm.mu.Unlock()
	b, _ := json.MarshalIndent(cat, "", "  ")
	tmp := pm.file + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		log.Printf("projects: save: %v", err)
		return
	}
	if err := os.Rename(tmp, pm.file); err != nil {
		log.Printf("projects: save: %v", err)
	}
}

// add registers the project containing path: its git repository (a linked
// worktree resolves to its main repository), or the folder itself.
func (pm *projectManager) add(path string, persist bool) (*project, error) {
	if home, err := os.UserHomeDir(); err == nil && (path == "~" || strings.HasPrefix(path, "~/")) {
		path = filepath.Join(home, path[1:]) // clients may not know this machine's home
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if abs, err = filepath.EvalSymlinks(abs); err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	root, common := abs, ""
	repo, err := gitx.Discover(ctx, abs)
	switch {
	case err == nil:
		root, common = repo.Root, repo.CommonDir
	case !errors.Is(err, gitx.ErrNotRepo):
		return nil, err
	}

	id := projectID(root)
	pm.mu.Lock()
	if p, ok := pm.projects[id]; ok {
		pm.mu.Unlock()
		return p, nil
	}
	p := &project{id: id, root: root, common: common}
	p.info = proto.ProjectInfo{ID: id, Name: filepath.Base(root), Path: root, Git: common != ""}
	pm.projects[id] = p
	pm.order = append(pm.order, id)
	pm.mu.Unlock()

	if persist {
		pm.save()
	}
	log.Printf("project %s added: %s", id, root)
	pm.s.broadcast(proto.EventProjectUpdated, p.snapshot())
	pm.request(p)
	return p, nil
}

// ensure adds the git repository containing dir, if any, and returns its
// project. Plain folders are only projects when added explicitly.
func (pm *projectManager) ensure(dir string) *project {
	if p := pm.containing(dir); p != nil {
		return p
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	if _, err := gitx.Discover(ctx, dir); err != nil {
		return nil
	}
	p, err := pm.add(dir, true)
	if err != nil {
		log.Printf("projects: %v", err)
		return nil
	}
	return p
}

// containing returns the project whose root or a worktree contains dir.
func (pm *projectManager) containing(dir string) *project {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	var best *project
	bestLen := -1
	for _, id := range pm.order {
		p := pm.projects[id]
		for _, root := range p.roots() {
			if within(dir, root) && len(root) > bestLen {
				best, bestLen = p, len(root)
			}
		}
	}
	return best
}

func (p *project) roots() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	roots := []string{p.root}
	for _, wt := range p.info.Worktrees {
		roots = append(roots, wt.Path)
	}
	return roots
}

func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

func (pm *projectManager) remove(id string) *proto.Error {
	pm.mu.Lock()
	if _, ok := pm.projects[id]; !ok {
		pm.mu.Unlock()
		return proto.Errorf(proto.ErrNotFound, "no project %q", id)
	}
	delete(pm.projects, id)
	for i, oid := range pm.order {
		if oid == id {
			pm.order = append(pm.order[:i], pm.order[i+1:]...)
			break
		}
	}
	pm.mu.Unlock()
	pm.save()
	pm.s.broadcast(proto.EventProjectRemoved, proto.ProjectRef{ID: id})
	return nil
}

func (pm *projectManager) get(id string) (*project, *proto.Error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	p, ok := pm.projects[id]
	if !ok {
		return nil, proto.Errorf(proto.ErrNotFound, "no project %q", id)
	}
	return p, nil
}

func (pm *projectManager) list() []proto.ProjectInfo {
	pm.mu.Lock()
	ps := make([]*project, 0, len(pm.order))
	for _, id := range pm.order {
		ps = append(ps, pm.projects[id])
	}
	pm.mu.Unlock()
	out := make([]proto.ProjectInfo, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.snapshot())
	}
	return out
}

func (p *project) snapshot() proto.ProjectInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info
}

// worktreeFor returns the worktree containing dir.
func (p *project) worktreeFor(dir string) (proto.WorktreeInfo, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var best proto.WorktreeInfo
	found := false
	for _, wt := range p.info.Worktrees {
		if within(dir, wt.Path) && len(wt.Path) > len(best.Path) {
			best, found = wt, true
		}
	}
	return best, found
}

// branchWorktree returns where branch is checked out, if anywhere.
func (p *project) branchWorktree(branch string) (proto.WorktreeInfo, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, wt := range p.info.Worktrees {
		if wt.Branch == branch {
			return wt, true
		}
	}
	return proto.WorktreeInfo{}, false
}

// request asks for a refresh on the next tick.
func (pm *projectManager) request(p *project) {
	p.mu.Lock()
	p.pending = true
	p.mu.Unlock()
}

// run refreshes projects until the server stops.
func (pm *projectManager) run() {
	t := time.NewTicker(projectTick)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			pm.mu.Lock()
			ps := make([]*project, 0, len(pm.order))
			for _, id := range pm.order {
				ps = append(ps, pm.projects[id])
			}
			pm.mu.Unlock()
			for _, p := range ps {
				pm.maybeRefresh(p)
			}
		case <-pm.s.quit:
			return
		}
	}
}

func (pm *projectManager) maybeRefresh(p *project) {
	if p.common == "" {
		return
	}
	pm.maybeFetchPRs(p)
	stamp := gitx.MetaStamp(p.common)
	interval := statusIntervalIdle
	if pm.s.projectHasPanes(p.id) {
		interval = statusIntervalActive
	}
	p.mu.Lock()
	due := p.pending || stamp != p.stamp || time.Since(p.lastStatus) > interval
	if !due || p.refreshing {
		p.mu.Unlock()
		return
	}
	p.refreshing, p.pending = true, false
	p.mu.Unlock()
	go pm.refresh(p, stamp)
}

func (pm *projectManager) refresh(p *project, stamp string) {
	pm.gitSem <- struct{}{}
	info := p.load()
	<-pm.gitSem

	p.mu.Lock()
	p.applyPRs(&info)
	old := p.info
	old.Refreshed, info.Refreshed = time.Time{}, time.Now()
	changed := !reflect.DeepEqual(old, withoutTime(info))
	p.info = info
	p.stamp = stamp
	p.lastStatus = time.Now()
	p.refreshing = false
	p.mu.Unlock()

	if changed && pm.s.projectRegistered(p) {
		pm.s.broadcast(proto.EventProjectUpdated, info)
	}
}

// requestPRs asks for pull requests to be fetched on the next tick.
func (pm *projectManager) requestPRs(p *project) {
	p.mu.Lock()
	p.prPending = true
	p.prNext = time.Time{}
	p.mu.Unlock()
}

func (pm *projectManager) maybeFetchPRs(p *project) {
	interval := prIntervalIdle
	if pm.s.projectHasPanes(p.id) {
		interval = prIntervalActive
	}
	now := time.Now()
	p.mu.Lock()
	due := p.prPending || p.prAt.IsZero() || now.Sub(p.prAt) > interval
	if !due || p.prFetching || now.Before(p.prNext) {
		p.mu.Unlock()
		return
	}
	p.prFetching, p.prPending = true, false
	p.mu.Unlock()
	go pm.fetchPRs(p)
}

func (pm *projectManager) fetchPRs(p *project) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	list, err := ghx.List(ctx, p.root)

	p.mu.Lock()
	prevStatus := p.prStatus
	p.prFetching, p.prAt = false, time.Now()
	switch {
	case errors.Is(err, ghx.ErrNoGH):
		p.prStatus, p.prNext = "install gh (GitHub CLI) to see pull requests", time.Now().Add(prRetryNoGitHub)
	case err != nil && (strings.Contains(err.Error(), "known GitHub host") || strings.Contains(err.Error(), "no git remotes")):
		p.prStatus, p.prNext = "no GitHub remote", time.Now().Add(prRetryNoGitHub)
	case err != nil:
		p.prStatus, p.prNext = err.Error(), time.Now().Add(prRetryError)
	default:
		p.prStatus = ""
		p.prs = map[string]proto.PRInfo{}
		for branch, pr := range ghx.ByBranch(list) {
			p.prs[branch] = proto.PRInfo{
				Number: pr.Number, Title: pr.Title, State: pr.State, Draft: pr.Draft, URL: pr.URL,
				Review: pr.Review, Checks: pr.Checks, Passed: pr.Passed, Total: pr.Total,
			}
		}
	}
	if err != nil {
		p.prs = nil
	}
	info := p.info
	info.Branches = append([]proto.BranchInfo(nil), info.Branches...)
	p.applyPRs(&info)
	changed := !reflect.DeepEqual(withoutTime(info), withoutTime(p.info))
	p.info = info
	statusChanged := p.prStatus != prevStatus
	p.mu.Unlock()

	if statusChanged && err != nil {
		log.Printf("project %s: pull requests unavailable: %v", p.id, err)
	}
	if changed && pm.s.projectRegistered(p) {
		pm.s.broadcast(proto.EventProjectUpdated, info)
	}
}

// applyPRs attaches fetched pull requests to branches. The caller holds p.mu.
func (p *project) applyPRs(info *proto.ProjectInfo) {
	info.PRStatus = p.prStatus
	for i := range info.Branches {
		if pr, ok := p.prs[info.Branches[i].Name]; ok {
			pr := pr
			info.Branches[i].PR = &pr
		} else {
			info.Branches[i].PR = nil
		}
	}
}

func withoutTime(info proto.ProjectInfo) proto.ProjectInfo {
	info.Refreshed = time.Time{}
	return info
}

// load reads the project's git state.
func (p *project) load() proto.ProjectInfo {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	info := proto.ProjectInfo{ID: p.id, Name: filepath.Base(p.root), Path: p.root, Git: true}
	info.Base = gitx.DefaultBase(ctx, p.root)

	wts, err := gitx.Worktrees(ctx, p.root)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	checkedOut := map[string]string{}
	for _, wt := range wts {
		wi := proto.WorktreeInfo{Path: wt.Path, Branch: wt.Branch, Head: wt.Head, Detached: wt.Detached, Main: wt.Main}
		if st, err := gitx.WorktreeStatus(ctx, wt.Path); err == nil {
			wi.Status = &proto.GitStatus{
				Staged: st.Staged, Unstaged: st.Unstaged, Untracked: st.Untracked, Conflicts: st.Conflicts,
				Files: st.Files, Added: st.Added, Deleted: st.Deleted,
			}
		}
		info.Worktrees = append(info.Worktrees, wi)
		if wt.Branch != "" {
			checkedOut[wt.Branch] = wt.Path
		}
	}

	branches, err := gitx.Branches(ctx, p.root, info.Base)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	for _, b := range branches {
		info.Branches = append(info.Branches, proto.BranchInfo{
			Name: b.Name, Upstream: b.Upstream, Gone: b.Gone, Ahead: b.Ahead, Behind: b.Behind,
			BaseAhead: b.BaseAhead, BaseBehind: b.BaseBehind, Committed: b.Committed, Subject: b.Subject,
			Worktree: checkedOut[b.Name],
		})
	}
	return info
}

// changes lists the work on a branch: uncommitted changes when it is
// checked out, else the files it changed since the base.
func (pm *projectManager) changes(cp proto.ChangesParams) (proto.Changes, *proto.Error) {
	p, perr := pm.get(cp.ProjectID)
	if perr != nil {
		return proto.Changes{}, perr
	}
	base := p.snapshot().Base
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	out := proto.Changes{ProjectID: p.id, Branch: cp.Branch, Base: base}
	var ch gitx.Changes
	var err error
	if wt, ok := p.branchWorktree(cp.Branch); ok {
		out.Worktree = wt.Path
		ch, err = gitx.WorktreeChanges(ctx, wt.Path, base)
	} else {
		ch, err = gitx.BranchChanges(ctx, p.root, cp.Branch, base)
	}
	if err != nil {
		return out, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	out.Files = make([]proto.FileChange, 0, len(ch.Files))
	for _, f := range ch.Files {
		out.Files = append(out.Files, proto.FileChange{
			Path: f.Path, OrigPath: f.OrigPath, Code: f.Code, Staged: f.Staged, Unstaged: f.Unstaged,
			Added: f.Added, Deleted: f.Deleted, Binary: f.Binary,
		})
	}
	out.Commits = make([]proto.CommitInfo, 0, len(ch.Commits))
	for _, c := range ch.Commits {
		out.Commits = append(out.Commits, proto.CommitInfo{Hash: c.Hash, Subject: c.Subject, Author: c.Author, Time: c.Time})
	}
	return out, nil
}

func (pm *projectManager) diff(dp proto.DiffParams) (proto.DiffResult, *proto.Error) {
	p, perr := pm.get(dp.ProjectID)
	if perr != nil {
		return proto.DiffResult{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	var d string
	var err error
	if wt, ok := p.branchWorktree(dp.Branch); ok {
		d, err = gitx.Diff(ctx, wt.Path, dp.File, "")
	} else {
		d, err = gitx.Diff(ctx, p.root, dp.File, p.snapshot().Base+"..."+dp.Branch)
	}
	if err != nil {
		return proto.DiffResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	return proto.DiffResult{Diff: d}, nil
}

// addWorktree checks branch out into <repo>.worktrees/<branch>, creating
// the branch from base if needed. A taken directory gets a numeric suffix.
func (pm *projectManager) addWorktree(p *project, branch, base string) (string, *proto.Error) {
	if !p.snapshot().Git {
		return "", proto.Errorf(proto.ErrBadRequest, "%s is not a git repository", p.root)
	}
	if wt, ok := p.branchWorktree(branch); ok {
		return "", proto.Errorf(proto.ErrBadRequest, "branch %s is already checked out at %s", branch, wt.Path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	if err := gitx.ValidBranchName(ctx, p.root, branch); err != nil {
		return "", proto.Errorf(proto.ErrBadRequest, "invalid branch name %q", branch)
	}
	if base == "" {
		base = p.snapshot().Base
	}
	path := gitx.WorktreeDir(p.root, branch)
	for i := 2; ; i++ {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			break
		}
		path = fmt.Sprintf("%s-%d", gitx.WorktreeDir(p.root, branch), i)
	}
	if err := gitx.AddWorktree(ctx, p.root, path, branch, base); err != nil {
		return "", proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	log.Printf("project %s: worktree %s for %s", p.id, path, branch)
	p.mu.Lock()
	p.info.Worktrees = append(p.info.Worktrees, proto.WorktreeInfo{Path: path, Branch: branch})
	p.mu.Unlock()
	pm.request(p)
	return path, nil
}

func (pm *projectManager) removeWorktree(wp proto.WorktreeRemoveParams) *proto.Error {
	p, perr := pm.get(wp.ProjectID)
	if perr != nil {
		return perr
	}
	var target *proto.WorktreeInfo
	for _, wt := range p.snapshot().Worktrees {
		if wt.Path == wp.Path {
			target = &wt
			break
		}
	}
	switch {
	case target == nil:
		return proto.Errorf(proto.ErrNotFound, "no worktree %s in %s", wp.Path, p.root)
	case target.Main:
		return proto.Errorf(proto.ErrBadRequest, "refusing to remove the main worktree")
	case pm.s.panesIn(wp.Path):
		return proto.Errorf(proto.ErrBadRequest, "panes are still running in %s; close them first", wp.Path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	if err := gitx.RemoveWorktree(ctx, p.root, wp.Path); err != nil {
		return proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	log.Printf("project %s: removed worktree %s", p.id, wp.Path)
	pm.request(p)
	return nil
}
