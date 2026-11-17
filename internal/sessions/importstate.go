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
	if len(im.Messages) == 0 {
		return ImportOutcome{Status: ImportSkipped, Why: "nothing was said in it"}
	}
	last := im.Messages[len(im.Messages)-1].ID
	rec, seen := st[im.Session.ID]
	if !seen {
		id, path, err := w.Write(ctx, im, im.Session.Dir)
		if err != nil {
			return ImportOutcome{Status: ImportFailed, Err: err}
		}
		fi, err := os.Stat(path)
		if err != nil {
			return ImportOutcome{Status: ImportFailed, Err: err}
		}
		st[im.Session.ID] = ImportRecord{Agent: w.Agent(), AgentSessionID: id, Path: path, LastMessageID: last, ImportedAt: now, Size: fi.Size()}
		return ImportOutcome{Status: ImportWritten, AgentSessionID: id, Path: path}
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
	if rec.LastMessageID == last {
		out.Status = ImportUnchanged
		return out
	}
	if fi.Size() != rec.Size {
		out.Status, out.Why = ImportCarriedOn, "carried on in conch since it was imported, so what Conductor has since is left out"
		return out
	}
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
	if fi, err = os.Stat(rec.Path); err != nil {
		out.Status, out.Err = ImportFailed, err
		return out
	}
	rec.LastMessageID, rec.Size, rec.ImportedAt = last, fi.Size(), now
	st[im.Session.ID] = rec
	out.Status, out.Added = ImportAdded, added
	return out
}
