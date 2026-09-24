package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// Moving a worktree to another machine. The client asks this machine what
// the move takes (worktree.describe), asks the other which of the branch's
// commits it already has (worktree.have), has this one pack the rest with
// the uncommitted work (worktree.pack), reads the pack (worktree.pack_read),
// uploads it there (fs.upload) and has that machine rebuild the worktree
// (worktree.unpack). The two servers never talk to each other.

const (
	moveHistory  = 1000            // commits offered to the other machine
	moveTimeout  = 5 * time.Minute // bundling and applying a big branch
	cloneTimeout = 10 * time.Minute
	packIdle     = 10 * time.Minute // an unread pack is deleted after this
	packVersion  = 1
)

// packLimit is the largest pack: it is uploaded to the other machine.
var packLimit = maxUploadSize

// packManifest leads every pack.
type packManifest struct {
	Version int    `json:"version"`
	Branch  string `json:"branch"`
	Head    string `json:"head"`
	Have    string `json:"have,omitempty"`
	Bundle  bool   `json:"bundle,omitempty"`
}

// Entries of a pack besides manifest.json.
const (
	packBundle   = "branch.bundle"
	packStaged   = "staged.patch"
	packUnstaged = "unstaged.patch"
	packFiles    = "files/"
)

// ---- worktree.describe ----

