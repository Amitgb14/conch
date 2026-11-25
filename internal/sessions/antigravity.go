package sessions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Antigravity CLI (agy) keeps everything in ~/.gemini/antigravity-cli:
// conversation_summaries.db lists the conversations — id, title, the first
// prompt (preview), the folders it worked in as file:// URIs, times — and
// beside it conversations/<id>.db holds one's state and
// brain/<id>/.system_generated/logs/transcript.jsonl its steps as JSON
// lines. Read off agy 1.5.16's own files; `agy --conversation <id>`
// reopens one. Its only delete is a call to its own running server, so
// conch removes a conversation's rows and moves its files to the trash.

func antigravityRoot(e Env) string { return filepath.Join(e.Home, ".gemini", "antigravity-cli") }

// agyRootOf is where s's files are: beside the summaries it was listed
// from, which for an account's conversation is in that account's folder.
func agyRootOf(e Env, s Session) string {
	if s.Path != "" {
		return filepath.Dir(s.Path)
	}
	return antigravityRoot(e)
}

// antigravity lists agy's conversations in its own folder and in each of
// its accounts' (internal/accounts): a folder given to it as --gemini_dir,
// which holds an antigravity-cli of its own.
func antigravity(e Env, dirs []string) []Session {
	var out []Session
	for _, r := range e.roots("antigravity", filepath.Join(e.Home, ".gemini")) {
		out = append(out, antigravityAt(filepath.Join(r.Dir, "antigravity-cli"), r.Name, dirs)...)
	}
	return out
}

func antigravityAt(root, account string, dirs []string) []Session {
	db := filepath.Join(root, "conversation_summaries.db")
	if _, err := os.Stat(db); err != nil || len(dirs) == 0 {
		return nil
	}
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The folders are a JSON list of URIs, so they are matched here rather
	// than in SQL. A subagent's conversation is part of its parent's.
	const q = "SELECT conversation_id, title, preview, last_modified_time, workspace_uris FROM conversation_summaries" +
		" WHERE parent_conversation_id = '' ORDER BY last_modified_time DESC LIMIT 1000"
	res, err := exec.CommandContext(ctx, bin, "-readonly", "-json", db, q).Output()
	if err != nil {
		// agy keeps the file in WAL mode, and once it has closed there is
		// no shared-memory file beside it, which a read-only open needs and
		// may not make. Then everything is in the file itself, so it is
		// read as it stands, with nothing written beside it.
		u := url.URL{Scheme: "file", Path: db, RawQuery: "immutable=1"}
		res, err = exec.CommandContext(ctx, bin, "-readonly", "-json", u.String(), q).Output()
	}
	if err != nil || len(bytes.TrimSpace(res)) == 0 {
		return nil
	}
	var rows []struct {
		ID       string `json:"conversation_id"`
		Title    string `json:"title"`
		Preview  string `json:"preview"`
		Modified string `json:"last_modified_time"`
		URIs     string `json:"workspace_uris"`
	}
	if json.Unmarshal(res, &rows) != nil {
		return nil
	}
	var out []Session
	for _, r := range rows {
		dir := antigravityDir(r.URIs, dirs)
		updated, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", r.Modified)
		if dir == "" || r.ID == "" || err != nil || time.Since(updated) > maxAge {
			continue
		}
		out = append(out, Session{Agent: "antigravity", ID: r.ID, Dir: dir, Account: account,
			Title: title(firstNonEmpty(r.Title, r.Preview)), Updated: updated, Path: db,
			Output: antigravityOutput(agyTranscriptPath(root, r.ID))})
	}
	return out
}

// antigravityDir is the first of a conversation's folders that is one of
// dirs or inside one.
func antigravityDir(uris string, dirs []string) string {
	var list []string
	if json.Unmarshal([]byte(uris), &list) != nil {
		return ""
	}
	for _, u := range list {
		p, err := url.Parse(u)
		if err != nil || p.Scheme != "file" || !filepath.IsAbs(p.Path) {
			continue
		}
		if withinAny(p.Path, dirs) {
			return p.Path
		}
	}
	return ""
}

func agyTranscriptPath(root, id string) string {
	return filepath.Join(root, "brain", id, ".system_generated", "logs", "transcript.jsonl")
}

// agyOutput is what has been read of one transcript: each model step's
// output tokens, by step, a step written again replacing itself.
type agyOutput struct {
	offset int64
	steps  map[int]int
}

var agyOutputs sync.Map // path → agyOutput

