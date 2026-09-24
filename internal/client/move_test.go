package client

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// moveFake is both ends of a move: a source that packs size bytes and a
// target that takes the upload. fail makes one method answer with an error;
// chunk replaces the source's pack_read answer.
type moveFake struct {
	mu       sync.Mutex
	size     int
	fail     string
	chunk    func(off int64) proto.WorktreePackChunk
	packHave string
	uploaded bytes.Buffer
	unpacked string
}

func (f *moveFake) data() []byte {
	b := make([]byte, f.size)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func (f *moveFake) handle(conn *proto.Conn, msg proto.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if msg.Method == f.fail {
		a3Reply(conn, msg, nil, &proto.Error{Code: proto.ErrBadRequest, Message: msg.Method + " failed"})
		return
	}
	switch msg.Method {
	case proto.MethodWorktreeHave:
		var p proto.WorktreeHaveParams
		json.Unmarshal(msg.Params, &p)
		have := []string{}
		for _, c := range p.Commits {
			if strings.HasPrefix(c, "old") {
				have = append(have, c)
			}
		}
		a3Reply(conn, msg, proto.WorktreeHaveResult{Have: have}, nil)
	case proto.MethodWorktreePack:
		var p proto.WorktreePackParams
		json.Unmarshal(msg.Params, &p)
		f.packHave = p.Have
		a3Reply(conn, msg, proto.WorktreePack{ID: "pk1", Name: "feat.tar.gz", Size: int64(f.size)}, nil)
	case proto.MethodWorktreePackRead:
		var p proto.WorktreePackReadParams
		json.Unmarshal(msg.Params, &p)
		if f.chunk != nil {
			a3Reply(conn, msg, f.chunk(p.Offset), nil)
			return
		}
		end := min(int(p.Offset)+proto.UploadChunkSize, f.size)
		a3Reply(conn, msg, proto.WorktreePackChunk{Data: f.data()[p.Offset:end], EOF: end == f.size}, nil)
	case proto.MethodFSUpload:
		var p proto.FSUploadParams
		json.Unmarshal(msg.Params, &p)
		f.uploaded.Write(p.Data)
		res := proto.FSUploadResult{Upload: "u1"}
		if p.Final {
			res.Path = "/up/feat.tar.gz"
		}
		a3Reply(conn, msg, res, nil)
	case proto.MethodWorktreeUnpack:
		var p proto.WorktreeUnpackParams
		json.Unmarshal(msg.Params, &p)
		f.unpacked = p.Pack
		a3Reply(conn, msg, proto.WorktreeUnpackResult{Path: "/there/feat", Branch: "feat"}, nil)
	default:
		a3Reply(conn, msg, nil, &proto.Error{Code: proto.ErrBadRequest, Message: "unexpected " + msg.Method})
	}
}

func newMoveFake(t *testing.T, size int) (*moveFake, *Client, *Client) {
	t.Helper()
	f := &moveFake{size: size}
	from := a3NewServer(t, f.handle).dial(t)
	to := a3NewServer(t, f.handle).dial(t)
	return f, from, to
}

func TestMoveSteps(t *testing.T) {
	f, from, to := newMoveFake(t, proto.UploadChunkSize*3/2)
	mv := NewMove(from, to, "r1", "/here/feat", "r5", []string{"new2", "new1", "old2", "old1"})
	ctx := context.Background()
	var progress []string
	for {
		progress = append(progress, mv.Progress())
		if mv.Timeout() <= 0 {
			t.Fatal("no timeout")
		}
		done, err := mv.Step(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	progress = append(progress, mv.Progress())
	want := "comparing history|packing|fetching 0%|fetching 66%|sending 0%|sending 66%|rebuilding the worktree|done"
	if got := strings.Join(progress, "|"); got != want {
		t.Fatalf("progress:\n%s\nwant\n%s", got, want)
	}
	// The newest commit the target has is what the pack skips.
	if f.packHave != "old2" || !bytes.Equal(f.uploaded.Bytes(), f.data()) || f.unpacked != "/up/feat.tar.gz" {
		t.Fatalf("have %q, uploaded %d bytes, unpacked %q", f.packHave, f.uploaded.Len(), f.unpacked)
	}
	if r := mv.Result(); r.Path != "/there/feat" || r.Branch != "feat" {
		t.Fatalf("result: %+v", r)
	}
	// Stepping a finished move does nothing more.
	if done, err := mv.Step(ctx); !done || err != nil {
		t.Fatalf("after done: %v %v", done, err)
	}
}

func TestMoveTimeouts(t *testing.T) {
	mv := &Move{}
	for phase, want := range map[movePhase]time.Duration{moveHave: 30 * time.Second, movePack: 5 * time.Minute, moveRead: 30 * time.Second,
		moveUpload: 30 * time.Second, moveUnpack: 5 * time.Minute} {
		mv.phase = phase
		if got := mv.Timeout(); got != want {
			t.Errorf("phase %d: %v", phase, got)
		}
	}
}

func TestMoveRunNothingInCommon(t *testing.T) {
	f, from, to := newMoveFake(t, 10)
	res, err := NewMove(from, to, "r1", "/here/feat", "r5", []string{"new1"}).Run(context.Background())
	if err != nil || res.Branch != "feat" || f.packHave != "" {
		t.Fatalf("run: %+v %v (have %q)", res, err, f.packHave)
	}
	// An empty pack (nothing to send) still goes up as one final chunk.
	f, from, to = newMoveFake(t, 0)
	if _, err := NewMove(from, to, "r1", "/p", "r5", nil).Run(context.Background()); err != nil || f.unpacked == "" {
		t.Fatalf("empty pack: %v", err)
	}
}

func TestMoveErrors(t *testing.T) {
	for method, want := range map[string]string{
		proto.MethodWorktreeHave:     "compare the history",
		proto.MethodWorktreePack:     "pack the worktree",
		proto.MethodWorktreePackRead: "read the pack",
		proto.MethodFSUpload:         "upload the pack",
		proto.MethodWorktreeUnpack:   "rebuild the worktree",
	} {
		f, from, to := newMoveFake(t, 100)
		f.fail = method
		_, err := NewMove(from, to, "r1", "/p", "r5", []string{"old1"}).Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), method+" failed") {
			t.Errorf("%s: %v", method, err)
		}
	}

	for name, c := range map[string]struct {
		chunk func(off int64) proto.WorktreePackChunk
		want  string
	}{
		"nothing sent":   {func(int64) proto.WorktreePackChunk { return proto.WorktreePackChunk{} }, "sent nothing"},
		"ends early":     {func(int64) proto.WorktreePackChunk { return proto.WorktreePackChunk{Data: []byte("abc"), EOF: true} }, "ended after 3 of 100"},
		"more than said": {func(int64) proto.WorktreePackChunk { return proto.WorktreePackChunk{Data: make([]byte, 101)} }, "more than the 100 bytes"},
	} {
		f, from, to := newMoveFake(t, 100)
		f.chunk = c.chunk
		if _, err := NewMove(from, to, "r1", "/p", "r5", nil).Run(context.Background()); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// A cancelled context stops the move.
	_, from, to := newMoveFake(t, 100)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewMove(from, to, "r1", "/p", "r5", nil).Run(ctx); err == nil {
		t.Fatal("cancelled: no error")
	}
}
