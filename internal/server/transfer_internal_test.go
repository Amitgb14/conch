package server

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// An upload into a folder of the person's that is abandoned takes its
// partial file with it — and never the folder, even one left empty.
func TestTransferAbandonedKeepsTheirFolder(t *testing.T) {
	now := time.Now()
	u := newUploads(t.TempDir())
	u.now = func() time.Time { return now }
	theirs := t.TempDir() // empty but for what the upload puts there
	owner := &client{}
	res, perr := u.chunk(owner, proto.FSUploadParams{Name: "big.iso", Size: 10, Data: []byte("1"), Dir: theirs})
	if perr != nil {
		t.Fatal(perr)
	}
	if entries, _ := os.ReadDir(theirs); len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), ".big.iso.part-") {
		t.Fatalf("partial file: %v", entries)
	}
	now = now.Add(uploadIdle)
	u.sweep()
	if u.active[res.Upload] != nil {
		t.Fatal("not abandoned")
	}
	entries, err := os.ReadDir(theirs)
	if err != nil {
		t.Fatalf("their folder is gone: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("left %v", entries)
	}
	// The same when the connection closes mid-way.
	u.chunk(owner, proto.FSUploadParams{Name: "big.iso", Size: 10, Data: []byte("1"), Dir: theirs})
	u.dropClient(owner)
	if entries, err := os.ReadDir(theirs); err != nil || len(entries) != 0 {
		t.Fatalf("after close: %v %v", entries, err)
	}
	// A chunk past the size announced ends it the same way.
	res, _ = u.chunk(owner, proto.FSUploadParams{Name: "big.iso", Size: 1, Data: []byte("1"), Dir: theirs})
	if _, perr := u.chunk(owner, proto.FSUploadParams{Upload: res.Upload, Data: []byte("2"), Final: true}); perr == nil {
		t.Fatal("took more than announced")
	}
	if entries, err := os.ReadDir(theirs); err != nil || len(entries) != 0 {
		t.Fatalf("after overrun: %v %v", entries, err)
	}
}

func TestTransferLimits(t *testing.T) {
	old := transferLimit
	transferLimit = 1 << 10
	t.Cleanup(func() { transferLimit = old })
	u := newUploads(t.TempDir())
	owner := &client{}
	dir := t.TempDir()

	// Into a folder, the transfer limit applies rather than a drop's …
	if _, perr := u.chunk(owner, proto.FSUploadParams{Name: "a", Size: transferLimit + 1, Dir: dir}); perr == nil || !strings.Contains(perr.Message, "limit") {
		t.Fatalf("over the limit: %v", perr)
	}
	if _, perr := u.chunk(owner, proto.FSUploadParams{Name: "a", Size: transferLimit, Data: make([]byte, transferLimit), Final: true, Dir: dir}); perr != nil {
		t.Fatalf("at the limit: %v", perr)
	}
	// … and to a packed folder, wherever it goes.
	if _, perr := u.chunk(owner, proto.FSUploadParams{Name: "a", Size: transferLimit + 1, Archive: true}); perr == nil {
		t.Fatal("archive over the limit")
	}
	// A folder that packs to more than the limit is not offered for
	// download, and leaves no pack behind.
	s := &Server{packs: newPacks(t.TempDir())}
	home := t.TempDir()
	t.Setenv("HOME", home)
	big := filepath.Join(home, "big")
	os.Mkdir(big, 0o755)
	noise := make([]byte, 64<<10)
	rand.Read(noise) // gzip cannot shrink it under the limit
	os.WriteFile(filepath.Join(big, "noise"), noise, 0o644)
	if _, perr := s.download(proto.FSDownloadParams{Path: "~/big"}); perr == nil || !strings.Contains(perr.Message, "packs to more than") {
		t.Fatalf("over the limit: %v", perr)
	}
	if entries, _ := os.ReadDir(s.packs.dir); len(entries) != 0 {
		t.Fatalf("left %v", entries)
	}
	// A file is read where it is, so the limit is not about it.
	if _, perr := s.download(proto.FSDownloadParams{Path: "~/big/noise"}); perr != nil {
		t.Fatalf("file: %v", perr)
	}
}
