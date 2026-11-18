package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ImportRecord is what an import wrote for one conversation, so that
// importing again adds only what is new, and never writes over what the
// person did with the session since.
type ImportRecord struct {
	Agent          string `json:"agent"`
	AgentSessionID string `json:"agentSessionID"`
	Path           string `json:"path"`
	// LastMessageID is the last of the conversation's rows the session
	// holds; Size is the session file's size once conch wrote it, which
	// it is still only if the agent has not written to it since.
	LastMessageID string    `json:"lastMessageID"`
	ImportedAt    time.Time `json:"importedAt"`
	Size          int64     `json:"size"`
	// Dir is the folder it was written to resume in: the one the person
	// chose (-dir) for a conversation whose own folders are gone, which
	// nothing in the other app's database says. Older states have none.
	Dir string `json:"dir,omitempty"`
}

// ImportState maps the other app's conversation id to what was written
// for it: $CONCH_HOME/imports/conductor/state.json for Conductor.
type ImportState map[string]ImportRecord

// LoadImportState reads the state at path; none there is an empty one. A
// file that cannot be read is an error, not an empty state: saved over,
// it would have every conversation imported a second time.
func LoadImportState(path string) (ImportState, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ImportState{}, nil
	}
	if err != nil {
		return nil, err
	}
	st := ImportState{}
	if len(bytes.TrimSpace(b)) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return st, nil
}

// Save writes the state to path whole, through a file beside it.
func (st ImportState) Save(path string) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeWhole(path, append(b, '\n'))
}

// ImportStatus is what became of one conversation.
type ImportStatus string

const (
	ImportWritten   ImportStatus = "written"    // a new session
	ImportAdded     ImportStatus = "added"      // new messages added to the session written before
	ImportUnchanged ImportStatus = "unchanged"  // imported before, nothing new
	ImportCarriedOn ImportStatus = "carried-on" // the session changed since: left alone
	ImportSkipped   ImportStatus = "skipped"    // nothing said in it
	ImportFailed    ImportStatus = "failed"     // not resumable; Err says why
	// ImportFound: the agent kept the conversation itself (Codex's own
	// rollout, written as Conductor ran it), so it resumes with nothing
	// written.
	ImportFound ImportStatus = "found"
)

// ImportOutcome is what ImportResumable did with one conversation.
type ImportOutcome struct {
	Status         ImportStatus
	AgentSessionID string
	Path           string
	Added          int    // messages added, for ImportAdded
	Why            string // for ImportCarriedOn and ImportSkipped
	Err            error  // for ImportFailed
}

// ImportPlan says what ImportResumable would do with im, writing
// nothing: ImportWritten for a conversation not imported before (which
// may yet fail), ImportAdded with the messages it would add, or why it
// would leave it alone. Cheap: no session is read, only stat'ed.
func ImportPlan(im Imported, st ImportState) ImportOutcome {
	if len(im.Messages) == 0 {
		return ImportOutcome{Status: ImportSkipped, Why: "nothing was said in it"}
	}
	rec, seen := st[im.Session.ID]
	if !seen {
		return ImportOutcome{Status: ImportWritten}
	}
	out := ImportOutcome{AgentSessionID: rec.AgentSessionID, Path: rec.Path}
	fi, err := os.Stat(rec.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		out.Status, out.Why = ImportCarriedOn, "its session was removed since it was imported; it is not written again"
		return out
	case err != nil:
		out.Status, out.Err = ImportFailed, err
		return out
	}
	cut := -1
	for i, m := range im.Messages {
		if m.ID == rec.LastMessageID {
			cut = i + 1
		}
	}
	switch {
	case cut == len(im.Messages):
		out.Status = ImportUnchanged
	case fi.Size() != rec.Size:
		out.Status, out.Why = ImportCarriedOn, "carried on in conch since it was imported, so what Conductor has since is left out"
	case cut < 0:
		out.Status, out.Err = ImportFailed, fmt.Errorf("the last message imported (%s) is no longer in the conversation", rec.LastMessageID)
	default:
		out.Status, out.Added = ImportAdded, len(im.Messages)-cut
	}
	return out
}

// ImportResumable makes im a session of its agent through w, or brings
// the one an earlier import wrote up to date, and records it in st:
//
//   - a conversation not imported before is written;
//   - one with new messages gets them only if the session file is as
//     conch left it; one the person carried on with is left alone and
//     said to be;
//   - nothing is ever deleted, nor written again once removed.
//
// A conversation that cannot be written comes back ImportFailed, and st
// is left as it was for it.
func ImportResumable(ctx context.Context, im Imported, w SessionWriter, st ImportState, now time.Time) ImportOutcome {
	out := ImportPlan(im, st)
	last := ""
	if n := len(im.Messages); n > 0 {
		last = im.Messages[n-1].ID
	}
	switch out.Status {
	case ImportWritten:
		id, path, err := w.Write(ctx, im, im.Session.Dir)
		if err != nil {
			return ImportOutcome{Status: ImportFailed, Err: err}
		}
		fi, err := os.Stat(path)
		if err != nil {
			return ImportOutcome{Status: ImportFailed, Err: err}
		}
		st[im.Session.ID] = ImportRecord{Agent: w.Agent(), AgentSessionID: id, Path: path, LastMessageID: last, ImportedAt: now, Size: fi.Size(), Dir: im.Session.Dir}
		return ImportOutcome{Status: ImportWritten, AgentSessionID: id, Path: path}
	case ImportAdded:
	default:
		return out
	}
	rec := st[im.Session.ID]
	ap, ok := w.(SessionAppender)
	if !ok {
		out.Status, out.Err = ImportFailed, fmt.Errorf("conch cannot add to a %s session it wrote", AgentName(w.Agent()))
		return out
	}
	added, err := ap.Append(ctx, im, im.Session.Dir, rec.AgentSessionID, rec.Path, rec.LastMessageID)
	if err != nil {
		out.Status, out.Err = ImportFailed, err
		return out
	}
	fi, err := os.Stat(rec.Path)
	if err != nil {
		out.Status, out.Err = ImportFailed, err
		return out
	}
	rec.LastMessageID, rec.Size, rec.ImportedAt = last, fi.Size(), now
	st[im.Session.ID] = rec
	out.Added = added
	return out
}
