package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/transfer"
)

// Browsing a checkout's files, for the file explorer.
//
// fs.list on its own walks anywhere from the home directory, which is what
// the project picker needs. A listing with a Root is confined instead: the
// root has to be a project's folder or one of its worktrees, and every path
// is resolved — symlinks included — and refused if it leaves it.

// fileStatusTTL is how long a checkout's git status is reused between
// listings. Expanding a few folders in a row then costs one git status, and
// a watched worktree drops its entry as soon as a file changes.
const fileStatusTTL = 2 * time.Second

// checkoutState is what git says about a checkout's files.
type checkoutState struct {
	at      time.Time
	status  map[string]string // path → code
	ignored map[string]bool   // paths; a folder ends in "/"
}

type fileStates struct {
	mu sync.Mutex
	m  map[string]*checkoutState // by root
}

func (fs *fileStates) drop(root string) {
	fs.mu.Lock()
	delete(fs.m, root)
	fs.mu.Unlock()
}

// get returns the checkout's git state, reading it again once it is stale.
// A folder that is not a git checkout has an empty one.
func (fs *fileStates) get(root string) *checkoutState {
	fs.mu.Lock()
	st := fs.m[root]
	fs.mu.Unlock()
	if st != nil && time.Since(st.at) < fileStatusTTL {
		return st
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	st = &checkoutState{at: time.Now(), status: map[string]string{}, ignored: map[string]bool{}}
	if files, err := gitx.StatusFiles(ctx, root); err == nil {
		for _, f := range files {
			st.status[f.Path] = f.Code
		}
		if paths, err := gitx.IgnoredPaths(ctx, root); err == nil {
			for _, p := range paths {
				st.ignored[p] = true
			}
		}
	}
	fs.mu.Lock()
	if fs.m == nil {
		fs.m = map[string]*checkoutState{}
	}
	fs.m[root] = st
	fs.mu.Unlock()
	return st
}

// isIgnored reports whether rel, or a folder it is in, is ignored.
func (st *checkoutState) isIgnored(rel string, dir bool) bool {
	if st.ignored[rel] || (dir && st.ignored[rel+"/"]) {
		return true
	}
	for i := strings.LastIndexByte(rel, '/'); i > 0; i = strings.LastIndexByte(rel[:i], '/') {
		if st.ignored[rel[:i+1]] {
			return true
		}
	}
	return false
}

// statusRank orders codes by how much a folder should say about them: a
// conflict matters most, an untracked file least.
var statusRank = map[string]int{"U": 6, "M": 5, "T": 5, "D": 4, "R": 3, "C": 3, "A": 2, "?": 1}

// folderStatus is the most telling code among the files under rel.
func (st *checkoutState) folderStatus(rel string) string {
	best := ""
	prefix := rel + "/"
	for p, code := range st.status {
		if strings.HasPrefix(p, prefix) && statusRank[code] > statusRank[best] {
			best = code
		}
	}
	return best
}

// checkoutRoot returns root cleaned, if it is a project's folder or one of
// its worktrees on this machine.
func (s *Server) checkoutRoot(root string) (string, *proto.Error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", proto.Errorf(proto.ErrBadRequest, "a checkout listing needs an absolute root, not %q", root)
	}
	root = filepath.Clean(root)
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
				return root, nil
			}
		}
	}
	return "", proto.Errorf(proto.ErrBadRequest, "%s is not a project or one of its worktrees", root)
}

// confine resolves rel, a slash-separated path under root, and refuses one
// that leaves root: by "..", by being absolute, or through a symlink.
func confine(root, rel string) (string, *proto.Error) {
	local := filepath.FromSlash(rel)
	if rel == "" || rel == "." {
		local = "."
	} else if !filepath.IsLocal(local) {
		return "", proto.Errorf(proto.ErrBadRequest, "%q is not a path inside the checkout", rel)
	}
	abs := filepath.Join(root, local)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Missing or a broken link: the caller's own open says so.
		return abs, nil
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	if !within(real, realRoot) {
		return "", proto.Errorf(proto.ErrBadRequest, "%s leads outside the checkout", rel)
	}
	return abs, nil
}

