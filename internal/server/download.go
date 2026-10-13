package server

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/transfer"
)

// Copying a file or folder off this machine (fs.download), for `conch
// download` and the Files view. A file is read where it is; a folder is
// packed first. Either way the client reads it back a chunk at a time
// through the pack store move.go keeps, which abandons what nobody reads.

// transferLimit bounds a copy either way that the person asked for by
// name: a folder packed for download, or a file or folder uploaded to a
// folder of their choosing. It is far above maxUploadSize, which is for
// what is dropped on a pane; tests lower it.
var transferLimit int64 = 4 << 30

func (s *Server) download(p proto.FSDownloadParams) (proto.FSDownload, *proto.Error) {
	if p.Path == "" {
		return proto.FSDownload{}, proto.Errorf(proto.ErrBadRequest, "fs.download needs a path")
	}
	path, err := resolvePath(p.Path)
	if err != nil {
		return proto.FSDownload{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		return proto.FSDownload{}, statError(path, err)
	}
	name := filepath.Base(path)
	if name == string(filepath.Separator) {
		name = "root"
	}
	id := randomHex(8)
	pf := &packFile{path: path, size: st.Size(), last: s.packs.now(), keep: true}
	switch {
	case st.IsDir():
		if err := os.MkdirAll(s.packs.dir, 0o700); err != nil {
			return proto.FSDownload{}, proto.Errorf(proto.ErrInternal, "%v", err)
		}
		pf.path, pf.keep = filepath.Join(s.packs.dir, id+".tar.gz"), false
		if pf.size, err = packFolder(pf.path, path); err != nil {
			os.Remove(pf.path)
			return proto.FSDownload{}, proto.Errorf(proto.ErrBadRequest, "pack %s: %v", path, err)
		}
	case !st.Mode().IsRegular():
		// A fifo would block the read, and a device has no end.
		return proto.FSDownload{}, proto.Errorf(proto.ErrBadRequest, "%s is not a file or folder", path)
	}
	s.packs.mu.Lock()
	s.packs.active[id] = pf
	s.packs.mu.Unlock()
	log.Printf("download: %s (%s)", path, sizeText(pf.size))
	return proto.FSDownload{ID: id, Name: name, Size: pf.size, Dir: st.IsDir()}, nil
}

// packFolder packs src into a new file at dst and returns its size.
func packFolder(dst, src string) (int64, error) {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	w := &limitWriter{w: f, left: transferLimit}
	if err := transfer.Pack(w, src); err != nil {
		if errors.Is(err, errTooBig) {
			return 0, fmt.Errorf("it packs to more than %s", sizeText(transferLimit))
		}
		return 0, err
	}
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), f.Close()
}

var errTooBig = errors.New("too big")

// limitWriter stops a pack that would pass the limit as soon as it does,
// rather than after filling the disk.
type limitWriter struct {
	w    *os.File
	left int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, errTooBig
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}

// statError says plainly that a path named for a copy is not there, rather
// than passing on stat's own words.
func statError(path string, err error) *proto.Error {
	if errors.Is(err, fs.ErrNotExist) {
		return proto.Errorf(proto.ErrBadRequest, "%s doesn't exist", path)
	}
	return proto.Errorf(proto.ErrBadRequest, "%v", err)
}
