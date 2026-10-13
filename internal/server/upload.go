package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/transfer"
)

// Files dropped on a remote pane in the TUI are uploaded here, one folder
// per day and a random folder per file, so the file keeps its own name
// (agents show it) without colliding with another of the same name.
const (
	// maxUploadSize bounds one file whatever the client allows: uploads
	// travel as base64 over the ssh bridge.
	maxUploadSize int64 = 256 << 20
	uploadIdle          = 2 * time.Minute    // an upload with no chunk for this long is abandoned
	uploadKeep          = 7 * 24 * time.Hour // day folders older than this are deleted
	uploadNameMax       = 200                // bytes
)

type uploads struct {
	dir string
	now func() time.Time

	mu     sync.Mutex
	active map[string]*upload
}

type upload struct {
	owner   *client
	f       *os.File
	part    string
	size    int64
	written int64
	last    time.Time

	// Where it goes (fs.transfer.v1). dir is a folder the person chose,
	// or "" for the uploads folder; own is the uploads folder's random
	// folder this upload made, removed if it is abandoned — never a
	// folder of theirs.
	name    string
	dir     string
	own     string
	archive bool
	replace bool
	mode    os.FileMode
	// parallel uploads say where each chunk goes; got is the chunks
	// already written, by offset, so one sent twice is not counted twice.
	parallel bool
	got      map[int64]bool
}

func newUploads(configDir string) *uploads {
	dir := filepath.Join(configDir, "uploads")
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs // the path is pasted into panes running elsewhere
	}
	return &uploads{dir: dir, now: time.Now, active: map[string]*upload{}}
}

// chunk stores one fs.upload request from c.
func (u *uploads) chunk(c *client, p proto.FSUploadParams) (proto.FSUploadResult, *proto.Error) {
	u.mu.Lock()
	id := p.Upload
	up := u.active[id]
	if id == "" {
		var perr *proto.Error
		if id, up, perr = u.start(c, p); perr != nil {
			u.mu.Unlock()
			return proto.FSUploadResult{}, perr
		}
	} else if up == nil || up.owner != c {
		u.mu.Unlock()
		return proto.FSUploadResult{}, proto.Errorf(proto.ErrNotFound, "no upload %q (it may have been abandoned)", id)
	}
	up.last = u.now()

	fail := func(code string, format string, args ...any) (proto.FSUploadResult, *proto.Error) {
		u.abort(id)
		u.mu.Unlock()
		return proto.FSUploadResult{}, proto.Errorf(code, format, args...)
	}
	if up.parallel {
		if perr := up.place(p); perr != nil {
			u.abort(id)
			u.mu.Unlock()
			return proto.FSUploadResult{}, perr
		}
	} else {
		if up.written+int64(len(p.Data)) > up.size {
			return fail(proto.ErrBadRequest, "upload is larger than the %d bytes announced", up.size)
		}
		if _, err := up.f.Write(p.Data); err != nil {
			return fail(proto.ErrInternal, "%v", err)
		}
		up.written += int64(len(p.Data))
	}
	if !p.Final {
		u.mu.Unlock()
		return proto.FSUploadResult{Upload: id}, nil
	}
	if up.written != up.size {
		return fail(proto.ErrBadRequest, "upload ended after %d of %d bytes", up.written, up.size)
	}
	delete(u.active, id)
	u.mu.Unlock()
	// Placing it can take a while — a folder is unpacked — and is this
	// upload's alone, so the others go on meanwhile.
	path, err := up.finish()
	if err != nil {
		os.Remove(up.part)
		if up.own != "" {
			os.Remove(up.own) // the random folder, now empty
		}
		return proto.FSUploadResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	return proto.FSUploadResult{Upload: id, Path: path}, nil
}

// place writes one chunk of a parallel upload where it says it goes. Only
// whole chunks at their own offsets are taken, each once, so the bytes
// written reach the size only when every one is there.
func (up *upload) place(p proto.FSUploadParams) *proto.Error {
	off, n := p.Offset, int64(len(p.Data))
	switch {
	case off < 0 || off%proto.UploadChunkSize != 0 || (off >= up.size && up.size > 0):
		return proto.Errorf(proto.ErrBadRequest, "no chunk of the upload starts at %d", off)
	case n != min(proto.UploadChunkSize, up.size-off):
		return proto.Errorf(proto.ErrBadRequest, "the chunk at %d is %d bytes, not %d", off, n, min(proto.UploadChunkSize, up.size-off))
	case up.got[off]:
		return proto.Errorf(proto.ErrBadRequest, "the chunk at %d came twice", off)
	case p.Final != (off+n == up.size):
		return proto.Errorf(proto.ErrBadRequest, "only the last chunk ends the upload")
	}
	if _, err := up.f.WriteAt(p.Data, off); err != nil {
		return proto.Errorf(proto.ErrInternal, "%v", err)
	}
	up.got[off] = true
	up.written += n
	return nil
}

// finish puts a complete upload where it was meant to go and returns its
// path there.
func (up *upload) finish() (string, error) {
	if err := up.f.Close(); err != nil {
		return "", err
	}
	if !up.archive && up.dir == "" {
		path := strings.TrimSuffix(up.part, ".part")
		return path, os.Rename(up.part, path)
	}
	base := up.dir
	if base == "" {
		base = up.own
	}
	name := up.name
	if !up.replace {
		name = transfer.FreeName(base, name)
	}
	target := filepath.Join(base, name)
	st, err := os.Lstat(target)
	exists := err == nil
	if !up.archive {
		if exists && st.IsDir() {
			return "", fmt.Errorf("%s is a folder", target)
		}
		if err := os.Chmod(up.part, up.mode); err != nil {
			return "", err
		}
		return target, os.Rename(up.part, target)
	}
	switch {
	case exists && !st.IsDir():
		return "", fmt.Errorf("%s is in the way and is not a folder", target)
	case !exists:
		if err := os.Mkdir(target, 0o755); err != nil {
			return "", err
		}
	}
	f, err := os.Open(up.part)
	if err != nil {
		return "", err
	}
	err = transfer.Unpack(f, target, up.replace)
	f.Close()
	if err != nil {
		if !exists {
			os.RemoveAll(target) // half a folder is worse than none
		}
		return "", err
	}
	os.Remove(up.part)
	if up.dir != "" && up.own != "" {
		os.Remove(up.own) // the pack's random folder; the copy is elsewhere
	}
	return target, nil
}

// start opens the file for a new upload. u.mu is held.
func (u *uploads) start(c *client, p proto.FSUploadParams) (string, *upload, *proto.Error) {
	name := uploadName(p.Name)
	limit := maxUploadSize
	if p.Dir != "" || p.Archive {
		limit = transferLimit // asked for by name, not dropped on a pane
	}
	if p.Size < 0 || p.Size > limit {
		return "", nil, proto.Errorf(proto.ErrBadRequest, "%s is %s; the limit is %s", name, sizeText(p.Size), sizeText(limit))
	}
	up := &upload{owner: c, size: p.Size, name: name, archive: p.Archive, replace: p.Replace, mode: 0o644}
	if p.Parallel {
		up.parallel, up.got = true, map[int64]bool{}
	}
	if p.Mode != 0 {
		up.mode = os.FileMode(p.Mode).Perm()
	}
	if p.Dir != "" {
		dir, err := resolvePath(p.Dir)
		if err != nil {
			return "", nil, proto.Errorf(proto.ErrInternal, "%v", err)
		}
		if st, err := os.Stat(dir); err != nil {
			return "", nil, statError(dir, err)
		} else if !st.IsDir() {
			return "", nil, proto.Errorf(proto.ErrBadRequest, "%s is not a folder", dir)
		}
		up.dir = dir
	}
	if up.dir != "" && !up.archive {
		// Beside where it goes, so putting it there is a rename on the
		// same filesystem and a half-sent file never has its name.
		f, err := os.CreateTemp(up.dir, "."+name+".part-*")
		if err != nil {
			return "", nil, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		up.f, up.part = f, f.Name()
	} else {
		dir := filepath.Join(u.dir, u.now().Format("2006-01-02"), randomHex(4))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", nil, proto.Errorf(proto.ErrInternal, "%v", err)
		}
		// MkdirAll leaves existing folders alone; make sure uploads aren't readable by others.
		for d := dir; d != filepath.Dir(u.dir); d = filepath.Dir(d) {
			_ = os.Chmod(d, 0o700)
		}
		part := filepath.Join(dir, name+".part")
		f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return "", nil, proto.Errorf(proto.ErrInternal, "%v", err)
		}
		up.f, up.part, up.own = f, part, dir
	}
	id := randomHex(8)
	u.active[id] = up
	return id, up, nil
}

