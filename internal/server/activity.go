package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// The autopilot's activity: every note it makes, kept so the person can
// see later what it did while nobody was looking — which is the point of
// an autopilot. Notes are appended a line each to a file in the config
// directory, so a restart or a reload keeps them, and the file is
// rewritten to the newest activityMax once it is twice that.

const (
	activityFile = "autopilot-activity.jsonl"
	activityMax  = 500
	// activityDefault is how many a list gives when not told.
	activityDefault = 100
)

type activityLog struct {
	file string

	mu    sync.Mutex
	notes []proto.AutopilotNote // oldest first
	lines int                   // lines in the file
	// needNL is a file whose last line was cut short: the next line
	// written starts a line of its own rather than finishing that one.
	needNL bool
}

func loadActivity(configDir string) *activityLog {
	l := &activityLog{file: filepath.Join(configDir, activityFile)}
	raw, err := os.ReadFile(l.file)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("autopilot activity: %v", err)
		}
		return l
	}
	l.needNL = len(raw) > 0 && raw[len(raw)-1] != '\n'
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		l.lines++
		var n proto.AutopilotNote
		if json.Unmarshal(sc.Bytes(), &n) == nil {
			l.notes = append(l.notes, n)
		}
	}
	if len(l.notes) > activityMax {
		l.notes = l.notes[len(l.notes)-activityMax:]
	}
	return l
}

// add keeps a note, stamped now unless it has a time, and returns it.
func (l *activityLog) add(n proto.AutopilotNote) proto.AutopilotNote {
	if n.At.IsZero() {
		n.At = time.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.notes = append(l.notes, n)
	if len(l.notes) > activityMax {
		l.notes = append([]proto.AutopilotNote(nil), l.notes[len(l.notes)-activityMax:]...)
	}
	line, err := json.Marshal(n)
	if err != nil {
		return n
	}
	if l.lines+1 > 2*activityMax {
		l.rewriteLocked()
		return n
	}
	f, err := os.OpenFile(l.file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Printf("autopilot activity: %v", err)
		return n
	}
	defer f.Close()
	if l.needNL {
		line = append([]byte("\n"), line...)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		log.Printf("autopilot activity: %v", err)
		return n
	}
	l.needNL = false
	l.lines++
	return n
}

// rewriteLocked writes the notes kept, and only those, through a file
// moved into place, so a crash leaves the old file or the new one.
func (l *activityLog) rewriteLocked() {
	var b bytes.Buffer
	for _, n := range l.notes {
		if line, err := json.Marshal(n); err == nil {
			b.Write(line)
			b.WriteByte('\n')
		}
	}
	tmp := l.file + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		log.Printf("autopilot activity: %v", err)
		return
	}
	if err := os.Rename(tmp, l.file); err != nil {
		log.Printf("autopilot activity: %v", err)
		return
	}
	l.lines, l.needNL = len(l.notes), false
}

// list is the newest notes first, of one project or of all of them.
func (l *activityLog) list(projectID string, limit int) []proto.AutopilotNote {
	switch {
	case limit <= 0:
		limit = activityDefault
	case limit > activityMax:
		limit = activityMax
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []proto.AutopilotNote{}
	for i := len(l.notes) - 1; i >= 0 && len(out) < limit; i-- {
		if projectID == "" || l.notes[i].ProjectID == projectID {
			out = append(out, l.notes[i])
		}
	}
	return out
}

func (s *Server) autopilotActivity(p proto.AutopilotActivityParams) proto.AutopilotActivity {
	return proto.AutopilotActivity{Notes: s.autopilot.activity.list(p.ProjectID, p.Limit)}
}