func (s *Server) describeWorktree(p proto.WorktreeRef) (proto.WorktreeMoveInfo, *proto.Error) {
	proj, wt, perr := s.moveSource(p.ProjectID, p.Path)
	if perr != nil {
		return proto.WorktreeMoveInfo{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	info := proto.WorktreeMoveInfo{Branch: wt.Branch, Head: wt.Head, Base: proj.snapshot().Base, Remote: gitx.RemoteURL(ctx, proj.root)}
	var err error
	if info.History, err = gitx.History(ctx, wt.Path, "HEAD", moveHistory); err != nil {
		return info, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if len(info.History) > 0 {
		info.Head = info.History[0]
	}
	st, err := gitx.WorktreeStatus(ctx, wt.Path)
	if err != nil {
		return info, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if st.Conflicts > 0 {
		return info, proto.Errorf(proto.ErrBadRequest, "%s has unresolved conflicts; finish or abort the merge first", wt.Path)
	}
	info.Staged, info.Unstaged, info.Other = st.Staged, st.Unstaged, st.Untracked
	if info.Local, err = gitx.UntrackedFiles(ctx, wt.Path, proj.snapshot().LocalFiles, true); err != nil {
		return info, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	return info, nil
}

// moveSource finds a worktree that can be moved: one of the project's, on a
// branch.
func (s *Server) moveSource(projectID, path string) (*project, proto.WorktreeInfo, *proto.Error) {
	proj, perr := s.projects.get(projectID)
	if perr != nil {
		return nil, proto.WorktreeInfo{}, perr
	}
	if !proj.snapshot().Git {
		return nil, proto.WorktreeInfo{}, proto.Errorf(proto.ErrBadRequest, "%s is not a git repository", proj.root)
	}
	for _, wt := range proj.snapshot().Worktrees {
		if wt.Path != path {
			continue
		}
		if wt.Branch == "" {
			return nil, wt, proto.Errorf(proto.ErrBadRequest, "%s isn't on a branch; check one out before moving it", path)
		}
		return proj, wt, nil
	}
	return nil, proto.WorktreeInfo{}, proto.Errorf(proto.ErrNotFound, "no worktree %s in %s", path, proj.root)
}

// ---- worktree.have ----

func (s *Server) haveCommits(p proto.WorktreeHaveParams) (proto.WorktreeHaveResult, *proto.Error) {
	proj, perr := s.projects.get(p.ProjectID)
	if perr != nil {
		return proto.WorktreeHaveResult{}, perr
	}
	if !proj.snapshot().Git {
		return proto.WorktreeHaveResult{}, proto.Errorf(proto.ErrBadRequest, "%s is not a git repository", proj.root)
	}
	if len(p.Commits) > moveHistory {
		p.Commits = p.Commits[:moveHistory]
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	have, err := gitx.HaveCommits(ctx, proj.root, p.Commits)
	if err != nil {
		return proto.WorktreeHaveResult{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	if have == nil {
		have = []string{}
	}
	return proto.WorktreeHaveResult{Have: have}, nil
}

// ---- worktree.pack and worktree.pack_read ----

// packs are packed worktrees waiting for the client to read them.
type packs struct {
	dir string
	now func() time.Time

	mu     sync.Mutex
	active map[string]*packFile
}

type packFile struct {
	path string
	size int64
	last time.Time
}

func newPacks(configDir string) *packs {
	return &packs{dir: filepath.Join(configDir, "moves"), now: time.Now, active: map[string]*packFile{}}
}

func (s *Server) packWorktree(p proto.WorktreePackParams) (proto.WorktreePack, *proto.Error) {
	proj, wt, perr := s.moveSource(p.ProjectID, p.Path)
	if perr != nil {
		return proto.WorktreePack{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), moveTimeout)
	defer cancel()
	if err := os.MkdirAll(s.packs.dir, 0o700); err != nil {
		return proto.WorktreePack{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	id := randomHex(8)
	path := filepath.Join(s.packs.dir, id+".tar.gz")
	size, err := writePack(ctx, path, proj, wt, p.Have)
	if err != nil {
		os.Remove(path)
		return proto.WorktreePack{}, proto.Errorf(proto.ErrBadRequest, "pack %s: %v", wt.Path, err)
	}
	if size > packLimit {
		os.Remove(path)
		return proto.WorktreePack{}, proto.Errorf(proto.ErrBadRequest, "%s packs to %s; the limit is %s (large untracked files?)",
			wt.Path, sizeText(size), sizeText(packLimit))
	}
	s.packs.mu.Lock()
	s.packs.active[id] = &packFile{path: path, size: size, last: s.packs.now()}
	s.packs.mu.Unlock()
	log.Printf("project %s: packed %s (%s) for another machine", proj.id, wt.Path, sizeText(size))
	return proto.WorktreePack{ID: id, Name: gitx.Slug(wt.Branch) + ".tar.gz", Size: size}, nil
}

// writePack writes the worktree's branch (the commits have doesn't reach),
// its uncommitted changes, and its untracked and local files.
func writePack(ctx context.Context, path string, proj *project, wt proto.WorktreeInfo, have string) (int64, error) {
	head, err := gitx.BranchHead(ctx, proj.root, wt.Branch)
	if err != nil {
		return 0, err
	}
	man := packManifest{Version: packVersion, Branch: wt.Branch, Head: head, Have: have}
	tmp, err := os.MkdirTemp(filepath.Dir(path), "pack-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	bundle := filepath.Join(tmp, packBundle)
	switch err := gitx.CreateBundle(ctx, proj.root, bundle, wt.Branch, have); {
	case err == nil:
		man.Bundle = true
	case !errors.Is(err, gitx.ErrEmptyBundle):
		return 0, err
	}
	staged, unstaged, err := gitx.WorkPatches(ctx, wt.Path)
	if err != nil {
		return 0, err
	}
	others, err := gitx.OtherFiles(ctx, wt.Path)
	if err != nil {
		return 0, err
	}
	local, err := gitx.UntrackedFiles(ctx, wt.Path, proj.snapshot().LocalFiles, true)
	if err != nil {
		return 0, err
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	zw, _ := gzip.NewWriterLevel(f, gzip.BestSpeed) // bundles are compressed already
	tw := tar.NewWriter(zw)
	manifest, _ := json.Marshal(man)
	add := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err := add("manifest.json", manifest); err != nil {
		return 0, err
	}
	if man.Bundle {
		if err := addFile(tw, packBundle, bundle); err != nil {
			return 0, err
		}
	}
	for name, patch := range map[string][]byte{packStaged: staged, packUnstaged: unstaged} {
		if len(patch) > 0 {
			if err := add(name, patch); err != nil {
				return 0, err
			}
		}
	}
	seen := map[string]bool{}
	for _, rel := range append(others, local...) {
		if seen[rel] {
			continue
		}
		seen[rel] = true
		if err := addFile(tw, packFiles+rel, filepath.Join(wt.Path, rel)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // deleted since git listed it
			}
			return 0, err
		}
	}
	if err := tw.Close(); err != nil {
		return 0, err
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), f.Close()
}

// addFile adds the file (or symlink) at src to a pack as name, keeping its
// mode. Folders and other kinds of file are left out.
func addFile(tw *tar.Writer, name, src string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	hdr := &tar.Header{Name: name, Mode: int64(st.Mode().Perm()), ModTime: st.ModTime()}
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		hdr.Typeflag, hdr.Linkname = tar.TypeSymlink, target
		return tw.WriteHeader(hdr)
	case !st.Mode().IsRegular():
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	hdr.Typeflag, hdr.Size = tar.TypeReg, st.Size()
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.CopyN(tw, in, st.Size())
	return err
}

func (pk *packs) read(p proto.WorktreePackReadParams) (proto.WorktreePackChunk, *proto.Error) {
	pk.mu.Lock()
	pf := pk.active[p.ID]
	if pf != nil {
		pf.last = pk.now()
	}
	pk.mu.Unlock()
	if pf == nil {
		return proto.WorktreePackChunk{}, proto.Errorf(proto.ErrNotFound, "no pack %q (it may have been read or abandoned)", p.ID)
	}
	if p.Offset < 0 || p.Offset > pf.size {
		return proto.WorktreePackChunk{}, proto.Errorf(proto.ErrBadRequest, "offset %d is outside the pack's %d bytes", p.Offset, pf.size)
	}
	f, err := os.Open(pf.path)
	if err != nil {
		return proto.WorktreePackChunk{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	defer f.Close()
	buf := make([]byte, min(int64(proto.UploadChunkSize), pf.size-p.Offset))
	if _, err := f.ReadAt(buf, p.Offset); err != nil && !errors.Is(err, io.EOF) {
		return proto.WorktreePackChunk{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	out := proto.WorktreePackChunk{Data: buf, EOF: p.Offset+int64(len(buf)) == pf.size}
	if out.EOF {
		pk.drop(p.ID)
	}
	return out, nil
}

func (pk *packs) drop(id string) {
	pk.mu.Lock()
	pf := pk.active[id]
	delete(pk.active, id)
	pk.mu.Unlock()
	if pf != nil {
		os.Remove(pf.path)
	}
}

// run deletes packs nobody reads, and on start what an earlier run left.
func (pk *packs) run(quit <-chan struct{}) {
	if entries, err := os.ReadDir(pk.dir); err == nil {
		for _, e := range entries {
			os.RemoveAll(filepath.Join(pk.dir, e.Name()))
		}
	}
	t := time.NewTicker(packIdle / 4)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			pk.sweep()
		case <-quit:
			return
		}
	}
}

func (pk *packs) sweep() {
	pk.mu.Lock()
	var stale []string
	for id, pf := range pk.active {
		if pk.now().Sub(pf.last) >= packIdle {
			stale = append(stale, id)
		}
	}
	pk.mu.Unlock()
	for _, id := range stale {
		pk.drop(id)
	}
}

// ---- worktree.unpack ----

func (s *Server) unpackWorktree(p proto.WorktreeUnpackParams) (proto.WorktreeUnpackResult, *proto.Error) {
	var res proto.WorktreeUnpackResult
	proj, perr := s.projects.get(p.ProjectID)
	if perr != nil {
		return res, perr
	}
	if !proj.snapshot().Git {
		return res, proto.Errorf(proto.ErrBadRequest, "%s is not a git repository", proj.root)
	}
	// Only a file uploaded here, which is removed once used.
	if rel, err := filepath.Rel(s.uploads.dir, filepath.Clean(p.Pack)); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return res, proto.Errorf(proto.ErrBadRequest, "%s is not an upload", p.Pack)
	}
	defer os.Remove(p.Pack)

	if err := os.MkdirAll(s.packs.dir, 0o700); err != nil {
		return res, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	tmp, err := os.MkdirTemp(s.packs.dir, "unpack-")
	if err != nil {
		return res, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	defer os.RemoveAll(tmp)
	man, files, err := readPack(p.Pack, tmp)
	if err != nil {
		return res, proto.Errorf(proto.ErrBadRequest, "read the pack: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), moveTimeout)
	defer cancel()
	// Ask git, not the last refresh: moving a checked-out branch's ref
	// would pull it from under its worktree.
	wts, err := gitx.Worktrees(ctx, proj.root)
	if err != nil {
		return res, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	for _, wt := range wts {
		if wt.Branch == man.Branch {
			return res, proto.Errorf(proto.ErrBadRequest, "branch %s is already checked out at %s", man.Branch, wt.Path)
		}
	}
	if err := gitx.ValidBranchName(ctx, proj.root, man.Branch); err != nil {
		return res, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	head := man.Head
	if man.Bundle {
		incoming := "refs/conch/incoming/" + man.Branch
		defer gitx.DeleteRef(context.Background(), proj.root, incoming)
		if head, err = gitx.FetchBundle(ctx, proj.root, filepath.Join(tmp, packBundle), man.Branch, incoming); err != nil {
			return res, proto.Errorf(proto.ErrBadRequest, "take the branch's commits: %v", err)
		}
	} else if have, _ := gitx.HaveCommits(ctx, proj.root, []string{head}); len(have) == 0 {
		return res, proto.Errorf(proto.ErrBadRequest, "this repository lacks commit %s", head)
	}

	// The branch here: new, behind the moved one, or in the way.
	old, oldErr := gitx.BranchHead(ctx, proj.root, man.Branch)
	switch {
	case oldErr != nil: // not here yet
	case old == head:
	case gitx.IsAncestor(ctx, proj.root, old, head):
	default:
		return res, proto.Errorf(proto.ErrBadRequest, "branch %s has commits there that the moved one doesn't; rename or delete it there first", man.Branch)
	}
	if err := gitx.SetBranch(ctx, proj.root, man.Branch, head); err != nil {
		return res, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	undoBranch := func() {
		c, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		if oldErr != nil {
			_ = gitx.DeleteBranch(c, proj.root, man.Branch)
		} else {
			_ = gitx.SetBranch(c, proj.root, man.Branch, old)
		}
	}
	path, _, perr := s.projects.addWorktree(proj, man.Branch, "")
	if perr != nil {
		undoBranch()
		return res, perr
	}
	fail := func(what string, err error) (proto.WorktreeUnpackResult, *proto.Error) {
		c, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		_ = gitx.ForceRemoveWorktree(c, proj.root, path)
		_ = gitx.PruneWorktrees(c, proj.root)
		undoBranch()
		proj.forgetWorktree(path) // addWorktree noted it before git's next refresh
		s.projects.request(proj)
		return proto.WorktreeUnpackResult{}, proto.Errorf(proto.ErrBadRequest, "%s: %v", what, err)
	}
	for _, patch := range []struct {
		name  string
		index bool
	}{{packStaged, true}, {packUnstaged, false}} {
		data, err := os.ReadFile(filepath.Join(tmp, patch.name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err == nil {
			err = gitx.ApplyPatch(ctx, path, data, patch.index)
		}
		if err != nil {
			return fail("apply the uncommitted changes", err)
		}
	}
	for _, rel := range files {
		if err := placeFile(path, rel, filepath.Join(tmp, packFiles, rel)); err != nil {
			return fail("write "+rel, err)
		}
	}
	s.projects.request(proj)
	log.Printf("project %s: moved worktree %s in at %s", proj.id, man.Branch, path)
	return proto.WorktreeUnpackResult{Path: path, Branch: man.Branch, Files: files}, nil
}

// readPack extracts a pack into dir and returns its manifest and the files
// to place in the worktree (relative paths, already checked).
func readPack(path, dir string) (packManifest, []string, error) {
	var man packManifest
	f, err := os.Open(path)
	if err != nil {
		return man, nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return man, nil, err
	}
	tr := tar.NewReader(zr)
	var files []string
	first := true
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return man, nil, err
		}
		if first {
			first = false
			if hdr.Name != "manifest.json" {
				return man, nil, errors.New("no manifest")
			}
			if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&man); err != nil {
				return man, nil, fmt.Errorf("manifest: %w", err)
			}
			if man.Version != packVersion || man.Branch == "" || man.Head == "" {
				return man, nil, fmt.Errorf("a pack this build can't read (version %d)", man.Version)
			}
			continue
		}
		name := hdr.Name
		switch {
		case name == packBundle || name == packStaged || name == packUnstaged:
			if hdr.Typeflag != tar.TypeReg {
				return man, nil, fmt.Errorf("%s is not a file", name)
			}
		case strings.HasPrefix(name, packFiles):
			rel, ok := cleanRel(strings.TrimPrefix(name, packFiles))
			if !ok {
				return man, nil, fmt.Errorf("unsafe file name %q", name)
			}
			files = append(files, rel)
			name = packFiles + rel
		default:
			continue // something a later build adds
		}
		dst := filepath.Join(dir, name)
		if err := makeParents(dir, name); err != nil {
			return man, nil, err
		}
		switch hdr.Typeflag {
		case tar.TypeSymlink:
			if err := os.Symlink(hdr.Linkname, dst); err != nil {
				return man, nil, err
			}
		case tar.TypeReg:
			out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(hdr.Mode).Perm()|0o600)
			if err != nil {
				return man, nil, err
			}
			_, err = io.Copy(out, tr)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return man, nil, err
			}
			_ = os.Chmod(dst, os.FileMode(hdr.Mode).Perm())
		default:
			return man, nil, fmt.Errorf("%s: unsupported entry", name)
		}
	}
	if first {
		return man, nil, errors.New("empty pack")
	}
	return man, files, nil
}

// cleanRel accepts a relative path that stays inside its folder and keeps
// out of .git.
func cleanRel(rel string) (string, bool) {
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "\\") {
		return "", false
	}
	c := filepath.Clean(rel)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", false
	}
	for _, part := range strings.Split(c, "/") {
		if part == ".git" {
			return "", false
		}
	}
	return c, true
}

// makeParents creates the folders of rel under root, refusing to go
// through anything that isn't a real folder: a symlink there could lead
// outside root.
func makeParents(root, rel string) error {
	dir := root
	for _, part := range strings.Split(filepath.Dir(rel), "/") {
		if part == "." {
			continue
		}
		dir = filepath.Join(dir, part)
		st, err := os.Lstat(dir)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := os.Mkdir(dir, 0o755); err != nil {
				return err
			}
		case err != nil:
			return err
		case !st.IsDir():
			return fmt.Errorf("%s is in the way", strings.TrimPrefix(dir, root+"/"))
		}
	}
	return nil
}

// placeFile moves an extracted file into the worktree at root, replacing
// what is there.
func placeFile(root, rel, src string) error {
	if err := makeParents(root, rel); err != nil {
		return err
	}
	dst := filepath.Join(root, rel)
	if st, err := os.Lstat(dst); err == nil && st.IsDir() {
		return fmt.Errorf("a folder is in the way")
	}
	os.Remove(dst)
	return os.Rename(src, dst)
}

// ---- project.clone ----

func (s *Server) cloneProject(p proto.ProjectCloneParams) (proto.ProjectInfo, *proto.Error) {
	if strings.TrimSpace(p.URL) == "" {
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "project.clone needs a URL")
	}
	path, err := resolvePath(p.Path)
	if err != nil {
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "%s already exists and isn't empty; add it as a project instead", path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cloneTimeout)
	defer cancel()
	if err := gitx.Clone(ctx, p.URL, path); err != nil {
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	proj, err := s.projects.add(path, true)
	if err != nil {
		return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	log.Printf("project %s: cloned %s into %s", proj.id, p.URL, path)
	return proj.snapshot(), nil
}