// abort deletes an upload's partial file. u.mu is held.
func (u *uploads) abort(id string) {
	up := u.active[id]
	if up == nil {
		return
	}
	delete(u.active, id)
	up.f.Close()
	os.Remove(up.part)
	if up.own != "" {
		os.Remove(up.own) // the random folder, now empty
	}
}

// dropClient abandons the uploads of a closed connection.
func (u *uploads) dropClient(c *client) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for id, up := range u.active {
		if up.owner == c {
			u.abort(id)
		}
	}
}

// sweep abandons uploads that stopped sending chunks.
func (u *uploads) sweep() {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := u.now()
	for id, up := range u.active {
		if now.Sub(up.last) >= uploadIdle {
			u.abort(id)
		}
	}
}

// prune deletes day folders older than uploadKeep, with any leftovers of
// uploads a reload or crash interrupted.
func (u *uploads) prune() {
	entries, err := os.ReadDir(u.dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("uploads: %v", err)
		}
		return
	}
	cutoff := u.now().Add(-uploadKeep)
	for _, e := range entries {
		day, err := time.ParseInLocation("2006-01-02", e.Name(), time.Local)
		if err != nil || !e.IsDir() || !day.AddDate(0, 0, 1).Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(u.dir, e.Name())); err != nil {
			log.Printf("uploads: %v", err)
		}
	}
}

// run sweeps abandoned uploads and prunes old ones until quit closes.
func (u *uploads) run(quit <-chan struct{}) {
	u.prune()
	sweep := time.NewTicker(uploadIdle / 4)
	defer sweep.Stop()
	daily := time.NewTicker(24 * time.Hour)
	defer daily.Stop()
	for {
		select {
		case <-sweep.C:
			u.sweep()
		case <-daily.C:
			u.prune()
		case <-quit:
			return
		}
	}
}

// uploadName makes a client's file name safe to create: a base name only,
// without control characters, never "." or "..", and at most uploadNameMax
// bytes with its extension kept.
func uploadName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name[strings.LastIndex(name, "/")+1:])
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	if len(name) <= uploadNameMax {
		return name
	}
	ext := filepath.Ext(name)
	if len(ext) > 16 {
		ext = ""
	}
	stem := name[:uploadNameMax-len(ext)]
	for !utf8.ValidString(stem) { // don't cut a character in half
		stem = stem[:len(stem)-1]
	}
	return stem + ext
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
