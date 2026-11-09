package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// chunked is n whole chunks and a few bytes more, each chunk different, so
// one written in the wrong place shows.
func chunked(n int) []byte {
	var b bytes.Buffer
	for i := range n {
		b.Write(bytes.Repeat([]byte{byte('a' + i%26)}, proto.UploadChunkSize))
	}
	b.WriteString("tail!")
	return b.Bytes()
}

// transferCtx stops a copy that hangs. It is not a speed test: twenty
// chunks as JSON under the race detector took up to 11 s alone, and a
// whole download is one context, so 20 s failed under the full suite.
func transferCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// held answers chunks only once heldBatch of them are waiting, last
// first, so a client that sent one at a time would never hear back.
const heldBatch = 4

// held answers chunks a group at a time: chunks 0–3, 4–7 and so on, each
// group once every chunk in it has been asked for, newest first. Nothing
// is answered until four are on the way at once, which is what the tests
// prove; and the group is by chunk, not by arrival. It was by arrival —
// every fourth request let the held ones go — and a client sends each
// chunk from a goroutine of its own, so two could arrive swapped: the
// oldest was then left among one to three held, the client waited for it
// with all its calls in flight and sent nothing more, and nothing let it
// go (20 s, then "context deadline exceeded", about once in fifty runs).
// The client waits for its oldest chunk with the seven after it asked
// for, so the oldest's group is always complete.
type held struct {
	total  int // chunks to be held, numbered from 0
	groups map[int][]heldMsg
}

type heldMsg struct {
	msg proto.Message
	val any
}

func (h *held) add(conn *proto.Conn, msg proto.Message, v any, chunk int) {
	if h.groups == nil {
		h.groups = map[int][]heldMsg{}
	}
	g := chunk / heldBatch
	h.groups[g] = append(h.groups[g], heldMsg{msg, v})
	if len(h.groups[g]) < min(heldBatch, h.total-g*heldBatch) {
		return
	}
	for i := len(h.groups[g]) - 1; i >= 0; i-- {
		a3Reply(conn, h.groups[g][i].msg, h.groups[g][i].val, nil)
	}
	delete(h.groups, g)
}

func TestUploadSeveralChunksAtOnce(t *testing.T) {
	data := chunked(20)
	middle := 19 // every chunk but the first and the last
	got := make([]byte, len(data))
	h := held{total: 19} // every chunk but the first and the last
	seen, final := 0, false
	s := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
		var p proto.FSUploadParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			t.Error(err)
			return
		}
		copy(got[p.Offset:], p.Data)
		switch {
		case p.Upload == "":
			if !p.Parallel || p.Offset != 0 || p.Final || p.Dir != "~/in" {
				t.Errorf("first chunk: %+v", p)
			}
			a3Reply(conn, msg, proto.FSUploadResult{Upload: "u1"}, nil)
		case p.Final:
			if seen != middle || p.Offset != 20*proto.UploadChunkSize {
				t.Errorf("last chunk at %d after %d of %d", p.Offset, seen, middle)
			}
			final = true
			a3Reply(conn, msg, proto.FSUploadResult{Upload: "u1", Path: "/there/f"}, nil)
		default:
			seen++
			h.add(conn, msg, proto.FSUploadResult{Upload: "u1"}, int(p.Offset/proto.UploadChunkSize)-1)
		}
	})
	s.caps = append(s.caps, proto.CapFSTransfer)
	c := s.dial(t)

	up := NewUpload(c, "f", data).Into("~/in", false)
	defer up.Close()
	last := 0
	for {
		done, err := up.Step(transferCtx(t))
		if err != nil {
			t.Fatal(err)
		}
		if up.Sent() < last || up.Sent() > len(data) {
			t.Fatalf("sent went %d → %d", last, up.Sent())
		}
		last = up.Sent()
		if done {
			break
		}
	}
	if !final || up.Path() != "/there/f" || up.Sent() != len(data) || !bytes.Equal(got, data) {
		t.Fatalf("final %v, path %q, sent %d, same bytes %v", final, up.Path(), up.Sent(), bytes.Equal(got, data))
	}
}

