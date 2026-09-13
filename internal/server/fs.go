package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// maxListEntries bounds a folder listing; huge folders are rarely where
// projects live, and the whole list travels to the client.
const maxListEntries = 2000

// resolvePath expands ~ and makes path absolute on this machine.
func resolvePath(path string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch {
	case path == "" || path == "~":
		path = home
	case strings.HasPrefix(path, "~/"):
		path = filepath.Join(home, path[2:])
	case !filepath.IsAbs(path):
		path = filepath.Join(home, path)
	}
	return filepath.Clean(path), nil
}

func (s *Server) listDir(lp proto.FSListParams) (proto.FSList, *proto.Error) {
	dir, err := resolvePath(lp.Path)
	if err != nil {
		return proto.FSList{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return proto.FSList{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	home, _ := os.UserHomeDir()
	out := proto.FSList{Path: dir, Home: home, Entries: []proto.FSEntry{}}
	if parent := filepath.Dir(dir); parent != dir {
		out.Parent = parent
	}

	projects := map[string]bool{}
	for _, p := range s.projects.list() {
		projects[p.Path] = true
	}
	for _, e := range entries {
		name := e.Name()
		if !lp.Hidden && strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			st, err := os.Stat(path)
			isDir = err == nil && st.IsDir()
		}
		if !isDir {
			continue
		}
		if len(out.Entries) == maxListEntries {
			out.Truncated = true
			break
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			real = path
		}
		_, gitErr := os.Stat(filepath.Join(path, ".git"))
		out.Entries = append(out.Entries, proto.FSEntry{Name: name, Git: gitErr == nil, Project: projects[real]})
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	return out, nil
}

func (s *Server) mkdir(mp proto.FSMkdirParams) (proto.FSList, *proto.Error) {
	path, err := resolvePath(mp.Path)
	if err != nil {
		return proto.FSList{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return proto.FSList{}, proto.Errorf(proto.ErrBadRequest, "%s already exists", path)
		}
		return proto.FSList{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	return s.listDir(proto.FSListParams{Path: filepath.Dir(path)})
}

// createProject makes a new folder, optionally a git repository, and adds
// it as a project. Existing folders are refused so nothing is overwritten.
func (s *Server) createProject(cp proto.ProjectCreateParams) (proto.ProjectInfo, *proto.Error) {
	path, err := resolvePath(cp.Path)
	if err != nil {
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "%s already exists; add it as a project instead", path)
		}
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if cp.Git {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		if err := gitx.InitRepo(ctx, path); err != nil {
			return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "git init: %v", err)
		}
	}
	p, err := s.projects.add(path, true)
	if err != nil {
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	return p.snapshot(), nil
}
