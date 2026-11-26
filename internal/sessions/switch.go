package sessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Switching a conversation to another agent (#352): the conversation one
// agent kept, written as a session of another agent's own, which that
// agent resumes as if it had held it all along. Agents read only their own
// files, so conch translates:
//
//   - messages cross word for word;
//   - shell commands become the target's own shell tool, with their
//     output as it was kept (start and end);
//   - file reads, edits and other tools become one-line notes — an edit is
//     never replayed as the target's own edit, since the file already says
//     what it did;
//   - thinking and reasoning were never kept (signed or encrypted);
//   - the latest plan, and a line saying where the conversation came from,
//     end it.
//
// A conversation larger than the target's context keeps its first request
// and its latest turns whole and shortens the middle (FitConversation). A
// summary of that middle by the brain is left for later.

// SwitchBudget is how much of a conversation crosses, in bytes of text: a
// little over 100k tokens, which leaves room in Claude Code's context
// (200k) and Codex's (272k) for the work that follows.
const SwitchBudget = 400 << 10

// maxShortMiddle is what a shortened message in the middle keeps.
const maxShortMiddle = 1000

// Crossing is what a switch carries and what it leaves behind, for a
// person to read before and after.
type Crossing struct {
	Messages int `json:"messages"` // said word for word
	Commands int `json:"commands"` // as the target's shell tool
	Notes    int `json:"notes"`    // reads, edits, other tools, images
	// Shortened counts the steps in the middle of a conversation too long
	// for the target's context that crossed as a short message or not at
	// all; Fits says none had to.
	Shortened int  `json:"shortened,omitempty"`
	Fits      bool `json:"fits"`
	Bytes     int  `json:"bytes"`
	Plan      bool `json:"plan,omitempty"` // the latest plan crosses
	// Left is what does not cross, as a person reads it.
	Left []string `json:"left,omitempty"`
}

// itemSize is what an item costs in a written session, roughly.
func itemSize(it Item) int { return len(it.Text) + len(it.Output) + len(it.Input) + 40 }

// FitConversation keeps c within budget bytes: the first request and the
// latest turns whole, and the turns between shortened to their messages —
// the newest of them first — with a note saying how many steps were left
// out. A conversation within budget comes back as it was.
func FitConversation(c Conversation, budget int) (Conversation, int) {
	total := 0
	for _, it := range c.Items {
		total += itemSize(it)
	}
	if total <= budget || len(c.Items) < 3 {
		return c, 0
	}
	first := -1
	for i, it := range c.Items {
		if it.Kind == KindMessage && it.Role == "user" {
			first = i
			break
		}
	}
	head := c.Items[:first+1]
	headSize := 0
	for _, it := range head {
		headSize += itemSize(it)
	}
	// The latest turns, as many as fit in three quarters of what is left,
	// starting where a turn starts when one does.
	room := (budget - headSize) * 3 / 4
	t, used := len(c.Items), 0
	for t > first+1 && used+itemSize(c.Items[t-1]) <= room {
		t--
		used += itemSize(c.Items[t])
	}
	for i := t; i < len(c.Items); i++ {
		if c.Items[i].Kind == KindMessage && c.Items[i].Role == "user" {
			if i > t {
				for _, it := range c.Items[t:i] {
					used -= itemSize(it)
				}
				t = i
			}
			break
		}
	}
	if t == len(c.Items) && t > first+1 {
		// Even the last step is over: keep it, cut.
		t = len(c.Items) - 1
	}
	middle := c.Items[first+1 : t]
	// The middle's messages, newest first, in what is left.
	left := budget - headSize - used - 200
	var kept []Item
	for i := len(middle) - 1; i >= 0; i-- {
		it := middle[i]
		if it.Kind != KindMessage {
			continue
		}
		it.Text = clip(it.Text, maxShortMiddle)
		if itemSize(it) > left {
			break
		}
		left -= itemSize(it)
		kept = append([]Item{it}, kept...)
	}
	shortened := len(middle) - len(kept)
	out := c
	out.Items = append([]Item(nil), head...)
	if shortened > 0 {
		at := time.Time{}
		if len(middle) > 0 {
			at = middle[0].Time
		}
		out.Items = append(out.Items, Item{Kind: KindNote, Role: "assistant", Time: at,
			Text: fmt.Sprintf("%d earlier steps were shortened to fit: what was said is kept, cut, and the commands, reads and edits between are left out", shortened)})
	}
	out.Items = append(out.Items, kept...)
	tail := append([]Item(nil), c.Items[t:]...)
	for i := range tail {
		tail[i].Text = clip(tail[i].Text, budget/2)
		tail[i].Output = clipMiddle(tail[i].Output, budget/4)
	}
	out.Items = append(out.Items, tail...)
	return out, shortened
}

// Measure says what switching c to another agent would carry, fitted to
// budget.
func Measure(c Conversation, budget int) (Conversation, Crossing) {
	fit, shortened := FitConversation(c, budget)
	cr := Crossing{Shortened: shortened, Fits: shortened == 0, Plan: fit.Plan != nil}
	for _, it := range fit.Items {
		cr.Bytes += itemSize(it)
		switch it.Kind {
		case KindMessage:
			cr.Messages++
		case KindCommand:
			cr.Commands++
		default:
			cr.Notes++
		}
	}
	from := AgentName(c.Session.Agent)
	cr.Left = append(cr.Left, from+"'s thinking and reasoning")
	if c.MessagesOnly {
		cr.Left = append(cr.Left, "the commands, reads and edits: conch reads only what was said in "+from+"'s sessions")
	}
	if shortened > 0 {
		cr.Left = append(cr.Left, fmt.Sprintf("%d steps in the middle, shortened to fit the context", shortened))
	}
	for _, it := range fit.Items {
		if it.Kind == KindNote && strings.HasPrefix(it.Text, "Image") {
			cr.Left = append(cr.Left, "images, as notes")
			break
		}
	}
	return fit, cr
}

// crossNote is a step the target agent did not take itself, as one line.
func crossNote(it Item, from string) string {
	switch it.Kind {
	case KindRead:
		return fmt.Sprintf("[%s read %s]", from, it.Text)
	case KindEdit:
		what := fmt.Sprintf("edited %s (+%d −%d)", it.Text, it.Added, it.Removed)
		switch {
		case it.Created:
			what = fmt.Sprintf("created %s (%d lines)", it.Text, it.Added)
		case it.Deleted:
			what = "deleted " + it.Text
		}
		if it.Failed {
			what += ", which failed"
		}
		return fmt.Sprintf("[%s %s]", from, what)
	case KindTool:
		s := fmt.Sprintf("[%s used %s", from, it.Text)
		if it.Input != "" {
			s += ": " + it.Input
		}
		if it.Failed {
			s += " — it failed"
		}
		return s + "]"
	case KindCommand:
		return fmt.Sprintf("[%s ran %s]", from, oneLine(it.Text))
	}
	if it.Text == "Image attached" {
		return "[An image was attached here; it was not carried over]"
	}
	return "[" + it.Text + "]"
}

// switchedNote ends a switched conversation: where it came from, and the
// plan as it stood.
func switchedNote(c Conversation, to string) string {
	from := AgentName(c.Session.Agent)
	var b strings.Builder
	fmt.Fprintf(&b, "[conch switched this conversation from %s to %s. What %s read, edited and used is shown above as notes; "+
		"the files are as %s left them. Its reasoning was not carried over.]", from, AgentName(to), from, from)
	if p := c.Plan; p != nil {
		b.WriteString("\n\nThe plan as it stands:\n")
		if t := strings.TrimSpace(p.Text); t != "" {
			b.WriteString(t + "\n")
		}
		for _, s := range p.Steps {
			mark := "[ ]"
			switch s.Status {
			case "completed":
				mark = "[x]"
			case "in_progress":
				mark = "[~]"
			}
			fmt.Fprintf(&b, "- %s %s\n", mark, s.Text)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// startNote opens a conversation whose first request was not kept.
func startNote(from string) string {
	return fmt.Sprintf("[conch carried this conversation over from %s; its beginning was not kept.]", from)
}

// crossOrder is c's items in the order a target takes them: its session
// must open with the person, so notes before the first request — an
// image attached with it — come just after it. start says the
// conversation's own beginning is gone (it opens with the agent), and a
// note must open it instead.
func crossOrder(c Conversation) (items []Item, start bool) {
	f := -1
	for i, it := range c.Items {
		if it.Kind == KindMessage && it.Role == "user" {
			f = i
			break
		}
	}
	if f < 0 {
		return c.Items, len(c.Items) > 0
	}
	for _, it := range c.Items[:f] {
		if it.Kind == KindMessage || it.Kind == KindCommand {
			return c.Items, true
		}
	}
	items = append(items, c.Items[f])
	items = append(items, c.Items[:f]...)
	return append(items, c.Items[f+1:]...), false
}

// ---- Claude Code ----

// claudeRows is c as the Claude Agent SDK's stream messages, which
// ClaudeWriter turns into a session: a command a Bash call and its result,
// every other step a note in the assistant's words.
func claudeRows(c Conversation) []ImportedMessage {
	from := AgentName(c.Session.Agent)
	var rows []ImportedMessage
	n := 0
	id := func() string { n++; return fmt.Sprintf("x%d", n) }
	assistant := func(content []map[string]any, stop string, t time.Time) {
		mid := id()
		msg := map[string]any{"id": "msg_conch_" + mid, "type": "message", "role": "assistant", "model": "<synthetic>",
			"content": content, "stop_reason": stop, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 0, "output_tokens": 0}}
		b, _ := json.Marshal(map[string]any{"type": "assistant", "message": msg})
		rows = append(rows, ImportedMessage{ID: mid, Role: "assistant", Content: string(b), Time: t})
	}
	user := func(content []map[string]any, t time.Time) {
		b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}})
		rows = append(rows, ImportedMessage{ID: id(), Role: "user", Content: string(b), Time: t})
	}
	text := func(s string) []map[string]any { return []map[string]any{{"type": "text", "text": s}} }

	var notes []string
	var notesAt time.Time
	flush := func() {
		if len(notes) > 0 {
			assistant(text(strings.Join(notes, "\n")), "end_turn", notesAt)
			notes = nil
		}
	}
	items, start := crossOrder(c)
	if len(items) == 0 {
		return nil
	}
	if start {
		user(text(startNote(from)), items[0].Time)
	}
	for _, it := range items {
		switch {
		case it.Kind == KindMessage && it.Role == "user":
			flush()
			user(text(it.Text), it.Time)
		case it.Kind == KindMessage:
			flush()
			assistant(text(it.Text), "end_turn", it.Time)
		case it.Kind == KindCommand:
			flush()
			call := "toolu_conch_" + fmt.Sprint(n+1)
			assistant([]map[string]any{{"type": "tool_use", "id": call, "name": "Bash",
				"input": map[string]any{"command": it.Text, "description": "Run by " + from}}}, "tool_use", it.Time)
			if !it.Done {
				continue // answered as interrupted by the writer
			}
			out := it.Output
			if it.Failed && it.Exit != 0 {
				out = strings.TrimRight(fmt.Sprintf("Exit code %d\n%s", it.Exit, out), "\n")
			}
			if out == "" {
				out = "(no output)"
			}
			user([]map[string]any{{"type": "tool_result", "tool_use_id": call, "content": out, "is_error": it.Failed}}, it.Time)
		default:
			if len(notes) == 0 {
				notesAt = it.Time
			}
			notes = append(notes, crossNote(it, from))
		}
	}
	flush()
	assistant(text(switchedNote(c, "claude")), "end_turn", time.Time{})
	return rows
}

