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
	defer u.mu.Unlock()
	id := p.Upload
	up := u.active[id]
	if id == "" {
		var perr *proto.Error
		if id, up, perr = u.start(c, p); perr != nil {
			return proto.FSUploadResult{}, perr
		}
	} else if up == nil || up.owner != c {
		return proto.FSUploadResult{}, proto.Errorf(proto.ErrNotFound, "no upload %q (it may have been abandoned)", id)
	}
	up.last = u.now()

	if up.written+int64(len(p.Data)) > up.size {
		u.abort(id)
		return proto.FSUploadResult{}, proto.Errorf(proto.ErrBadRequest, "upload is larger than the %d bytes announced", up.size)
	}
	if _, err := up.f.Write(p.Data); err != nil {
		u.abort(id)
		return proto.FSUploadResult{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	up.written += int64(len(p.Data))
	if !p.Final {
		return proto.FSUploadResult{Upload: id}, nil
	}

	if up.written != up.size {
		u.abort(id)
		return proto.FSUploadResult{}, proto.Errorf(proto.ErrBadRequest, "upload ended after %d of %d bytes", up.written, up.size)
	}
	delete(u.active, id)
	path := strings.TrimSuffix(up.part, ".part")
	err := up.f.Close()
	if err == nil {
		err = os.Rename(up.part, path)
	}
	if err != nil {
		os.Remove(up.part)
		return proto.FSUploadResult{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	return proto.FSUploadResult{Upload: id, Path: path}, nil
}

// start opens the file for a new upload. u.mu is held.
func (u *uploads) start(c *client, p proto.FSUploadParams) (string, *upload, *proto.Error) {
	if p.Size < 0 || p.Size > maxUploadSize {
		return "", nil, proto.Errorf(proto.ErrBadRequest, "%s is %s; the limit is %s", uploadName(p.Name), sizeText(p.Size), sizeText(maxUploadSize))
	}
	dir := filepath.Join(u.dir, u.now().Format("2006-01-02"), randomHex(4))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	// MkdirAll leaves existing folders alone; make sure uploads aren't readable by others.
	for d := dir; d != filepath.Dir(u.dir); d = filepath.Dir(d) {
		_ = os.Chmod(d, 0o700)
	}
	part := filepath.Join(dir, uploadName(p.Name)+".part")
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", nil, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	id := randomHex(8)
	up := &upload{owner: c, f: f, part: part, size: p.Size}
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
	os.Remove(filepath.Dir(up.part)) // the random folder, now empty
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