func (s *Server) listCheckout(lp proto.FSListParams) (proto.FSList, *proto.Error) {
	root, perr := s.checkoutRoot(lp.Root)
	if perr != nil {
		return proto.FSList{}, perr
	}
	dir, perr := confine(root, lp.Path)
	if perr != nil {
		return proto.FSList{}, perr
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return proto.FSList{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	home, _ := os.UserHomeDir()
	out := proto.FSList{Path: dir, Home: home, Entries: []proto.FSEntry{}}
	if dir != root {
		out.Parent = filepath.Dir(dir)
	}
	st := s.files.get(root)
	relDir := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(dir, root), string(filepath.Separator)))
	for _, e := range entries {
		name := e.Name()
		if name == ".git" {
			// git's own, never a file anyone browses for — and a
			// thousand objects nobody wants in a tree. It used to show
			// when hidden files were asked for; now that they are shown
			// by default, that would be the first thing in every
			// checkout.
			continue
		}
		if !lp.Hidden && strings.HasPrefix(name, ".") {
			continue
		}
		rel := name
		if relDir != "" {
			rel = relDir + "/" + name
		}
		fe := fileEntry(dir, e)
		if !lp.Files && !fe.Dir {
			continue
		}
		fe.Ignored = st.isIgnored(rel, fe.Dir)
		if fe.Ignored && !lp.Ignored {
			continue
		}
		if fe.Dir {
			fe.Status = st.folderStatus(rel)
		} else {
			fe.Status = st.status[rel]
		}
		if len(out.Entries) == maxListEntries {
			out.Truncated = true
			break
		}
		out.Entries = append(out.Entries, fe)
	}
	sortEntries(out.Entries)
	return out, nil
}

// fileEntry describes one entry of dir, saying for a symlink what it leads
// to.
func fileEntry(dir string, e os.DirEntry) proto.FSEntry {
	fe := proto.FSEntry{Name: e.Name(), Dir: e.IsDir()}
	if info, err := e.Info(); err == nil {
		fe.ModTime = info.ModTime()
		fe.Size = info.Size()
	}
	if e.Type()&os.ModeSymlink != 0 {
		fe.Symlink = true
		if target, err := os.Stat(filepath.Join(dir, e.Name())); err != nil {
			fe.Broken = true
		} else {
			fe.Dir = target.IsDir()
			fe.Size = target.Size()
			fe.ModTime = target.ModTime()
		}
	}
	if fe.Dir {
		fe.Size = 0
	}
	return fe
}