// WriteConversation writes c — another agent's conversation — as a new
// Claude Code session to resume in dir, and says what it wrote.
func (w ClaudeWriter) WriteConversation(ctx context.Context, c Conversation, dir string) (Written, error) {
	rows := claudeRows(c)
	if len(rows) == 0 {
		return Written{}, errors.New("nothing was said in it to carry over")
	}
	im := Imported{Session: Session{Agent: "claude", ID: "switch-" + c.Session.Agent + "-" + c.Session.ID,
		Title: c.Session.Title, Branch: c.Session.Branch, Dir: dir}, Messages: rows}
	sid, path, err := w.Write(ctx, im, dir)
	if err != nil {
		return Written{}, err
	}
	return Written{Agent: "claude", ID: sid, Path: path, Files: []string{path}}, nil
}

// ReadBack reads a session WriteConversation wrote, as conch reads any.
func (ClaudeWriter) ReadBack(ctx context.Context, wr Written) (Conversation, error) {
	return ReadConversation(ctx, Env{}, Session{Agent: "claude", ID: wr.ID, Path: wr.Path})
}

// ---- writing, checking, undoing ----

// Written is a session a switch wrote: the agent's id for it, its file,
// and every file written for it.
type Written struct {
	Agent string   `json:"agent"`
	ID    string   `json:"id"`
	Path  string   `json:"path"`
	Files []string `json:"files"`
}

