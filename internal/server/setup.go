package server

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/conch/internal/agentsetup"
	"github.com/Amitgb14/conch/internal/proto"
)

// agentSetup reports what agents load in a directory. In a linked worktree
// it also compares with the main checkout: local files the worktree lacks,
// and setup items only the main checkout has.
func (s *Server) agentSetup(ap proto.AgentSetupParams) (proto.AgentSetupResult, *proto.Error) {
	abs, perr := resolveSetupDir(ap.Dir)
	if perr != nil {
		return proto.AgentSetupResult{}, perr
	}
	names := agentsetup.Names()
	if ap.Agent != "" {
		names = []string{ap.Agent}
	}

	res := proto.AgentSetupResult{Dir: abs, Agents: []proto.AgentSetup{}}
	mainDir := ""
	if p := s.projects.containing(abs); p != nil {
		res.ProjectID = p.id
		if wt, ok := p.worktreeFor(abs); ok && !wt.Main && wt.Path != p.root {
			res.Main, res.Worktree = p.root, wt.Path
			rel, _ := filepath.Rel(wt.Path, abs)
			if d := filepath.Join(p.root, rel); isDirectory(d) {
				mainDir = d
			} else {
				mainDir = p.root
			}
			ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
			res.LocalFiles = p.localFileStates(ctx, wt.Path)
			cancel()
		}
	}

	env := agentsetup.CurrentEnv()
	for _, name := range names {
		setup, ok := agentsetup.Inspect(env, name, abs)
		if !ok {
			return proto.AgentSetupResult{}, proto.Errorf(proto.ErrBadRequest, "unknown agent %q (known: %s)", name, strings.Join(agentsetup.Names(), ", "))
		}
		if mainDir != "" {
			main, _ := agentsetup.Inspect(env, name, mainDir)
			agentsetup.MarkMissing(&setup, main)
		}
		res.Agents = append(res.Agents, setup)
	}
	return res, nil
}