// sortEntries puts folders first, as every file tree has them.
func sortEntries(entries []proto.FSEntry) {
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

// listFiles lists a folder anywhere on the machine with its files, for
// browsing the machine itself rather than a checkout (fs.transfer.v1).
// There is no git here to ask, and nothing to confine to: whoever is
// connected already has a shell on this machine.
//
// With SSH it lists the host that pane is logged in to instead.
func (s *Server) listFiles(lp proto.FSListParams) (proto.FSList, *proto.Error) {
	fsys, perr := s.filesOn(lp.SSH)
	if perr != nil {
		return proto.FSList{}, perr
	}
	dir, err := resolveOn(fsys, lp.Path)
	if err != nil {
		return proto.FSList{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return proto.FSList{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	home, _ := fsys.Home()
	out := proto.FSList{Path: dir, Home: home, Entries: []proto.FSEntry{}}
	if parent := filepath.Dir(dir); parent != dir {
		out.Parent = parent
	}
	for _, e := range entries {
		if !lp.Hidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if len(out.Entries) == maxListEntries {
			out.Truncated = true
			break
		}
		out.Entries = append(out.Entries, infoEntry(fsys, dir, e))
	}
	sortEntries(out.Entries)
	return out, nil
}

// infoEntry is fileEntry for what an FS lists.
func infoEntry(fsys transfer.FS, dir string, info fs.FileInfo) proto.FSEntry {
	fe := proto.FSEntry{Name: info.Name(), Dir: info.IsDir(), Size: info.Size(), ModTime: info.ModTime()}
	if info.Mode()&fs.ModeSymlink != 0 {
		fe.Symlink = true
		if target, err := fsys.Stat(filepath.Join(dir, info.Name())); err != nil {
			fe.Broken = true
		} else {
			fe.Dir, fe.Size, fe.ModTime = target.IsDir(), target.Size(), target.ModTime()
		}
	}
	if fe.Dir {
		fe.Size = 0
	}
	return fe
}

// readFile returns the start of a file in a checkout, or without a root
// anywhere on the machine (fs.transfer.v1), or says it is binary.
func (s *Server) readFile(rp proto.FSReadParams) (proto.FSReadResult, *proto.Error) {
	if rp.Path == "" || rp.Path == "." {
		return proto.FSReadResult{}, proto.Errorf(proto.ErrBadRequest, "fs.read needs a file")
	}
	fsys, perr := s.filesOn(rp.SSH)
	if perr != nil {
		return proto.FSReadResult{}, perr
	}
	var path string
	if rp.Root == "" || rp.SSH != "" {
		var err error
		if path, err = resolveOn(fsys, rp.Path); err != nil {
			return proto.FSReadResult{}, proto.Errorf(proto.ErrInternal, "%v", err)
		}
	} else {
		root, perr := s.checkoutRoot(rp.Root)
		if perr != nil {
			return proto.FSReadResult{}, perr
		}
		if path, perr = confine(root, rp.Path); perr != nil {
			return proto.FSReadResult{}, perr
		}
	}
	if rp.Offset < 0 {
		return proto.FSReadResult{}, proto.Errorf(proto.ErrBadRequest, "negative offset")
	}
	max := rp.Max
	if max <= 0 || max > proto.FSReadMax {
		max = proto.FSReadMax
	}
	// A folder has nothing to read, and a fifo or device would block — even
	// opening a fifo does — so the file is looked at before it is opened,
	// and opened without blocking in case it changed in between.
	if info, err := fsys.Stat(path); err != nil {
		return proto.FSReadResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	} else if !info.Mode().IsRegular() {
		return proto.FSReadResult{}, proto.Errorf(proto.ErrBadRequest, "%s is not a regular file", rp.Path)
	}
	f, err := fsys.Open(path)
	if err != nil {
		return proto.FSReadResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return proto.FSReadResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if !info.Mode().IsRegular() {
		return proto.FSReadResult{}, proto.Errorf(proto.ErrBadRequest, "%s is not a regular file", rp.Path)
	}
	res := proto.FSReadResult{Path: path, Size: info.Size(), ModTime: info.ModTime()}
	buf := make([]byte, max)
	n, err := io.ReadFull(io.NewSectionReader(f, rp.Offset, int64(max)), buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return proto.FSReadResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	data := buf[:n]
	res.Truncated = rp.Offset+int64(n) < info.Size()
	if n > 0 {
		res.MIME = http.DetectContentType(data)
	}
	if isBinary(data, res.Truncated) {
		res.Binary = true
		return res, nil
	}
	res.Data = string(data)
	return res, nil
}

// isBinary guesses whether data is not text: a NUL early on, or bytes that
// are not UTF-8. A cut in the middle of a character at the end of a
// truncated read is not held against it.
func isBinary(data []byte, truncated bool) bool {
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	if truncated {
		for i := 0; i < utf8.UTFMax && len(data) > 0 && !utf8.Valid(data); i++ {
			data = data[:len(data)-1]
		}
	}
	return !utf8.Valid(data)
}
