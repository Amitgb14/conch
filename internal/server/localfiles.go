package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// DefaultLocalFiles are copied from the main checkout into new worktrees:
// files an agent in the worktree would otherwise lack. Only files git
// ignores are copied — tracked ones are already there, and an untracked file
// git doesn't ignore could end up committed on the task's branch (and would
// stop the worktree from being removed).
var DefaultLocalFiles = []string{
	".env", ".env.*", ".envrc",
	".claude/settings.local.json", "CLAUDE.local.md", ".mcp.json",
	"AGENTS.override.md", ".gemini/.env",
}

// maxLocalFile keeps a stray pattern from copying something huge.
const maxLocalFile = 10 << 20

func (p *project) setLocalFiles(patterns []string, reset bool) {
	clean := []string{}
	for _, pat := range patterns {
		if pat = strings.TrimSpace(pat); pat != "" {
			clean = append(clean, pat)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if reset {
		p.info.LocalFiles, p.info.LocalFilesDefault = DefaultLocalFiles, true
		return
	}
	p.info.LocalFiles, p.info.LocalFilesDefault = clean, false
}

func (pm *projectManager) setLocalFiles(fp proto.ProjectFilesParams) (proto.ProjectInfo, *proto.Error) {
	p, perr := pm.get(fp.ProjectID)
	if perr != nil {
		return proto.ProjectInfo{}, perr
	}
	for _, pat := range fp.Patterns {
		if filepath.IsAbs(pat) || strings.HasPrefix(filepath.Clean(pat), "..") {
			return proto.ProjectInfo{}, proto.Errorf(proto.ErrBadRequest, "pattern %q must be relative to the project", pat)
		}
	}
	p.setLocalFiles(fp.Patterns, fp.Reset)
	pm.save()
	info := p.snapshot()
	pm.s.broadcast(proto.EventProjectUpdated, info)
	return info, nil
}

// localFiles lists the ignored files of the main checkout that the
// project's patterns match; with ignored false, the untracked files git
// doesn't ignore, which are never copied.
func (p *project) localFiles(ctx context.Context, ignored bool) ([]string, error) {
	info := p.snapshot()
	if !info.Git {
		return nil, nil
	}
	return gitx.UntrackedFiles(ctx, p.root, info.LocalFiles, ignored)
}

// linkedWorktree checks that path is one of p's worktrees other than the
// main checkout.
func (p *project) linkedWorktree(path string) *proto.Error {
	for _, wt := range p.snapshot().Worktrees {
		if wt.Path == path {
			if wt.Main {
				return proto.Errorf(proto.ErrBadRequest, "%s is the main checkout; local files come from there", path)
			}
			return nil
		}
	}
	return proto.Errorf(proto.ErrNotFound, "no worktree %s in %s", path, p.root)
}

func (pm *projectManager) copyFiles(wp proto.WorktreeFilesParams) (proto.WorktreeFilesResult, *proto.Error) {
	p, perr := pm.get(wp.ProjectID)
	if perr != nil {
		return proto.WorktreeFilesResult{}, perr
	}
	if perr := p.linkedWorktree(wp.Path); perr != nil {
		return proto.WorktreeFilesResult{}, perr
	}
	res, err := pm.copyLocalFiles(p, wp.Path, wp.Overwrite)
	if err != nil {
		return res, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if len(res.Copied) > 0 {
		log.Printf("project %s: copied %s into %s", p.id, strings.Join(res.Copied, ", "), wp.Path)
	}
	return res, nil
}

// copyLocalFiles copies the project's local files from the main checkout
// into the worktree at dst.
func (pm *projectManager) copyLocalFiles(p *project, dst string, overwrite bool) (proto.WorktreeFilesResult, error) {
	var res proto.WorktreeFilesResult
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	files, err := p.localFiles(ctx, true)
	if err != nil {
		return res, err
	}
	for _, rel := range files {
		switch why := copyFile(filepath.Join(p.root, rel), filepath.Join(dst, rel), overwrite); why {
		case "":
			res.Copied = append(res.Copied, rel)
		default:
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s (%s)", rel, why))
		}
	}
	return res, nil
}

// copyFile copies src to dst, keeping its mode (symlinks stay symlinks). It
// returns "" on success, else why it didn't.
func copyFile(src, dst string, overwrite bool) string {
	st, err := os.Lstat(src)
	if err != nil {
		return err.Error()
	}
	if _, err := os.Lstat(dst); err == nil && !overwrite {
		return "exists"
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err.Error()
	}
	if st.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err.Error()
		}
		_ = os.Remove(dst)
		if err := os.Symlink(target, dst); err != nil {
			return err.Error()
		}
		return ""
	}
	if !st.Mode().IsRegular() {
		return "not a regular file"
	}
	if st.Size() > maxLocalFile {
		return "larger than 10 MB"
	}
	in, err := os.Open(src)
	if err != nil {
		return err.Error()
	}
	defer in.Close()
	tmp := dst + ".conch-tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err.Error()
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err.Error()
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err.Error()
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err.Error()
	}
	return ""
}

// localFileStates compares the main checkout's local files with a linked
// worktree's copies.
func (p *project) localFileStates(ctx context.Context, dst string) []proto.LocalFile {
	files, err := p.localFiles(ctx, true)
	if err != nil {
		return nil
	}
	out := make([]proto.LocalFile, 0, len(files))
	for _, rel := range files {
		out = append(out, proto.LocalFile{Path: rel, State: fileState(filepath.Join(p.root, rel), filepath.Join(dst, rel))})
	}
	unignored, _ := p.localFiles(ctx, false)
	for _, rel := range unignored {
		out = append(out, proto.LocalFile{Path: rel, State: proto.FileNotIgnored})
	}
	return out
}

func fileState(src, dst string) string {
	a, err := os.Lstat(src)
	if err != nil {
		return proto.FileMissing
	}
	b, err := os.Lstat(dst)
	if err != nil {
		return proto.FileMissing
	}
	if a.Mode()&os.ModeSymlink != 0 || b.Mode()&os.ModeSymlink != 0 {
		ta, _ := os.Readlink(src)
		tb, _ := os.Readlink(dst)
		if ta == tb && a.Mode().Type() == b.Mode().Type() {
			return proto.FileSame
		}
		return proto.FileDiffers
	}
	if a.Size() != b.Size() {
		return proto.FileDiffers
	}
	if a.Size() > maxLocalFile {
		return proto.FileSame // same size; not worth reading
	}
	ca, err1 := os.ReadFile(src)
	cb, err2 := os.ReadFile(dst)
	if err1 != nil || err2 != nil || !bytes.Equal(ca, cb) {
		return proto.FileDiffers
	}
	return proto.FileSame
}
