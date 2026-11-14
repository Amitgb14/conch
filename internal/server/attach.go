package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// ---- agent.attach ----

// attachFolder makes the folder one message's attachments are copied into,
// in the checkout of the agent in a pane: <checkout>/.conch/attachments/ID.
// Inside the checkout, because every agent can read there without asking;
// kept out of git through info/exclude, which is never committed, so no
// .gitignore changes. Folders older than proto.AttachKeep are removed on
// the way: the checkout being used again is when its old attachments go.
func (s *Server) attachFolder(p proto.AgentAttachParams) (proto.AgentAttachResult, *proto.Error) {
	e, perr := s.get(p.ID)
	if perr != nil {
		return proto.AgentAttachResult{}, perr
	}
	info := e.info()
	switch {
	case info.State != proto.PaneRunning:
		return proto.AgentAttachResult{}, proto.Errorf(proto.ErrBadRequest, "pane %s has exited", info.ID)
	case info.Agent == nil:
		return proto.AgentAttachResult{}, proto.Errorf(proto.ErrBadRequest, "pane %s is not running an agent", info.ID)
	}
	root := s.checkoutOf(e.dir)
	base := filepath.Join(root, filepath.FromSlash(proto.AttachDir))
	if err := os.MkdirAll(base, 0o755); err != nil {
		return proto.AgentAttachResult{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	if err := excludeFromGit(root, proto.AttachDir+"/"); err != nil {
		return proto.AgentAttachResult{}, proto.Errorf(proto.ErrInternal, "keeping attachments out of git: %v", err)
	}
	dropOldAttachments(base, time.Now())
	var dir string
	for range 5 {
		d := filepath.Join(base, randomHex(4))
		if err := os.Mkdir(d, 0o755); err == nil {
			dir = d
			break
		} else if !os.IsExist(err) {
			return proto.AgentAttachResult{}, proto.Errorf(proto.ErrInternal, "%v", err)
		}
	}
	if dir == "" {
		return proto.AgentAttachResult{}, proto.Errorf(proto.ErrInternal, "no free folder for attachments in %s", base)
	}
	return proto.AgentAttachResult{Dir: dir, Root: root, Cwd: e.dir, MaxSize: proto.AttachMaxSize, MaxFiles: proto.AttachMaxFiles}, nil
}

// checkoutOf is the checkout a folder is in: the deepest project or
// worktree holding it, else the top of its git repository, else the
// folder itself.
func (s *Server) checkoutOf(dir string) string {
	best := ""
	for _, info := range s.projects.list() {
		cands := []string{info.Path}
		for _, wt := range info.Worktrees {
			cands = append(cands, wt.Path)
		}
		for _, c := range cands {
			if c == "" {
				continue
			}
			for _, form := range []string{c, realDir(c)} {
				if (dir == form || strings.HasPrefix(dir, form+string(filepath.Separator))) && len(form) > len(best) {
					best = form
				}
			}
		}
	}
	if best != "" {
		return best
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			return top
		}
	}
	return dir
}

// dropOldAttachments removes the folders under base not changed for
// proto.AttachKeep. Anything else there — a file somebody put in — stays.
func dropOldAttachments(base string, now time.Time) {
	ents, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, d := range ents {
		if !d.IsDir() {
			continue
		}
		fi, err := d.Info()
		if err != nil || now.Sub(fi.ModTime()) < proto.AttachKeep {
			continue
		}
		_ = os.RemoveAll(filepath.Join(base, d.Name()))
	}
}
