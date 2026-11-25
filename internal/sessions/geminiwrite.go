package sessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// geminiWriteVersion is the Gemini CLI GeminiWriter's sessions were
// checked against: its chatRecordingService.ts, sessionUtils.ts and
// projectRegistry.ts as shipped in 0.66.0.
const geminiWriteVersion = "0.66.0"

// GeminiWriter writes Gemini CLI sessions: a chat file in the folder
// Gemini CLI keeps for the project, which `gemini --resume ID` opens with
// its history.
//
// The file is the JSONL Gemini CLI 0.66 writes, named as it names one
// (session-<minute>-<first 8 of the id>.jsonl): a header with the session
// id and the hash of the project's folder, which Gemini checks, then a
// line per message — the person's as text parts, Gemini's as text with
// its tool calls recorded on it, each with its result, which is how
// Gemini's own resume turns them back into function calls and responses.
//
// The project's folder is ~/.gemini/tmp/<short id> (or under
// $GEMINI_CLI_HOME), the short id from projects.json. A project Gemini CLI
// has not opened yet has none, and conch does not write Gemini's registry:
// it claims a short id the way Gemini does — a .project_root file naming
// the folder, which Gemini looks for when its registry has no entry — and
// Gemini adds it to the registry itself when it starts.
type GeminiWriter struct {
	Env Env
}

// Agent is the agent whose sessions it writes.
func (GeminiWriter) Agent() string { return "gemini" }

// root is Gemini CLI's own folder.
func (w GeminiWriter) root() string {
	home := w.Env.Home
	if d := w.Env.get("GEMINI_CLI_HOME"); d != "" {
		home = d
	}
	return filepath.Join(home, ".gemini")
}

// project is the short id Gemini CLI files dir under, and the marker
// conch had to write to claim one, if it did.
func (w GeminiWriter) project(dir string) (slug, marker string, err error) {
	tmp := filepath.Join(w.root(), "tmp")
	var reg struct {
		Projects map[string]string `json:"projects"`
	}
	if b, err := os.ReadFile(filepath.Join(w.root(), "projects.json")); err == nil {
		_ = json.Unmarshal(b, &reg)
	}
	owner := func(slug string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(tmp, slug, ".project_root"))
		return strings.TrimSpace(string(b)), err == nil
	}
	if s := reg.Projects[dir]; s != "" && safeName(s) {
		if o, ok := owner(s); !ok || o == dir {
			return s, "", nil
		}
	}
	// A folder whose marker names dir, which Gemini would find too.
	if entries, err := os.ReadDir(tmp); err == nil {
		for _, e := range entries {
			if o, ok := owner(e.Name()); ok && o == dir && e.IsDir() {
				return e.Name(), "", nil
			}
		}
	}
	// A new one, named as Gemini names it: the folder's name made a slug,
	// with -1, -2… after it when it is taken.
	taken := map[string]bool{}
	for _, s := range reg.Projects {
		taken[s] = true
	}
	base := geminiSlug(filepath.Base(dir))
	for n := 0; n < 1000; n++ {
		cand := base
		if n > 0 {
			cand = fmt.Sprintf("%s-%d", base, n)
		}
		if taken[cand] {
			continue
		}
		if _, err := os.Stat(filepath.Join(tmp, cand)); err == nil {
			continue // another's, or one Gemini left without a marker
		}
		if err := os.MkdirAll(filepath.Join(tmp, cand), 0o700); err != nil {
			return "", "", err
		}
		m := filepath.Join(tmp, cand, ".project_root")
		f, err := os.OpenFile(m, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			continue
		}
		_, werr := f.WriteString(dir)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			os.Remove(m)
			return "", "", werr
		}
		return cand, m, nil
	}
	return "", "", errors.New("no name left for the project's folder")
}

// geminiSlug is Gemini CLI's slugify: lower case, every run of characters
// other than a letter or digit one "-", none at either end.
func geminiSlug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	if b.Len() == 0 {
		return "project"
	}
	return b.String()
}

