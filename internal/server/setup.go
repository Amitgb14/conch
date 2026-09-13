package server

import (
	"context"
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
	dir := ap.Dir
	if home, err := os.UserHomeDir(); err == nil && (dir == "~" || strings.HasPrefix(dir, "~/")) {
		dir = filepath.Join(home, dir[1:])
	}
	abs, err := filepath.Abs(dir)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
	if err != nil {
		return proto.AgentSetupResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return proto.AgentSetupResult{}, proto.Errorf(proto.ErrBadRequest, "%s is not a directory", abs)
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

func isDirectory(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
