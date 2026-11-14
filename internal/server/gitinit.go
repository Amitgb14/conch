package server

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// gitInitMu keeps two git_inits of one folder from racing: the second
// would find the first's .git half made.
var gitInitMu sync.Mutex

// notGitHint is what a task or a branch in a plain-folder project is told:
// the way out, not only the refusal.
const notGitHint = "make it one with conch project git-init, or Initialize Git on its row"

// gitInit makes the plain folder of project id a git repository with an
// empty first commit, and adopts it at once so its clients see Git without
// waiting for a tick. Files already there are left untracked (InitRepo).
func (s *Server) gitInit(id string) (proto.ProjectInfo, *proto.Error) {
	p, perr := s.projects.get(id)
	if perr != nil {
		return proto.ProjectInfo{}, perr
	}
	gitInitMu.Lock()
	defer gitInitMu.Unlock()
	name := p.snapshot().Name
	if s.projects.adoptGit(p) != "" {
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "%s is already a git repository", name)
	}
	if st, err := os.Stat(p.root); err != nil || !st.IsDir() {
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "%s is gone: no folder at %s", name, p.root)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	// A folder inside another repository would become a repository nested
	// in it: the outer one would see it as an untracked folder, or worse,
	// as a submodule nobody asked for.
	repo, err := gitx.Discover(ctx, p.root)
	switch {
	case err == nil && repo.Root != p.root:
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest,
			"%s is inside the git repository %s; add that repository as the project instead", name, repo.Root)
	case err == nil:
		// Made a repository behind adoptGit's back (no .git entry it
		// could see, e.g. GIT_DIR); take it as it is.
	case !errors.Is(err, gitx.ErrNotRepo):
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrInternal, "%v", err)
	default:
		if err := gitx.InitRepo(ctx, p.root); err != nil {
			return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "git init: %v", err)
		}
	}
	if s.projects.adoptGit(p) == "" {
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrInternal, "%s: git init left no repository conch can read", name)
	}
	// refreshNow applies what git now says — Git, its base and its one
	// branch — and broadcasts project.updated, since all of that changed.
	s.projects.refreshNow(p)
	return p.snapshot(), nil
}
