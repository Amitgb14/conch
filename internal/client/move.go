package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// Move carries a worktree from one machine's server to another's, a step
// per call so a caller can show progress between them. The servers never
// talk to each other: the pack comes back through this client and goes up
// to the other machine as an upload.
type Move struct {
	From, To  *Client
	ProjectID string // the project on From
	Path      string // the worktree on From
	Target    string // the project on To
	History   []string

	phase  movePhase
	have   string
	pack   proto.WorktreePack
	data   []byte
	up     *Upload
	result proto.WorktreeUnpackResult
}

type movePhase int

const (
	moveHave movePhase = iota
	movePack
	moveRead
	moveUpload
	moveUnpack
	moveDone
)

// NewMove prepares a move of the worktree whose history (newest first, from
// worktree.describe) tells the other machine which commits to skip.
func NewMove(from, to *Client, projectID, path, target string, history []string) *Move {
	return &Move{From: from, To: to, ProjectID: projectID, Path: path, Target: target, History: history}
}

// Timeout is how long the next step may take: packing and unpacking a big
// branch run git for a while.
func (mv *Move) Timeout() time.Duration {
	if mv.phase == movePack || mv.phase == moveUnpack {
		return 5 * time.Minute
	}
	return 30 * time.Second
}

// Step runs the next step and reports whether the worktree has arrived.
func (mv *Move) Step(ctx context.Context) (bool, error) {
	switch mv.phase {
	case moveHave:
		var res proto.WorktreeHaveResult
		if err := mv.To.Call(ctx, proto.MethodWorktreeHave, proto.WorktreeHaveParams{ProjectID: mv.Target, Commits: mv.History}, &res); err != nil {
			return false, fmt.Errorf("compare the history: %w", err)
		}
		if len(res.Have) > 0 {
			mv.have = res.Have[0] // the newest: everything before it is there too
		}
		mv.phase = movePack
	case movePack:
		if err := mv.From.Call(ctx, proto.MethodWorktreePack, proto.WorktreePackParams{ProjectID: mv.ProjectID, Path: mv.Path, Have: mv.have}, &mv.pack); err != nil {
			return false, fmt.Errorf("pack the worktree: %w", err)
		}
		mv.data = make([]byte, 0, mv.pack.Size)
		mv.phase = moveRead
	case moveRead:
		var c proto.WorktreePackChunk
		if err := mv.From.Call(ctx, proto.MethodWorktreePackRead, proto.WorktreePackReadParams{ID: mv.pack.ID, Offset: int64(len(mv.data))}, &c); err != nil {
			return false, fmt.Errorf("read the pack: %w", err)
		}
		if len(c.Data) == 0 && !c.EOF {
			return false, errors.New("read the pack: the server sent nothing")
		}
		mv.data = append(mv.data, c.Data...)
		if int64(len(mv.data)) > mv.pack.Size {
			return false, fmt.Errorf("read the pack: more than the %d bytes announced", mv.pack.Size)
		}
		if c.EOF {
			if int64(len(mv.data)) != mv.pack.Size {
				return false, fmt.Errorf("read the pack: it ended after %d of %d bytes", len(mv.data), mv.pack.Size)
			}
			mv.up = NewUpload(mv.To, mv.pack.Name, mv.data)
			mv.phase = moveUpload
		}
	case moveUpload:
		done, err := mv.up.Step(ctx)
		if err != nil {
			return false, fmt.Errorf("upload the pack: %w", err)
		}
		if done {
			mv.data = nil
			mv.phase = moveUnpack
		}
	case moveUnpack:
		if err := mv.To.Call(ctx, proto.MethodWorktreeUnpack, proto.WorktreeUnpackParams{ProjectID: mv.Target, Pack: mv.up.Path()}, &mv.result); err != nil {
			return false, fmt.Errorf("rebuild the worktree: %w", err)
		}
		mv.phase = moveDone
	}
	return mv.phase == moveDone, nil
}

// Run takes every remaining step, each within its own timeout.
func (mv *Move) Run(ctx context.Context) (proto.WorktreeUnpackResult, error) {
	for {
		sctx, cancel := context.WithTimeout(ctx, mv.Timeout())
		done, err := mv.Step(sctx)
		cancel()
		if err != nil || done {
			return mv.result, err
		}
	}
}

// Result is the new worktree, once the move is done.
func (mv *Move) Result() proto.WorktreeUnpackResult { return mv.result }

// Progress says what the move is doing, for a status line.
func (mv *Move) Progress() string {
	switch mv.phase {
	case moveHave:
		return "comparing history"
	case movePack:
		return "packing"
	case moveRead:
		return fmt.Sprintf("fetching %d%%", percent(int64(len(mv.data)), mv.pack.Size))
	case moveUpload:
		return fmt.Sprintf("sending %d%%", percent(int64(mv.up.Sent()), int64(mv.up.Size())))
	case moveUnpack:
		return "rebuilding the worktree"
	}
	return "done"
}

func percent(n, total int64) int64 {
	if total <= 0 {
		return 100
	}
	return n * 100 / total
}
