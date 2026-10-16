package server

import (
	"log"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// A fresh worktree has the code and none of what makes it run: no
// node_modules, no virtualenv, no database. A project's setup command fixes
// that in every new worktree, in a terminal of its own so its output can
// be read and it can be stopped like anything else. The agent starts beside
// it rather than after it: an install can take minutes, and an agent reading
// the code first loses nothing.
//
// The command is the person's, stored with the project on this machine,
// never read from the repository: a command a clone could bring with it
// would run on checkout, which is not a thing to do to somebody who only
// wanted to look at a branch.

// setupScript wraps the command so the terminal says how it ended: gone on
// success, and on failure left open at a shell in the worktree, the error
// above it, which is the one place to fix it from. The command itself runs
// in the person's login shell so their PATH (nvm, pyenv, asdf) is the one
// it sees; the wrapper is sh, whose syntax every shell's -c doesn't share.
const setupScript = `"$CONCH_SHELL" -l -c "$CONCH_SETUP"
s=$?
if [ "$s" -eq 0 ]; then
	printf '\nconch: setup finished\n'
	exit 0
fi
printf '\nconch: setup failed (exit %d). This terminal stays open so you can look into it.\n' "$s"
exec "$CONCH_SHELL" -l
`

func (p *project) setSetup(cmd string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.info.Setup = strings.TrimSpace(cmd)
}

func (pm *projectManager) setSetup(sp proto.ProjectSetupParams) (proto.ProjectInfo, *proto.Error) {
	p, perr := pm.get(sp.ProjectID)
	if perr != nil {
		return proto.ProjectInfo{}, perr
	}
	if !p.snapshot().Git {
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "%s is not a git repository, so it has no worktrees to set up", p.root)
	}
	p.setSetup(sp.Command)
	pm.save()
	info := p.snapshot()
	pm.s.broadcast(proto.EventProjectUpdated, info)
	return info, nil
}

// startSetup runs the project's setup command in a new worktree, returning
// the pane it runs in, or "" when there is none or it could not start. A
// setup that cannot start is logged and left: the worktree is there, and
// the agent can still work in it.
func (s *Server) startSetup(p *project, path, branch string) string {
	cmd := p.snapshot().Setup
	if cmd == "" {
		return ""
	}
	shell := config.DefaultShell()
	info, perr := s.create(proto.PaneCreateParams{
		Name:    "setup",
		Command: []string{"/bin/sh", "-c", setupScript},
		Cwd:     path,
		Env: []string{
			"CONCH_SETUP=" + cmd,
			"CONCH_SHELL=" + shell,
			"CONCH_PROJECT=" + p.id,
			"CONCH_ROOT=" + p.root,
			"CONCH_WORKTREE=" + path,
			"CONCH_BRANCH=" + branch,
		},
	})
	if perr != nil {
		log.Printf("project %s: setup in %s: %v", p.id, path, perr)
		return ""
	}
	log.Printf("project %s: setup running in %s (%s)", p.id, info.ID, path)
	return info.ID
}