// safeName is a single path element.
func safeName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`)
}

// WriteConversation writes c as a new Gemini CLI session to resume in
// dir. Nothing is left behind when it fails.
func (w GeminiWriter) WriteConversation(ctx context.Context, c Conversation, dir string) (Written, error) {
	if err := ctx.Err(); err != nil {
		return Written{}, err
	}
	if !c.HasMessages() {
		return Written{}, errors.New("nothing was said in it to carry over")
	}
	if dir == "" {
		return Written{}, errors.New("no folder to resume it in")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return Written{}, fmt.Errorf("no folder to resume it in: %s is not there", dir)
	}
	slug, marker, err := w.project(dir)
	if err != nil {
		return Written{}, err
	}
	undo := func() {
		if marker != "" {
			os.Remove(marker)
			os.Remove(filepath.Dir(marker)) // only when empty
		}
	}
	id := newSessionUUID()
	now := time.Now()
	chats := filepath.Join(w.root(), "tmp", slug, "chats")
	if err := os.MkdirAll(chats, 0o700); err != nil {
		undo()
		return Written{}, err
	}
	stamp := now.UTC().Format("2006-01-02T15-04")
	path := filepath.Join(chats, "session-"+stamp+"-"+id[:8]+".jsonl")
	for n := 1; fileExists(path); n++ {
		path = filepath.Join(chats, fmt.Sprintf("session-%s-%d-%s.jsonl", stamp, n, id[:8]))
	}
	if err := ctx.Err(); err != nil {
		undo()
		return Written{}, err
	}
	if err := writeWhole(path, geminiChatFile(c, id, dir, now)); err != nil {
		undo()
		return Written{}, err
	}
	wr := Written{Agent: "gemini", ID: id, Path: path, Files: []string{path}}
	if marker != "" {
		wr.Files = append(wr.Files, marker)
	}
	return wr, nil
}

// ReadBack reads a session WriteConversation wrote, as conch reads any.
func (GeminiWriter) ReadBack(ctx context.Context, wr Written) (Conversation, error) {
	return ReadConversation(ctx, Env{}, Session{Agent: "gemini", ID: wr.ID, Path: wr.Path})
}

func fileExists(p string) bool { _, err := os.Lstat(p); return err == nil }

// geminiStamp is a time as Gemini CLI writes one (toISOString).
func geminiStamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// geminiChatFile is c as a Gemini CLI chat file. What Gemini says between two
// of the person's messages is one message, as Gemini records a turn: its
// words and the notes, then a command's call, its result recorded on it,
// and the words after that a message of their own.
func geminiChatFile(c Conversation, id, dir string, now time.Time) []byte {
	from := AgentName(c.Session.Agent)
	sum := sha256.Sum256([]byte(dir))
	first := now
	if len(c.Items) > 0 && !c.Items[0].Time.IsZero() {
		first = c.Items[0].Time
	}
	body := appendJSONLine(nil, map[string]any{"sessionId": id, "projectHash": hex.EncodeToString(sum[:]),
		"startTime": geminiStamp(first), "lastUpdated": geminiStamp(now)})
	n := 0
	last := first
	at := func(t time.Time) string {
		if t.IsZero() {
			t = last
		}
		last = t
		return geminiStamp(t)
	}
	msgID := func() string { n++; return fmt.Sprintf("conch-%s-%d", id[:8], n) }
	user := func(text string, t time.Time) {
		body = appendJSONLine(body, map[string]any{"id": msgID(), "timestamp": at(t), "type": "user",
			"content": []map[string]string{{"text": text}}})
	}
	var said []string
	var saidAt time.Time
	flush := func(calls []map[string]any) {
		if len(said) == 0 && len(calls) == 0 {
			return
		}
		m := map[string]any{"id": msgID(), "timestamp": at(saidAt), "type": "gemini", "content": strings.Join(said, "\n\n")}
		if len(calls) > 0 {
			m["toolCalls"] = calls
		}
		body = appendJSONLine(body, m)
		said = nil
	}
	say := func(text string, t time.Time) {
		if len(said) == 0 {
			saidAt = t
		}
		said = append(said, text)
	}
	items, start := crossOrder(c)
	if start {
		user(startNote(from), items[0].Time)
	}
	for _, it := range items {
		switch {
		case it.Kind == KindMessage && it.Role == "user":
			flush(nil)
			user(it.Text, it.Time)
		case it.Kind == KindMessage:
			say(it.Text, it.Time)
		case it.Kind == KindCommand:
			if len(said) == 0 {
				saidAt = it.Time
			}
			call := fmt.Sprintf("run_shell_command_conch_%d", n+1)
			out, status := geminiShellOutput(it)
			flush([]map[string]any{{
				"id": call, "name": "run_shell_command", "args": map[string]any{"command": it.Text, "description": "Run by " + from},
				"result":    []map[string]any{{"functionResponse": map[string]any{"id": call, "name": "run_shell_command", "response": map[string]string{"output": out}}}},
				"status":    status,
				"timestamp": at(it.Time), "displayName": "Shell", "description": "", "resultDisplay": it.Output,
				"renderOutputAsMarkdown": false,
			}})
		default:
			say(crossNote(it, from), it.Time)
		}
	}
	// The last line joins what was said last, as one turn.
	say(switchedNote(c, "gemini"), time.Time{})
	flush(nil)
	return body
}

// geminiShellOutput is a command's result as Gemini CLI 0.66's shell
// tool returns it to the model (shell.ts: "Output: …", and "Exit Code: N"
// when it is not 0; its own words for a command cancelled), and the
// call's status.
func geminiShellOutput(it Item) (out, status string) {
	if !it.Done {
		return shellCancelled, "cancelled"
	}
	o := it.Output
	if o == "" {
		o = "(empty)"
	}
	out = "Output: " + o
	status = "success"
	if it.Failed {
		status = "error"
		out += fmt.Sprintf("\nExit Code: %d", failedExit(it))
	}
	return out, status
}

// shellCancelled is what Gemini CLI's and Qwen Code's shell tool return
// for a command stopped before it finished, with nothing printed.
const shellCancelled = "Command was cancelled by user before it could complete. There was no output before it was cancelled."

// failedExit is a failed command's exit code, 1 when it was not kept.
func failedExit(it Item) int {
	if it.Exit != 0 {
		return it.Exit
	}
	return 1
}
