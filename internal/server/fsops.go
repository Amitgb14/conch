package server

import (
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/conch/internal/proto"
)

// fs.rename, fs.copy, fs.delete and fs.mkdir with a Root: the Files tab's
// own handling of a checkout's files. Each stays inside the checkout, never
// writes over something already there, and keeps away from .git, which is
// git's to change. An agent may do them in its own project's checkouts, as
// it may fs.write there, and nowhere else.

// writableCheckout is root, if it is a checkout this connection may change.
func (s *Server) writableCheckout(c *client, root, method string) (string, *proto.Error) {
	root, perr := s.checkoutRoot(root)
	if perr != nil {
		return "", perr
	}
	if caller, ok := s.scopedCaller(c); ok {
		if perr := s.projectInScope(caller, method, s.projectOfCheckout(root)); perr != nil {
			return "", perr
		}
	}
	return root, nil
}

// confineEntry is rel inside root as an entry of its folder: the folder
// is resolved and confined, the last name is not followed, so a symlink is
// renamed, copied or deleted as itself — wherever it points.
func confineEntry(root, rel string) (string, *proto.Error) {
	clean := strings.Trim(filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel))), "/")
	if rel == "" || clean == "." || clean == "" {
		return "", proto.Errorf(proto.ErrBadRequest, "the checkout itself can't be changed this way")
	}
	if first, _, _ := strings.Cut(clean, "/"); first == ".git" {
		return "", proto.Errorf(proto.ErrBadRequest, "%s is git's own", rel)
	}
	dir, name := filepath.Split(filepath.FromSlash(clean))
	parent, perr := confine(root, filepath.ToSlash(dir))
	if perr != nil {
		return "", perr
	}
	return filepath.Join(parent, name), nil
}

// fsTarget is where a rename or copy goes: not there yet, and not inside
// what moves.
func fsTarget(root, from, to string) (string, *proto.Error) {
	if to == "" {
		return "", proto.Errorf(proto.ErrBadRequest, "where to? a new path is needed")
	}
	dst, perr := confineEntry(root, to)
	if perr != nil {
		return "", perr
	}
	if _, err := os.Lstat(dst); err == nil {
		return "", proto.Errorf(proto.ErrBadRequest, "%s already exists", to)
	}
	if dst == from || within(dst, from) {
		return "", proto.Errorf(proto.ErrBadRequest, "%s is inside what it would move", to)
	}
	return dst, nil
}

func (s *Server) renamePath(c *client, p proto.FSPathParams) (proto.FSPathResult, *proto.Error) {
	root, perr := s.writableCheckout(c, p.Root, proto.MethodFSRename)
	if perr != nil {
		return proto.FSPathResult{}, perr
	}
	src, perr := confineEntry(root, p.Path)
	if perr != nil {
		return proto.FSPathResult{}, perr
	}
	if _, err := os.Lstat(src); err != nil {
		return proto.FSPathResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	dst, perr := fsTarget(root, src, p.To)
	if perr != nil {
		return proto.FSPathResult{}, perr
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return proto.FSPathResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return proto.FSPathResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	s.lineIdx.drop(src)
	log.Printf("fs.rename: %s → %s", src, dst)
	return proto.FSPathResult{Path: dst}, nil
}

func (s *Server) copyPath(c *client, p proto.FSPathParams) (proto.FSPathResult, *proto.Error) {
	root, perr := s.writableCheckout(c, p.Root, proto.MethodFSCopy)
	if perr != nil {
		return proto.FSPathResult{}, perr
	}
	src, perr := confineEntry(root, p.Path)
	if perr != nil {
		return proto.FSPathResult{}, perr
	}
	if _, err := os.Lstat(src); err != nil {
		return proto.FSPathResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	dst, perr := fsTarget(root, src, p.To)
	if perr != nil {
		return proto.FSPathResult{}, perr
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return proto.FSPathResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if err := copyTree(src, dst); err != nil {
		os.RemoveAll(dst) // half a copy is worse than none
		return proto.FSPathResult{}, proto.Errorf(proto.ErrBadRequest, "copy %s: %v", p.Path, err)
	}
	return proto.FSPathResult{Path: dst}, nil
}

// copyTree copies a file, a link (as a link) or a folder and all in it.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, to)
		case d.IsDir():
			return os.Mkdir(to, info.Mode().Perm()|0o700)
		case info.Mode().IsRegular():
			return copyNew(p, to, info.Mode().Perm())
		default:
			return nil // a fifo or a socket is not copied
		}
	})
}

func copyNew(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (s *Server) deletePath(c *client, p proto.FSPathParams) (proto.FSPathResult, *proto.Error) {
	root, perr := s.writableCheckout(c, p.Root, proto.MethodFSDelete)
	if perr != nil {
		return proto.FSPathResult{}, perr
	}
	path, perr := confineEntry(root, p.Path)
	if perr != nil {
		return proto.FSPathResult{}, perr
	}
	if _, err := os.Lstat(path); err != nil {
		return proto.FSPathResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if err := os.RemoveAll(path); err != nil {
		return proto.FSPathResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	s.lineIdx.drop(path)
	log.Printf("fs.delete: %s", path)
	return proto.FSPathResult{Path: path}, nil
}

// mkdirIn makes a folder inside a checkout, and the folders on the way.
func (s *Server) mkdirIn(c *client, mp proto.FSMkdirParams) (proto.FSList, *proto.Error) {
	if mp.SSH != "" {
		return proto.FSList{}, proto.Errorf(proto.ErrBadRequest, "a checkout is on this machine, not on a pane's ssh host")
	}
	root, perr := s.writableCheckout(c, mp.Root, proto.MethodFSMkdir)
	if perr != nil {
		return proto.FSList{}, perr
	}
	path, perr := confineEntry(root, mp.Path)
	if perr != nil {
		return proto.FSList{}, perr
	}
	if _, err := os.Lstat(path); err == nil {
		return proto.FSList{}, proto.Errorf(proto.ErrBadRequest, "%s already exists", mp.Path)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return proto.FSList{}, proto.Errorf(proto.ErrBadRequest, "%s is in the way", mp.Path)
		}
		return proto.FSList{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	return proto.FSList{Path: path, Entries: []proto.FSEntry{}}, nil
}