// antigravityOutput is the tokens a conversation's model steps wrote. A
// transcript is read whole once and then only what was appended, a line
// still being written left for next time; one that shrank is read again.
func antigravityOutput(path string) int {
	prev, _ := agyOutputs.Load(path)
	cur, _ := prev.(agyOutput)
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0
	}
	if st.Size() < cur.offset || cur.steps == nil {
		cur = agyOutput{steps: map[int]int{}}
	} else {
		cur.steps = maps.Clone(cur.steps)
	}
	if _, err := f.Seek(cur.offset, io.SeekStart); err == nil {
		r := bufio.NewReaderSize(f, 1<<16)
		for {
			b, err := r.ReadBytes('\n')
			if err != nil {
				break
			}
			cur.offset += int64(len(b))
			if !bytes.Contains(b, []byte(`"output_tokens"`)) {
				continue
			}
			var l struct {
				Step   *int   `json:"step_index"`
				Type   string `json:"type"`
				Output int    `json:"output_tokens"`
			}
			if json.Unmarshal(b, &l) == nil && l.Step != nil && l.Type == "PLANNER_RESPONSE" {
				cur.steps[*l.Step] = l.Output
			}
		}
	}
	agyOutputs.Store(path, cur)
	n := 0
	for _, v := range cur.steps {
		n += v
	}
	return n
}

// antigravityTranscript reads a conversation's steps: what the person
// asked and what the model answered, its tool steps left out.
func antigravityTranscript(e Env, s Session) ([]Turn, error) {
	f, err := os.Open(agyTranscriptPath(agyRootOf(e, s), s.ID))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var turns []Turn
	lines(f, 1<<30, func(b []byte) bool {
		var l struct {
			Type    string    `json:"type"`
			Content string    `json:"content"`
			Created time.Time `json:"created_at"`
		}
		if json.Unmarshal(b, &l) != nil {
			return true
		}
		switch l.Type {
		case "USER_INPUT":
			turns = addTurn(turns, "user", userRequest(l.Content), l.Created)
		case "PLANNER_RESPONSE":
			turns = addTurn(turns, "assistant", l.Content, l.Created)
		}
		return true
	})
	return turns, nil
}

// userRequest is what the person typed, which agy wraps in <USER_REQUEST>
// beside metadata of its own; the text unchanged otherwise.
func userRequest(text string) string {
	const open, closing = "<USER_REQUEST>", "</USER_REQUEST>"
	i := strings.Index(text, open)
	if i < 0 {
		return text
	}
	rest := text[i+len(open):]
	if j := strings.Index(rest, closing); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// antigravityDelete removes a conversation's rows from the summaries, in
// one transaction, and only then moves its files to the trash: rows that
// cannot be removed (no sqlite3, agy holding the database) leave the
// conversation whole rather than listed with nothing behind it.
func antigravityDelete(ctx context.Context, e Env, s Session, trashDir string) error {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return fmt.Errorf("deleting an Antigravity conversation needs the sqlite3 command")
	}
	q := sqlQuote(s.ID)
	// The search and parent tables came later than the summaries; each is
	// emptied only where it exists.
	tables, err := exec.CommandContext(ctx, bin, s.Path,
		"SELECT name FROM sqlite_master WHERE name IN ('conversations_fts', 'conversation_parents')").CombinedOutput()
	if err != nil {
		return fmt.Errorf("sqlite3: %v: %s", err, strings.TrimSpace(string(tables)))
	}
	sql := "BEGIN IMMEDIATE; DELETE FROM conversation_summaries WHERE conversation_id = " + q + ";"
	for _, t := range strings.Fields(string(tables)) {
		sql += " DELETE FROM " + t + " WHERE conversation_id = " + q + ";"
	}
	sql += " COMMIT;"
	// agy writes to it while it runs: wait a moment for it, not forever.
	if out, err := exec.CommandContext(ctx, bin, "-bail", "-cmd", ".timeout 2000", s.Path, sql).CombinedOutput(); err != nil {
		// agy's search table is FTS5; a sqlite3 built without it cannot
		// touch it, and the transaction leaves the conversation whole.
		if strings.Contains(string(out), "no such module: fts5") {
			return fmt.Errorf("deleting an Antigravity conversation needs a sqlite3 with FTS5, and %s has none; nothing was deleted", bin)
		}
		return fmt.Errorf("sqlite3: %v: %s", err, strings.TrimSpace(string(out)))
	}
	root := filepath.Dir(s.Path)
	forgetLast(filepath.Join(root, "cache", "last_conversations.json"), s.ID)
	var paths []string
	for _, p := range []string{
		filepath.Join(root, "brain", s.ID),
		filepath.Join(root, "conversations", s.ID+".db"),
		filepath.Join(root, "conversations", s.ID+".db-wal"),
		filepath.Join(root, "conversations", s.ID+".db-shm"),
		filepath.Join(root, "implicit", s.ID+".pb"),
	} {
		if _, err := os.Lstat(p); err == nil {
			paths = append(paths, p)
		}
	}
	agyOutputs.Delete(agyTranscriptPath(root, s.ID))
	return trash(s.Agent, paths, trashDir)
}

// forgetLast takes a deleted conversation out of the folder → conversation
// map `agy --continue` reopens from, so it is not sent to one that is
// gone. A file it cannot read is agy's business and is left alone.
func forgetLast(path, id string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var last map[string]string
	if json.Unmarshal(b, &last) != nil {
		return
	}
	n := len(last)
	for dir, v := range last {
		if v == id {
			delete(last, dir)
		}
	}
	if len(last) == n {
		return
	}
	if out, err := json.MarshalIndent(last, "", "  "); err == nil {
		_ = os.WriteFile(path, append(out, '\n'), 0o600)
	}
}