// agentSync gives the agents named the setup another has in a checkout,
// or puts an earlier sync back. Nothing is written unless Apply says so:
// the client shows the plan first.
// agentSkill installs or removes conch's skill in this machine's home.
func (s *Server) agentSkill(p proto.AgentSkillParams) (proto.AgentSkillResult, *proto.Error) {
	changes, err := agentsetup.InstallSkill(agentsetup.CurrentEnv(), p.Agents, p.Remove, p.Apply)
	if err != nil {
		return proto.AgentSkillResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	out := proto.AgentSkillResult{Changes: []proto.SkillChange{}, Applied: p.Apply}
	for _, c := range changes {
		out.Changes = append(out.Changes, proto.SkillChange{Path: c.Path, Agents: c.Agents, Action: c.Action, Detail: c.Detail, Error: c.Error})
	}
	return out, nil
}

func (s *Server) agentSync(sp proto.AgentSyncParams) (proto.AgentSyncResult, *proto.Error) {
	dir := sp.Dir
	if !sp.User { // the home is the place for a user sync; nothing to resolve
		var perr *proto.Error
		if dir, perr = resolveSetupDir(sp.Dir); perr != nil {
			return proto.AgentSyncResult{}, perr
		}
	}
	var res agentsetup.SyncResult
	var err error
	switch {
	case sp.User && sp.Undo:
		res, err = agentsetup.UndoUserSync(sp.Stamp)
	case sp.User:
		res, err = agentsetup.SyncUser(agentsetup.CurrentEnv(), sp.From, sp.To, sp.Apply)
	case sp.Undo:
		res, err = agentsetup.UndoSync(dir, sp.Stamp)
	default:
		res, err = agentsetup.Sync(dir, sp.From, sp.To, sp.Apply)
	}
	if err != nil {
		return proto.AgentSyncResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	out := proto.AgentSyncResult{Dir: res.Dir, From: res.From, To: res.To, Notes: res.Notes,
		Applied: sp.Apply && !sp.Undo, Undone: sp.Undo, Undo: res.Undo}
	for _, c := range res.Changes {
		out.Changes = append(out.Changes, proto.SyncChange{Agent: c.Agent, Kind: c.Kind, Name: c.Name,
			Path: c.Path, Action: c.Action, Detail: c.Detail, Done: c.Done, Error: c.Error})
	}
	if out.Undo != "" && !sp.User {
		// The record of what to put back lives in the checkout, so keep
		// .conch out of git as the handoff documents do.
		if err := excludeFromGit(res.Dir, ".conch/"); err != nil {
			log.Printf("agent sync: keeping .conch out of git in %s: %v", res.Dir, err)
		}
	}
	if sp.User {
		out.Undos = agentsetup.UserSyncUndos()
	} else {
		out.Undos = agentsetup.SyncUndos(res.Dir)
	}
	return out, nil
}

// libraryEnv is the environment the library is read and applied in; tests
// give it a scratch home and PATH.
var libraryEnv = agentsetup.CurrentEnv

// agentLibrary saves or imports into the library when asked, then says
// where each server and skill stands with each agent. Nothing is written
// to any agent here: libraryApply does that, after the plan is seen.
func (s *Server) agentLibrary(lp proto.AgentLibraryParams) (proto.AgentLibraryResult, *proto.Error) {
	env := libraryEnv()
	var out proto.AgentLibraryResult
	switch {
	case lp.Set != nil:
		if err := agentsetup.SaveLibrary(*lp.Set); err != nil {
			return out, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
	case lp.Import != "":
		ir, err := agentsetup.ImportLibrary(env, lp.Import)
		if err != nil {
			return out, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		out.Imported = append(ir.Servers, ir.Skills...)
		out.Skipped = ir.Skipped
	}
	lib, cells, err := agentsetup.LibraryStatus(env)
	if err != nil {
		return out, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if lib.Servers == nil {
		lib.Servers = []proto.LibraryServer{}
	}
	if lib.Skills == nil {
		lib.Skills = []proto.LibrarySkill{}
	}
	out.Library, out.Cells, out.Agents, out.Undos = lib, cells, agentsetup.SyncNames(), agentsetup.LibraryUndos()
	return out, nil
}

// libraryApply gives each agent what the library says, says what it would
// do, or puts an apply back.
func (s *Server) libraryApply(ap proto.LibraryApplyParams) (proto.AgentSyncResult, *proto.Error) {
	env := libraryEnv()
	var res agentsetup.SyncResult
	var err error
	if ap.Undo {
		res, err = agentsetup.UndoLibrary(env, ap.Stamp)
	} else {
		res, err = agentsetup.ApplyLibrary(env, ap.Apply)
	}
	if err != nil {
		return proto.AgentSyncResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	out := proto.AgentSyncResult{Dir: "~", From: "library", To: res.To, Notes: res.Notes,
		Applied: ap.Apply && !ap.Undo, Undone: ap.Undo, Undo: res.Undo, Undos: agentsetup.LibraryUndos()}
	for _, c := range res.Changes {
		out.Changes = append(out.Changes, proto.SyncChange{Agent: c.Agent, Kind: c.Kind, Name: c.Name,
			Path: c.Path, Action: c.Action, Detail: c.Detail, Done: c.Done, Error: c.Error})
	}
	return out, nil
}

// resolveSetupDir turns a client's directory into one on this machine.
func resolveSetupDir(dir string) (string, *proto.Error) {
	if home, err := os.UserHomeDir(); err == nil && (dir == "~" || strings.HasPrefix(dir, "~/")) {
		dir = filepath.Join(home, dir[1:])
	}
	abs, err := filepath.Abs(dir)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
	if err != nil {
		return "", proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return "", proto.Errorf(proto.ErrBadRequest, "%s is not a directory", abs)
	}
	return abs, nil
}

func isDirectory(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
