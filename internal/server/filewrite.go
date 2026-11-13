package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/Amitgb14/conch/internal/proto"
)

// fs.write saves a file edited in a window (Conch.app's Files tab) inside
// a project's checkout or one of its worktrees. Agents work in the same
// files, so a save names the content it began from (FSReadResult.Hash)
// and is refused when the file is something else by now: an edit never
// quietly writes over an agent's.

// contentHash is a file's fingerprint as fs.read and fs.write give it.
func contentHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Server) writeFile(c *client, wp proto.FSWriteParams) (proto.FSWriteResult, *proto.Error) {
	switch {
	case wp.Root == "":
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrBadRequest, "fs.write needs the checkout it writes in")
	case wp.Path == "" || wp.Path == ".":
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrBadRequest, "fs.write needs a file")
	case len(wp.Data) > proto.FSWriteMax:
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrBadRequest, "%d bytes is more than fs.write takes (%d)", len(wp.Data), proto.FSWriteMax)
	}
	root, perr := s.checkoutRoot(wp.Root)
	if perr != nil {
		return proto.FSWriteResult{}, perr
	}
	// An agent writes in its own project's checkouts through conch, as it
	// changes nothing else of another project's.
	if caller, ok := s.scopedCaller(c); ok {
		if perr := s.checkoutInScope(caller, proto.MethodFSWrite, root); perr != nil {
			return proto.FSWriteResult{}, perr
		}
	}
	path, perr := confine(root, wp.Path)
	if perr != nil {
		return proto.FSWriteResult{}, perr
	}
	// Through a link to the file it names, so the link stays a link.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := fs.FileMode(0o644)
	info, err := os.Stat(path)
	switch {
	case err == nil && !info.Mode().IsRegular():
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrBadRequest, "%s is not a regular file", wp.Path)
	case err == nil:
		mode = info.Mode().Perm()
		if !wp.Force {
			cur, err := os.ReadFile(path)
			if err != nil {
				return proto.FSWriteResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
			}
			if wp.Base == "" || contentHash(cur) != wp.Base {
				return proto.FSWriteResult{}, proto.Errorf(proto.ErrFileChanged, "%s changed on disk since it was opened", wp.Path)
			}
		}
	case errors.Is(err, fs.ErrNotExist):
		if !wp.Create && !wp.Force {
			return proto.FSWriteResult{}, proto.Errorf(proto.ErrFileChanged, "%s was deleted since it was opened", wp.Path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return proto.FSWriteResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
	default:
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if err := writeAtomic(path, []byte(wp.Data), mode); err != nil {
		return proto.FSWriteResult{}, proto.Errorf(proto.ErrInternal, "save %s: %v", wp.Path, err)
	}
	res := proto.FSWriteResult{Path: path, Size: int64(len(wp.Data)), Hash: contentHash([]byte(wp.Data))}
	if info, err := os.Stat(path); err == nil {
		res.ModTime = info.ModTime()
	}
	log.Printf("fs.write: %s (%d bytes)", path, len(wp.Data))
	return res, nil
}

// writeAtomic replaces path with data in one step: written beside it and
// renamed over it, so a reader — an agent, an editor, git — never sees a
// file half written, and a failed write leaves the old one.
func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".conch-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // gone already when the rename went through
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// projectOfCheckout is the project a checkout root belongs to: the one
// whose path, or one of whose worktrees', it is.
func (s *Server) projectOfCheckout(root string) string {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		real = root
	}
	for _, info := range s.projects.list() {
		cands := []string{info.Path}
		for _, wt := range info.Worktrees {
			cands = append(cands, wt.Path)
		}
		for _, c := range cands {
			if c == root || c == real {
				return info.ID
			}
		}
	}
	return ""
}