// ConversationWriter writes another agent's conversation as a session of
// its own agent's, and reads one back as conch reads any.
type ConversationWriter interface {
	Agent() string
	WriteConversation(ctx context.Context, c Conversation, dir string) (Written, error)
	ReadBack(ctx context.Context, w Written) (Conversation, error)
}

// WriterFor is the writer of agent's sessions in env, or nil when conch
// writes none: that agent is handed the Markdown document instead.
func WriterFor(agent string, env Env) ConversationWriter {
	switch agent {
	case "claude":
		return ClaudeWriter{Env: env}
	case "codex":
		return CodexWriter{Env: env}
	case "gemini":
		return GeminiWriter{Env: env}
	case "qwen":
		return QwenWriter{Env: env}
	}
	// Copilot CLI is read whole but not written: a session of its is a
	// folder — events.jsonl beside workspace.yaml, checkpoints and a
	// database — and the list of open sessions outside it, more than
	// conch can write as new files alone.
	return nil
}

// CheckReadBack says whether what was read back of a written session is
// what was meant to be written: every message, every command.
func CheckReadBack(sent, got Conversation) error { return checkReadBack(sent, got, true) }

// checkReadBack is CheckReadBack, the commands counted only when
// commands is set: Codex's own importer makes Claude's tools notes.
func checkReadBack(sent, got Conversation, commands bool) error {
	count := func(c Conversation) (user, assistant, commands int) {
		for _, it := range c.Items {
			switch {
			case it.Kind == KindMessage && it.Role == "user":
				user++
			case it.Kind == KindMessage:
				assistant++
			case it.Kind == KindCommand:
				commands++
			}
		}
		return
	}
	su, _, sc := count(sent)
	gu, ga, gc := count(got)
	switch {
	case gu == 0 || ga == 0:
		return errors.New("the session written reads back empty")
	case gu < su:
		return fmt.Errorf("the session written reads back with %d of %d requests", gu, su)
	case commands && gc != sc:
		return fmt.Errorf("the session written reads back with %d of %d commands", gc, sc)
	}
	// Every request, in order and word for word.
	var want, have []string
	for _, it := range sent.Items {
		if it.Kind == KindMessage && it.Role == "user" {
			want = append(want, strings.TrimSpace(it.Text))
		}
	}
	for _, it := range got.Items {
		if it.Kind == KindMessage && it.Role == "user" {
			have = append(have, strings.TrimSpace(it.Text))
		}
	}
	j := 0
	for _, h := range have {
		if j < len(want) && h == want[j] {
			j++
		}
	}
	if j < len(want) {
		return fmt.Errorf("a request did not read back as it was written: %q", clip(want[j], 80))
	}
	return nil
}