func TestUploadOneAtATimeToAnOlderServer(t *testing.T) {
	for name, caps := range map[string][]string{
		"no fs.transfer.v1": {"fs.upload.v1"},
		"one chunk":         {"fs.upload.v1", proto.CapFSTransfer},
	} {
		t.Run(name, func(t *testing.T) {
			data := chunked(3)
			if name == "one chunk" {
				data = data[:proto.UploadChunkSize]
			}
			var got bytes.Buffer
			s := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
				var p proto.FSUploadParams
				json.Unmarshal(msg.Params, &p)
				if p.Parallel || p.Offset != 0 {
					t.Errorf("sent as parallel: %+v", p)
				}
				got.Write(p.Data) // in order, as an older server takes them
				a3Reply(conn, msg, proto.FSUploadResult{Upload: "u1", Path: "/up/f"}, nil)
			})
			s.caps = caps
			up := NewUpload(s.dial(t), "f", data)
			defer up.Close()
			if path, err := up.Run(transferCtx(t)); err != nil || path != "/up/f" || !bytes.Equal(got.Bytes(), data) {
				t.Fatalf("%q %v, same bytes %v", path, err, bytes.Equal(got.Bytes(), data))
			}
		})
	}
}

func TestUploadChunkRefused(t *testing.T) {
	s := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
		var p proto.FSUploadParams
		json.Unmarshal(msg.Params, &p)
		if p.Offset == 3*proto.UploadChunkSize {
			a3Reply(conn, msg, nil, proto.Errorf(proto.ErrInternal, "disk full"))
			return
		}
		a3Reply(conn, msg, proto.FSUploadResult{Upload: "u1"}, nil)
	})
	s.caps = append(s.caps, proto.CapFSTransfer)
	up := NewUpload(s.dial(t), "f", chunked(12))
	defer up.Close()
	if _, err := up.Run(transferCtx(t)); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("got %v", err)
	}
	if up.Sent() > 3*proto.UploadChunkSize {
		t.Fatalf("counted %d bytes past the refused chunk", up.Sent())
	}
}

// downloadServer serves data as a download, holding reads in batches; the
// last chunk must be asked for after every other one has been answered.
func downloadServer(t *testing.T, data []byte, bad func(off int64, c *proto.FSDownloadChunk)) *a3Server {
	answered := 0
	middle := (len(data) - 1) / proto.UploadChunkSize // chunks before the last
	h := held{total: middle}
	s := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
		if msg.Method == proto.MethodFSDownload {
			a3Reply(conn, msg, proto.FSDownload{ID: "d1", Name: "big.bin", Size: int64(len(data))}, nil)
			return
		}
		var p proto.FSDownloadReadParams
		json.Unmarshal(msg.Params, &p)
		end := min(p.Offset+proto.UploadChunkSize, int64(len(data)))
		c := proto.FSDownloadChunk{Data: data[p.Offset:end], EOF: end == int64(len(data))}
		last := c.EOF
		if bad != nil {
			bad(p.Offset, &c)
		}
		if last {
			if answered != middle {
				t.Errorf("the last chunk was asked for with %d of %d in", answered, middle)
			}
			a3Reply(conn, msg, c, nil)
			return
		}
		answered++
		h.add(conn, msg, c, int(p.Offset/proto.UploadChunkSize))
	})
	s.caps = append(s.caps, proto.CapFSTransfer)
	return s
}

func TestDownloadSeveralChunksAtOnce(t *testing.T) {
	data := chunked(20)
	dest := t.TempDir()
	d := NewDownload(downloadServer(t, data, nil).dial(t), "~/big.bin", dest, false)
	defer d.Close()
	path, err := d.Run(transferCtx(t))
	if err != nil || path != filepath.Join(dest, "big.bin") {
		t.Fatalf("%q %v", path, err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, data) {
		t.Fatal("not the same bytes")
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 1 {
		t.Fatalf("left behind: %v", entries)
	}
}

func TestDownloadBadChunk(t *testing.T) {
	for name, bad := range map[string]func(int64, *proto.FSDownloadChunk){
		"short":       func(off int64, c *proto.FSDownloadChunk) { c.Data = c.Data[:10] },
		"early end":   func(off int64, c *proto.FSDownloadChunk) { c.EOF = off == 2*proto.UploadChunkSize },
		"no end":      func(off int64, c *proto.FSDownloadChunk) { c.EOF = false },
		"long at end": func(off int64, c *proto.FSDownloadChunk) { c.Data = append(c.Data, 'x') },
	} {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			d := NewDownload(downloadServer(t, chunked(5), bad).dial(t), "~/big.bin", dest, false)
			defer d.Close()
			if _, err := d.Run(transferCtx(t)); err == nil {
				t.Fatal("took a bad chunk")
			}
			if entries, _ := os.ReadDir(dest); len(entries) != 0 {
				t.Fatalf("left behind: %v", entries)
			}
		})
	}
}

