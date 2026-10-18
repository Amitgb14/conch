package server

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// A pull request that conflicts with its base stops the loop: it cannot
// merge, and CI passing says nothing about it. With [verify] auto_resolve
// the server first rebases the branch onto the base as the remote has it,
// which is all most of them need — the base moved, the branch did not
// touch the same lines. When the rebase stops on a conflict it is undone,
// and the branch's agent is asked to do it, told which files and what the
// base did to them, to keep both sides.
//
// Either way the rewritten branch has to replace the one that was pushed,
// which is the one step here that could lose work, so it is checked
// first (shipRewrite): the base must be all there, nothing may be deleted
// that the branch did not delete before, the project's check must have
// passed for the commit, and the push only goes through while the remote
// still has what was there when the rebase began — anybody else's push
// meanwhile is kept, and the rewrite refused.

// conflictUnwatched marks a pull request that already conflicted when the
// server first saw it: known, and left to the person, as a red one is.
const conflictUnwatched = "-"

// resolveTimeout bounds one attempt: a fetch and a rebase.
const resolveTimeout = 2 * time.Minute

// rewrite is a branch rebased onto Base that must replace what was pushed
// (Lease, "" when it never was), and the files it had deleted before.
type rewrite struct {
	Base    string   `json:"base"`
	Lease   string   `json:"lease,omitempty"`
	Deleted []string `json:"deleted,omitempty"`
}

// autopilotResolves looks at every conflicting pull request the server
// is minding.
func (s *Server) autopilotResolves() {
	a := s.autopilot
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.saved.Policy.AutoResolve <= 0 {
		a.mu.Unlock()
		return
	}
	keys := make([]string, 0, len(a.saved.Conflicted))
	for key, at := range a.saved.Conflicted {
		if at != conflictUnwatched && !a.resolving[key] {
			keys = append(keys, key)
		}
	}
	a.mu.Unlock()
	slices.Sort(keys)
	for _, key := range keys {
		s.autopilotResolve(key)
	}
}

// autopilotResolve brings one conflicting branch up to date with its
// base, once for each commit it is at: after the agent's resolution the
// pull request still says it conflicts until GitHub looks again, and that
// is no reason to do it twice.
func (s *Server) autopilotResolve(key string) {
	a := s.autopilot
	projectID, branch, _ := strings.Cut(key, "\x00")
	p, perr := s.projects.get(projectID)
	if perr != nil {
		return
	}
	wt, ok := p.branchWorktree(branch)
	if !ok || wt.Path == "" {
		return // not checked out here: nowhere to rebase it
	}
	if agent := s.branchAgent(projectID, branch); agent != nil &&
		(agent.Agent.State == proto.AgentWorking || agent.Agent.State == proto.AgentBlocked) {
		return // it may be resolving it now; look again when it is free
	}
	head := branchHead(p, branch)
	a.mu.Lock()
	if a.resolving[key] || a.saved.Conflicted[key] == head || head == "" {
		a.mu.Unlock()
		return
	}
	a.resolving[key] = true
	a.saved.Conflicted[key] = head
	a.saveLocked()
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.resolving, key)
		a.mu.Unlock()
	}()
	note := func(text string, isErr bool) {
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Text: text, Error: isErr})
	}

	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()
	base := gitx.FreshBase(ctx, wt.Path, branch, p.snapshot().Base)
	if base == "" {
		note(branch+" conflicts with its base, which could not be fetched", true)
		return
	}
	if gitx.InProgress(ctx, wt.Path) != "" {
		note(branch+" conflicts with "+base+", and a rebase or merge is already under way there; finish it and it goes out", false)
		return
	}
	lease := gitx.PushedHead(ctx, wt.Path, branch)
	before, err := gitx.Deleted(ctx, wt.Path, base)
	if err != nil {
		note(fmt.Sprintf("%s conflicts with %s, and what it changed could not be read: %v", branch, base, err), true)
		return
	}
	rw := rewrite{Base: base, Lease: lease, Deleted: before}
	moved, err := gitx.RebaseOnto(ctx, wt.Path, branch, base)
	var conflict *gitx.OntoConflictError
	switch {
	case err == nil && !moved:
		return // on top of it already: the push that says so is shipping's
	case err == nil:
		a.mu.Lock()
		a.saved.Rewrites[key] = rw
		a.saveLocked()
		a.mu.Unlock()
		s.projects.request(p)
		note(fmt.Sprintf("rebased %s onto %s, which it conflicted with; checking it before it goes out", branch, base), false)
		s.autopilotRewritten(projectID, branch)
	case errors.As(err, &conflict):
		s.askToResolve(p, branch, rw, conflict.Files)
	default:
		note(fmt.Sprintf("%s conflicts with %s, and rebasing it failed: %v", branch, base, err), true)
	}
}

// autopilotRewritten is a branch just rebased: it is checked, where the
// project has a check, and shipped — which replaces the pushed branch.
func (s *Server) autopilotRewritten(projectID, branch string) {
	if cmd := s.autopilot.policy().Checks[projectID]; cmd != "" {
		go s.autopilotStartCheck(projectID, branch, cmd)
		return
	}
	go s.autopilotShip(projectID, branch)
}