// TryWrite writes c with w into a scratch home first and reads it back;
// only when that reads back whole is it written for real. A conversation
// that cannot be written, or reads back wrong, writes nothing, and the
// error says why: the caller hands the Markdown document over instead.
func TryWrite(ctx context.Context, c Conversation, w ConversationWriter, scratch func(string) ConversationWriter, dir string) (Written, error) {
	tmp, err := os.MkdirTemp("", "conch-switch-")
	if err != nil {
		return Written{}, err
	}
	defer os.RemoveAll(tmp)
	dry := scratch(tmp)
	wr, err := dry.WriteConversation(ctx, c, dir)
	if err != nil {
		return Written{}, fmt.Errorf("a trial write failed: %w", err)
	}
	got, err := dry.ReadBack(ctx, wr)
	if err != nil {
		return Written{}, fmt.Errorf("a trial write could not be read back: %w", err)
	}
	if err := CheckReadBack(c, got); err != nil {
		return Written{}, fmt.Errorf("a trial write was wrong: %w", err)
	}
	return w.WriteConversation(ctx, c, dir)
}

// Ways a conversation was written (Switched.Via).
const (
	ViaImporter = "codex import" // Codex's own import of a Claude Code session
	ViaWriter   = "conch"        // a session conch wrote
)

// SwitchWrite writes the conversation c — fitted to the target's context
// as fit — as a session of agent to's own, for it to resume in dir. A
// Claude Code session going to Codex is imported by Codex itself where it
// can (CodexImporter), checked by reading it back; otherwise, and when
// that fails, conch writes it (TryWrite). via says which; an error says
// why nothing could be, and nothing is left behind.
func SwitchWrite(ctx context.Context, c, fit Conversation, to string, env Env, dir string) (Written, string, error) {
	w := WriterFor(to, env)
	if w == nil {
		return Written{}, "", fmt.Errorf("conch does not write %s's sessions", AgentName(to))
	}
	var why error
	if to == "codex" && c.Session.Agent == "claude" && c.Session.Path != "" {
		wr, err := CodexImporter{Env: env}.Import(ctx, c.Session.Path)
		if err == nil {
			got, rerr := ReadConversation(ctx, Env{}, Session{Agent: "codex", ID: wr.ID, Path: wr.Path})
			if rerr == nil {
				rerr = checkReadBack(c, got, false)
			}
			if rerr == nil {
				return wr, ViaImporter, nil
			}
			// Codex's thread is not what was said: it goes, and conch
			// writes its own.
			_ = os.Remove(wr.Path)
			err = fmt.Errorf("Codex's import read back wrong: %w", rerr)
		}
		why = err
	}
	scratch := func(tmp string) ConversationWriter { return WriterFor(to, Env{Home: tmp}) }
	wr, err := TryWrite(ctx, fit, w, scratch, dir)
	if err != nil {
		if why != nil {
			err = fmt.Errorf("%v; and %w", why, err)
		}
		return Written{}, "", err
	}
	return wr, ViaWriter, nil
}

