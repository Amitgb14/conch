package pane

import (
	"encoding/base64"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The Program Status Protocol (OSC 7501): a program says what it is doing
// on the terminal it already has, instead of every tool guessing from its
// spinner. conch has guessed for five agents and keeps the scars —
// Claude Code's title spinner changed shape at 2.1.228 and its "esc to
// interrupt" line went away at 2.1.284, and each time the rules had to be
// rewritten from the real agent. A program that reports its own state
// needs no rule at all, and the report travels down the pty, so it works
// over ssh and inside a container where a socket or a hook would not.
//
// conch is the terminal here, so it reads the reports and answers the
// query a program sends first (`OSC 7501 ; ?`): a program that gets no
// answer takes the protocol as unsupported and stays quiet, which is why
// reading the stream is not on its own enough.
//
// Spec: https://mitchellh.com/writing/program-status-osc7501 (rev 0.3).
// What conch does not do yet is said in the docs rather than guessed at:
// `progress` and `title` are kept but nothing draws them.

// The states a report carries. `clear` is not a state: it removes
// records.
const (
	StatusIdle    = "idle"
	StatusWorking = "working"
	StatusDone    = "done"
	StatusBlocked = "blocked"
	StatusError   = "error"
	statusClear   = "clear"
)

// The reasons a program can be blocked.
const (
	BlockedPermission = "permission"
	BlockedQuestion   = "question"
	BlockedAuth       = "auth"
)

// ProgramStatus is one record: what a program said about itself, and
// when. ID is the record's path ("" is the root, "build/test" a child of
// "build").
type ProgramStatus struct {
	ID       string    `json:"id,omitempty"`
	State    string    `json:"state"`
	Kind     string    `json:"kind,omitempty"`     // blocked: permission, question, auth
	App      string    `json:"app,omitempty"`      // e.g. claude-code
	Title    string    `json:"title,omitempty"`    // short label
	Msg      string    `json:"msg,omitempty"`      // one line
	Progress int       `json:"progress,omitempty"` // 0..100; -1 when absent
	At       time.Time `json:"at"`
}

// The limits the protocol sets, so a program cannot grow conch's memory
// by reporting.
const (
	maxIDBytes    = 128
	maxIDSegment  = 32
	maxIDDepth    = 8
	maxAppBytes   = 32
	maxRecords    = 256
	maxFreeText   = 1024
	statusNoValue = -1
)

// parseStatus reads the body of an OSC 7501 report. ok is false for a
// body conch must ignore: an unknown state, a malformed id, free text
// that is not base64 or that decodes to control characters. The spec
// throws the whole report away in each of those cases rather than keeping
// half of it, since half a report is a wrong report.
func parseStatus(body string) (rec ProgramStatus, ok bool) {
	rec.Progress = statusNoValue
	seen := false
	for _, pair := range strings.Split(body, ":") {
		key, value, has := strings.Cut(pair, "=")
		if !has {
			continue // malformed pairs are skipped, the report is not
		}
		switch key {
		case "state":
			switch value {
			case StatusIdle, StatusWorking, StatusDone, StatusBlocked, StatusError, statusClear:
				rec.State, seen = value, true
			default:
				return ProgramStatus{}, false // an unknown state voids it
			}
		case "id":
			if !validID(value) {
				return ProgramStatus{}, false
			}
			rec.ID = value
		case "kind":
			switch value {
			case BlockedPermission, BlockedQuestion, BlockedAuth:
				rec.Kind = value
			}
		case "app":
			if value == "" || len(value) > maxAppBytes || !validName(value) {
				return ProgramStatus{}, false
			}
			rec.App = value
		case "progress":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 100 {
				return ProgramStatus{}, false
			}
			rec.Progress = n
		case "title", "msg":
			text, good := decodeText(value)
			if !good {
				return ProgramStatus{}, false
			}
			if key == "title" {
				rec.Title = text
			} else {
				rec.Msg = text
			}
		}
	}
	if !seen {
		return ProgramStatus{}, false // state is the one required key
	}
	return rec, true
}

// decodeText reads one of the base64 values. Padding is optional, and
// what comes out has to be a line of text: a control character in it
// would be an escape sequence conch went on to draw.
func decodeText(value string) (string, bool) {
	if value == "" {
		return "", true
	}
	if len(value) > maxFreeText {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		if raw, err = base64.RawStdEncoding.DecodeString(value); err != nil {
			return "", false
		}
	}
	s := string(raw)
	if !utf8.ValidString(s) {
		return "", false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return s, true
}

// validID checks a record's path: segments of the name characters, no
// more than eight deep, under the byte limit.
func validID(id string) bool {
	if id == "" {
		return true // the root record
	}
	if len(id) > maxIDBytes {
		return false
	}
	parts := strings.Split(id, "/")
	if len(parts) > maxIDDepth {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > maxIDSegment || !validName(p) {
			return false
		}
	}
	return true
}

// validName is the character set ids and app names share.
func validName(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '_' || r == '.' || r == '+' || r == '-':
		default:
			return false
		}
	}
	return true
}