// askToResolve hands a conflict to the branch's agent, within
// auto_resolve's allowance.
func (s *Server) askToResolve(p *project, branch string, rw rewrite, files []string) {
	a := s.autopilot
	key := branchKey(p.id, branch)
	note := func(text string, isErr bool) {
		s.autopilotNote(proto.AutopilotNote{ProjectID: p.id, Branch: branch, Text: text, Error: isErr})
	}
	agent := s.freeAgent(p.id, branch)
	if agent == nil {
		note(fmt.Sprintf("%s conflicts with %s in %s, and there is no agent free to resolve it", branch, rw.Base, strings.Join(files, ", ")), true)
		return
	}
	a.mu.Lock()
	limit := a.saved.Policy.AutoResolve
	tries := a.saved.Resolves[key]
	if tries < limit {
		a.saved.Resolves[key]++
		a.saved.Rewrites[key] = rw
		a.saveLocked()
	}
	a.mu.Unlock()
	if tries >= limit {
		note(fmt.Sprintf("%s still conflicts with %s; auto_resolve tried %d times, so it's yours now", branch, rw.Base, limit), true)
		return
	}
	wt, _ := p.branchWorktree(branch)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	changes := gitx.BaseChanges(ctx, wt.Path, branch, rw.Base, files, 20)
	prompt := resolvePrompt(rw.Base, files, changes, a.policy().Checks[p.id])
	if _, perr := s.promptAgent(proto.AgentPromptParams{ID: agent.ID, Text: prompt}); perr != nil {
		a.mu.Lock()
		if a.saved.Resolves[key] > 0 {
			a.saved.Resolves[key]--
		}
		a.saved.Conflicted[key] = "" // try again
		a.saveLocked()
		a.mu.Unlock()
		note(fmt.Sprintf("%s conflicts with %s, and asking %s to resolve it failed: %s", branch, rw.Base, agent.DisplayName(), perr.Message), true)
		return
	}
	note(fmt.Sprintf("%s conflicts with %s in %s: asked %s to resolve it", branch, rw.Base, strings.Join(files, ", "), agent.DisplayName()), false)
}

// resolvePrompt is what an agent is told about a conflict: where, what
// the other side did, and that both sides are to survive.
func resolvePrompt(base string, files []string, changes, check string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This branch's pull request conflicts with %s. Rebase it onto %s — run `git rebase %s` here; conch has just fetched it. It stops on conflicts in:\n\n", base, base, base)
	for _, f := range files {
		b.WriteString("- " + f + "\n")
	}
	if changes != "" {
		fmt.Fprintf(&b, "\nWhat %s did to them since this branch left it:\n\n```\n%s\n```\n", base, changes)
	}
	b.WriteString("\nResolve each conflict so that both sides survive: keep every change " + base + " made and every change this branch made. ")
	b.WriteString("Where they really contradict, stop and say so rather than pick one. Do not delete a file or a test to get past a conflict. ")
	b.WriteString("Then `git add` the files and `git rebase --continue` until the rebase is done")
	if check != "" {
		fmt.Fprintf(&b, ", and run `%s` to confirm both sides still work", check)
	}
	b.WriteString(". Do not push: conch checks the result — the base all there, nothing deleted that this branch did not delete before — and pushes it.")
	return b.String()
}

// shipRewrite replaces the pushed branch with its rebased self, once it
// has been checked: "" when it went, or what is in the way.
func (s *Server) shipRewrite(p *project, branch, dir string, rw rewrite, prNumber int) string {
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	switch {
	case gitx.InProgress(ctx, dir) != "":
		return "its rebase is not finished"
	case !gitx.IsAncestor(ctx, dir, rw.Base, "HEAD"):
		return "it is not on top of " + rw.Base + " yet"
	}
	after, err := gitx.Deleted(ctx, dir, rw.Base)
	if err != nil {
		return "what the rebase changed could not be read: " + err.Error()
	}
	var lost []string
	for _, f := range after {
		if !slices.Contains(rw.Deleted, f) {
			lost = append(lost, f)
		}
	}
	if len(lost) > 0 {
		return "resolving it deleted " + strings.Join(lost, ", ") + ", which the branch did not delete before; look at it before it goes out"
	}
	if err := gitx.PushOver(ctx, dir, branch, rw.Lease); err != nil {
		var rej *gitx.PushRejectedError
		if errors.As(err, &rej) {
			return "somebody pushed to it since the rebase began, so nothing was overwritten"
		}
		return "pushing it failed: " + err.Error()
	}
	key := branchKey(p.id, branch)
	a := s.autopilot
	a.mu.Lock()
	delete(a.saved.Rewrites, key)
	a.saveLocked()
	a.mu.Unlock()
	s.projects.request(p)
	s.projects.requestPRs(p)
	s.autopilotNote(proto.AutopilotNote{ProjectID: p.id, Branch: branch,
		Text: fmt.Sprintf("pushed %s, rebased onto %s, to #%d", branch, rw.Base, prNumber)})
	return ""
}