// SwitchRecord is what one switch wrote, so it can be undone: each file
// with what it held when written, and nothing else.
type SwitchRecord struct {
	Stamp  string         `json:"stamp"`
	Pane   string         `json:"pane,omitempty"`
	From   string         `json:"from"`
	FromID string         `json:"from_id"`
	To     string         `json:"to"`
	ToID   string         `json:"to_id"`
	Time   time.Time      `json:"time"`
	Files  []RecordedFile `json:"files"`
}

// RecordedFile is a file a switch wrote and its hash then.
type RecordedFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Record is what wr wrote, hashed as it is now.
func Record(stamp, pane string, c Conversation, wr Written) SwitchRecord {
	r := SwitchRecord{Stamp: stamp, Pane: pane, From: c.Session.Agent, FromID: c.Session.ID, To: wr.Agent, ToID: wr.ID, Time: time.Now()}
	for _, f := range wr.Files {
		r.Files = append(r.Files, RecordedFile{Path: f, SHA256: fileHash(f)})
	}
	return r
}

func fileHash(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Save writes the record into dir, as STAMP.json.
func (r SwitchRecord) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeWhole(filepath.Join(dir, safeStamp(r.Stamp)+".json"), append(b, '\n'))
}

// LoadSwitchRecord reads the record of stamp in dir.
func LoadSwitchRecord(dir, stamp string) (SwitchRecord, error) {
	var r SwitchRecord
	b, err := os.ReadFile(filepath.Join(dir, safeStamp(stamp)+".json"))
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("the record of switch %s cannot be read: %w", stamp, err)
	}
	return r, nil
}

func safeStamp(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
	if s == "" {
		return "_"
	}
	return s
}

// Undo removes the files the switch wrote that are still as it wrote
// them. One the agent has written to since holds its own work and stays,
// named in kept.
func (r SwitchRecord) Undo() (removed, kept []string, err error) {
	for _, f := range r.Files {
		h := fileHash(f.Path)
		switch {
		case h == "":
			continue // gone already
		case h != f.SHA256:
			kept = append(kept, f.Path)
			continue
		}
		if rerr := os.Remove(f.Path); rerr != nil {
			err = errors.Join(err, rerr)
			continue
		}
		removed = append(removed, f.Path)
	}
	return removed, kept, err
}
