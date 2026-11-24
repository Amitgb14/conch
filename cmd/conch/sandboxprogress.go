package main

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

// Setting up a sandbox, step by step, for Conch.app's New Sandbox sheet:
// `sandbox create -progress FILE` (and `add`) append one JSON object a
// line to FILE as it goes, so the sheet can show the steps where it was
// asked rather than a terminal printing them. The lines on stderr are
// unchanged; the file is read by something that is not a person.
//
// The steps are what the person waits on: the provider making it, the
// sandbox answering ssh, conch installed there, the install checked to
// be this build, and the machine saved. They are told apart by the words
// setting up already says (progress), so nothing in internal/remote
// learns about the sheet.

// The steps, in order.
const (
	stepCreate  = "create"
	stepAnswer  = "answer"
	stepInstall = "install"
	stepCheck   = "check"
	stepAdd     = "add"
)

// progressEvent is one line of the file. Step and State say where it is;
// Line is what stderr said; End, once, how it finished: "ready",
// "failed" (with Error, the sandbox kept or deleted as Kept says).
type progressEvent struct {
	At      int64  `json:"at"` // Unix milliseconds
	Step    string `json:"step,omitempty"`
	State   string `json:"state,omitempty"` // start, done, fail
	Line    string `json:"line,omitempty"`
	Sandbox string `json:"sandbox,omitempty"`
	Machine string `json:"machine,omitempty"`
	Label   string `json:"label,omitempty"`
	End     string `json:"end,omitempty"`
	Error   string `json:"error,omitempty"`
	Kept    *bool  `json:"kept,omitempty"`
}

type progressLog struct {
	mu   sync.Mutex
	f    *os.File
	step string
	now  func() time.Time
}

// stepLog is where this run's steps go; nil writes nothing.
var stepLog *progressLog

// openProgress starts the file at path, empty; "" is no file.
func openProgress(path string) (*progressLog, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	return &progressLog{f: f, now: time.Now}, nil
}

func (l *progressLog) write(e progressEvent) {
	if l == nil {
		return
	}
	e.At = l.now().UnixMilli()
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	// One write a line, so a reader never sees half of one.
	_, _ = l.f.Write(append(b, '\n'))
}

// begin starts a step, finishing the one before it.
func (l *progressLog) begin(step, line string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.step == step {
		l.write(progressEvent{Step: step, Line: line})
		return
	}
	if l.step != "" {
		l.write(progressEvent{Step: l.step, State: "done"})
	}
	l.step = step
	l.write(progressEvent{Step: step, State: "start", Line: line})
}

// note records a line under the step it belongs to: a new step when the
// words say one has begun, else the current one.
func (l *progressLog) note(line string) {
	if l == nil {
		return
	}
	l.begin(stepOf(line, l.current()), line)
}

func (l *progressLog) current() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.step
}

// stepOf is the step a line of setting up belongs to.
func stepOf(line, current string) string {
	switch {
	case strings.HasPrefix(line, "waiting for ") && strings.HasSuffix(line, " to answer"):
		return stepAnswer
	case strings.HasPrefix(line, "probing "),
		strings.HasPrefix(line, "building conch"),
		strings.HasPrefix(line, "copying conch"),
		strings.HasPrefix(line, "downloading conch"),
		strings.HasPrefix(line, "no way to build conch"),
		strings.HasPrefix(line, "conch is not installed"),
		strings.HasPrefix(line, "conch on ") && strings.Contains(line, "older build"):
		return stepInstall
	case line == "connecting":
		return stepCheck
	}
	if current == "" {
		return stepCreate
	}
	return current
}

// sandbox says which sandbox this is, once the provider has named it.
func (l *progressLog) sandbox(id string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.write(progressEvent{Step: l.step, Sandbox: id})
}

// ready ends the run: every step done, the machine saved.
func (l *progressLog) ready(machine, label string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.step != "" && l.step != stepAdd {
		l.write(progressEvent{Step: l.step, State: "done"})
	}
	l.write(progressEvent{Step: stepAdd, State: "done", Machine: machine, Label: label})
	l.step = stepAdd
	l.write(progressEvent{End: "ready", Machine: machine, Label: label})
}

// failed ends the run on the step it was at. An install that worked but
// left an older conch is the check's failure, not the install's.
func (l *progressLog) failed(err error, kept bool) {
	if l == nil || err == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	step := l.step
	if step == "" {
		step = stepCreate
	}
	if strings.Contains(err.Error(), "still from an older build after installing") {
		if step != stepCheck {
			l.write(progressEvent{Step: step, State: "done"})
		}
		step = stepCheck
	}
	l.write(progressEvent{Step: step, State: "fail", Error: err.Error()})
	l.write(progressEvent{End: "failed", Error: err.Error(), Kept: &kept})
}

func (l *progressLog) close() {
	if l != nil {
		_ = l.f.Close()
	}
}