// statusRecords is what a pane's program has reported, by id. A report
// replaces its record whole — keys left out are gone, not kept — so this
// stores what arrived and nothing more, except an `app` taken from the
// nearest ancestor that has one.
type statusRecords struct {
	byID  map[string]ProgramStatus
	order []string // least recently updated first, for the cap
}

// apply takes a parsed report. A `clear` removes the record it names and
// everything beneath it; with no id it removes the lot.
func (r *statusRecords) apply(rec ProgramStatus, now time.Time) {
	if rec.State == statusClear {
		r.clear(rec.ID)
		return
	}
	if r.byID == nil {
		r.byID = map[string]ProgramStatus{}
	}
	rec.At = now
	if rec.App == "" {
		rec.App = r.inheritedApp(rec.ID)
	}
	if _, had := r.byID[rec.ID]; !had {
		r.order = append(r.order, rec.ID)
	} else {
		r.touch(rec.ID)
	}
	r.byID[rec.ID] = rec
	for len(r.order) > maxRecords {
		oldest := r.order[0]
		r.order = r.order[1:]
		delete(r.byID, oldest)
	}
}

// inheritedApp is the app of the nearest ancestor that named one, so a
// child record says which program it belongs to without repeating it.
func (r *statusRecords) inheritedApp(id string) string {
	for id != "" {
		cut := strings.LastIndex(id, "/")
		if cut < 0 {
			id = ""
		} else {
			id = id[:cut]
		}
		if rec, ok := r.byID[id]; ok && rec.App != "" {
			return rec.App
		}
	}
	return ""
}

func (r *statusRecords) touch(id string) {
	for i, had := range r.order {
		if had == id {
			r.order = append(append(r.order[:i:i], r.order[i+1:]...), id)
			return
		}
	}
}

// clear removes id and its children; "" removes every record.
func (r *statusRecords) clear(id string) {
	if id == "" {
		r.byID, r.order = nil, nil
		return
	}
	kept := r.order[:0:0]
	for _, had := range r.order {
		if had == id || strings.HasPrefix(had, id+"/") {
			delete(r.byID, had)
			continue
		}
		kept = append(kept, had)
	}
	r.order = kept
}

// settle drops what the protocol says must not outlive the program, when
// it exits or a new shell prompt begins: work that is no longer going on,
// and a question nobody can answer any more. What the program finished or
// failed at stays, since that is the thing the person has not seen yet.
func (r *statusRecords) settle() {
	kept := r.order[:0:0]
	for _, id := range r.order {
		switch r.byID[id].State {
		case StatusWorking, StatusBlocked:
			delete(r.byID, id)
		default:
			kept = append(kept, id)
		}
	}
	r.order = kept
}

