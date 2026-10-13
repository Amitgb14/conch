// Package transfer packs a folder for copying between machines and unpacks
// it at the other end, and picks the name a copy lands under. Both ends use
// it: the server for fs.upload and fs.download, the client for the same
// from this side.
//
// An archive is gzipped tar with paths relative to the folder packed — no
// top folder of its own, since the receiving end decides what the copy is
// called. Unpacking takes the archive as untrusted: a sandbox is somebody
// else's machine, and what comes from it must not write outside the folder
// it was asked into.
package transfer

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Pack writes the folder src to w as a gzipped tar: folders, files and
// symlinks (as links, never followed). Sockets, devices and the like are
// left out.
func Pack(w io.Writer, src string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", src)
	}
	zw, _ := gzip.NewWriterLevel(w, gzip.BestSpeed) // the ssh link compresses too
	tw := tar.NewWriter(zw)
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr := &tar.Header{Name: filepath.ToSlash(rel), Mode: int64(info.Mode().Perm()), ModTime: info.ModTime()}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			hdr.Typeflag, hdr.Linkname = tar.TypeSymlink, target
			return tw.WriteHeader(hdr)
		case d.IsDir():
			hdr.Typeflag, hdr.Name = tar.TypeDir, hdr.Name+"/"
			return tw.WriteHeader(hdr)
		case !info.Mode().IsRegular():
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		hdr.Typeflag, hdr.Size = tar.TypeReg, info.Size()
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		// CopyN: a file that grows while it is read must not overrun the
		// size its header already gave.
		if _, err := io.CopyN(tw, f, info.Size()); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

// Unpack extracts an archive Pack wrote into dest, which must exist. With
// replace, files already there are written over (folders are merged);
// without it, a name that is taken is an error. Nothing is written outside
// dest: a path that leaves it is refused, and so is going through a
// symlink, since one could lead anywhere.
func Unpack(r io.Reader, dest string, replace bool) error {
	st, err := os.Lstat(dest)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", dest)
	}
	zr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("not an archive: %w", err)
	}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, ok := localPath(hdr.Name)
		if !ok {
			return fmt.Errorf("unsafe name in the archive: %q", hdr.Name)
		}
		if err := parents(dest, rel); err != nil {
			return err
		}
		dst := filepath.Join(dest, rel)
		mode := os.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			err = folder(dst, mode)
		case tar.TypeReg:
			err = file(dst, tr, mode, replace)
		case tar.TypeSymlink:
			err = link(dst, hdr.Linkname, replace)
		default:
			continue // hard links, devices: Pack never writes them
		}
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
	}
}

// localPath turns an archive's slash-separated name into a path that stays
// inside the folder it is unpacked into.
func localPath(name string) (string, bool) {
	name = strings.TrimSuffix(name, "/")
	if name == "" || strings.Contains(name, "\\") {
		return "", false
	}
	local := filepath.FromSlash(name)
	if !filepath.IsLocal(local) {
		return "", false
	}
	return filepath.Clean(local), true
}

// parents creates the folders above rel under root, refusing anything in
// the way that isn't a real folder.
func parents(root, rel string) error {
	dir := root
	for _, part := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
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
			return fmt.Errorf("%s is in the way", strings.TrimPrefix(dir, root+string(filepath.Separator)))
		}
	}
	return nil
}

func folder(dst string, mode os.FileMode) error {
	st, err := os.Lstat(dst)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return os.Mkdir(dst, mode|0o700)
	case err != nil:
		return err
	case !st.IsDir():
		return errors.New("something that is not a folder is in the way")
	}
	return nil
}

// file writes beside dst and renames it into place, so a replaced file is
// never half written and a symlink at dst is replaced, not followed.
func file(dst string, r io.Reader, mode os.FileMode, replace bool) error {
	st, err := os.Lstat(dst)
	switch {
	case err == nil && st.IsDir():
		return errors.New("a folder is in the way")
	case err == nil && !replace:
		return os.ErrExist
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".part-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

func link(dst, target string, replace bool) error {
	st, err := os.Lstat(dst)
	switch {
	case err == nil && st.IsDir():
		return errors.New("a folder is in the way")
	case err == nil && !replace:
		return os.ErrExist
	case err == nil:
		if err := os.Remove(dst); err != nil {
			return err
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	return os.Symlink(target, dst)
}

// SafeName makes a name from the other end safe to create here: its last
// path element, without control characters, and never empty, "." or "..".
func SafeName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name[strings.LastIndexAny(name, `/\`)+1:])
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}

// FreeName is name in dir, or when that is taken the first of "name 2",
// "name 3", … that is not, keeping the extension: a copy never lands on
// something already there unless asked to.
func FreeName(dir, name string) string {
	// Anything Lstat can't see is free as far as a name goes; the write
	// itself says why if it is not.
	if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
		return name
	}
	ext := filepath.Ext(name)
	if ext == name || len(ext) > 16 { // ".bashrc" has no extension to keep
		ext = ""
	}
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s %d%s", stem, i, ext)
		if _, err := os.Lstat(filepath.Join(dir, cand)); err != nil {
			return cand
		}
	}
}