func TestPipe(t *testing.T) {
	p := newPipe[int]()
	// Answers come back in the order asked, whatever order they finish in.
	gates := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	for i, g := range gates {
		p.send(func(ctx context.Context) (int, error) { <-g; return i, nil })
	}
	close(gates[2])
	close(gates[1])
	close(gates[0])
	for want := range 3 {
		if got, err := p.next(transferCtx(t)); got != want || err != nil {
			t.Fatalf("got %d %v, want %d", got, err, want)
		}
	}
	if p.busy() != 0 {
		t.Fatal("still busy")
	}

	// A step that gives up waiting leaves the call in flight.
	p.send(func(ctx context.Context) (int, error) { <-ctx.Done(); return 0, ctx.Err() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.next(ctx); !errors.Is(err, context.Canceled) || p.busy() != 1 {
		t.Fatalf("next on a cancelled step: %v, busy %d", err, p.busy())
	}

	// stop cancels what is in flight.
	var stopped atomic.Bool
	done := make(chan struct{})
	p.send(func(ctx context.Context) (int, error) {
		<-ctx.Done()
		stopped.Store(true)
		close(done)
		return 0, ctx.Err()
	})
	p.stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop left a call running")
	}
	if !stopped.Load() || p.busy() != 0 {
		t.Fatal("not stopped")
	}
}

func TestPipeCallTimeout(t *testing.T) {
	old := chunkTimeout
	chunkTimeout = 20 * time.Millisecond
	defer func() { chunkTimeout = old }()
	p := newPipe[int]()
	defer p.stop()
	p.send(func(ctx context.Context) (int, error) { <-ctx.Done(); return 0, ctx.Err() })
	if _, err := p.next(transferCtx(t)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

// TestPipeKeepsItsTimeout: a pipe takes the chunk timeout when it is
// made. Its calls used to read the variable whenever their goroutine got
// to run, which raced TestPipeCallTimeout setting it while TestPipe's last
// calls were still starting (seen once on CI's macOS runner).
func TestPipeKeepsItsTimeout(t *testing.T) {
	old := chunkTimeout
	defer func() { chunkTimeout = old }()
	chunkTimeout = 20 * time.Millisecond
	p := newPipe[int]()
	defer p.stop()
	chunkTimeout = time.Hour // set after the pipe was made: not its business
	p.send(func(ctx context.Context) (int, error) { <-ctx.Done(); return 0, ctx.Err() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.next(ctx); !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("the call did not time out on the pipe's own timeout: %v (step %v)", err, ctx.Err())
	}
}

// TestSSHCopyRefusedByAnOlderServer: a server without fs.ssh.v1 ignores
// SSH and would copy on its own machine instead, so nothing is sent.
func TestSSHCopyRefusedByAnOlderServer(t *testing.T) {
	var calls atomic.Int32
	s := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
		calls.Add(1)
		a3Reply(conn, msg, proto.FSUploadResult{Upload: "u1", Path: "/here/f"}, nil)
	})
	s.caps = append(s.caps, proto.CapFSTransfer)
	c := s.dial(t)
	up := NewUpload(c, "f", []byte("x")).Into("~", false).On("p1")
	defer up.Close()
	if _, err := up.Run(transferCtx(t)); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("upload: %v", err)
	}
	d := NewDownload(c, "~/f", t.TempDir(), false).On("p1")
	defer d.Close()
	if _, err := d.Run(transferCtx(t)); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("download: %v", err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d calls reached the server", n)
	}
}