// current is the record that says what the program is doing, for a
// reader that wants one answer rather than a tree: anything blocked
// first, since that is what wants a person; then the root, which is the
// program itself; then the newest. Nothing reported leaves ok false.
func (r *statusRecords) current() (ProgramStatus, bool) {
	if len(r.byID) == 0 {
		return ProgramStatus{}, false
	}
	var newest ProgramStatus
	for i := len(r.order) - 1; i >= 0; i-- {
		rec := r.byID[r.order[i]]
		if rec.State == StatusBlocked {
			return rec, true
		}
		if newest.State == "" {
			newest = rec
		}
	}
	if root, ok := r.byID[""]; ok {
		return root, true
	}
	return newest, true
}

// all is every record, oldest update first.
func (r *statusRecords) all() []ProgramStatus {
	out := make([]ProgramStatus, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.byID[id])
	}
	return out
}

// restore puts back what a snapshot carried through a reload, in the
// order it was taken. A record from an older server with no state is
// left out rather than kept as an empty one.
func (r *statusRecords) restore(recs []ProgramStatus) {
	for _, rec := range recs {
		if rec.State == "" {
			continue
		}
		if r.byID == nil {
			r.byID = map[string]ProgramStatus{}
		}
		if _, had := r.byID[rec.ID]; !had {
			r.order = append(r.order, rec.ID)
		}
		r.byID[rec.ID] = rec
	}
}

// statusQuery reports whether a body is the question a program asks
// before it reports anything: "do you speak this?". The answer is the
// same body back.
func statusQuery(body string) bool { return body == "?" }

// statusReply is what conch answers that question with.
const statusReply = "\x1b]7501;?\x1b\\"

// readOSC acts on the sequences one read brought: the window title, what
// the program says about itself, and the shell prompt marker that ends a
// command (OSC 133 A), which is one of the two moments the protocol says
// to drop work that is no longer going on.
func (p *Pane) readOSC(seqs []oscSeq) {
	for _, seq := range seqs {
		switch seq.num {
		case "0", "2":
			if t, ok := titleOf(seq); ok {
				p.mu.Lock()
				p.title = t
				p.mu.Unlock()
			}
		case "7501":
			p.programStatus(seq.data)
		case "133":
			if strings.HasPrefix(seq.data, "A") {
				p.settleStatus()
			}
		}
	}
}

// programStatus answers the query a program opens with, or takes its
// report. The answer goes out the way conch's own key sequences do, so
// it is written by the one goroutine that owns the terminal.
func (p *Pane) programStatus(body string) {
	if statusQuery(body) {
		p.answerStatusQuery()
		return
	}
	rec, ok := parseStatus(body)
	if !ok {
		return // a report conch cannot trust is not half-kept
	}
	p.mu.Lock()
	p.status.apply(rec, time.Now())
	p.mu.Unlock()
	p.notify() // the state changed without a cell moving
}

// answerStatusQuery tells the program conch speaks the protocol. A
// program that gets no answer takes it as unsupported and reports
// nothing, so this is what turns the rest of this file on.
func (p *Pane) answerStatusQuery() {
	if !p.running() {
		return
	}
	p.emuMu.Lock()
	defer p.emuMu.Unlock()
	_, _ = io.WriteString(p.emu.InputPipe(), statusReply)
}

// settleStatus drops the records that must not outlive the command, and
// says so if anything went.
func (p *Pane) settleStatus() {
	p.mu.Lock()
	before := len(p.status.byID)
	p.status.settle()
	changed := before != len(p.status.byID)
	p.mu.Unlock()
	if changed {
		p.notify()
	}
}

// ProgramStatus is what the program in this pane last said about itself,
// if it speaks the Program Status Protocol.
func (p *Pane) ProgramStatus() (ProgramStatus, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status.current()
}

// ProgramStatusAll is every record the program is keeping.
func (p *Pane) ProgramStatusAll() []ProgramStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status.all()
}
