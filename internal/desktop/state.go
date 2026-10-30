package desktop

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// maxState bounds desktop.json: a layout, not a store for anything else.
const maxState = 1 << 20

// stateFile is desktop.json: the window's layout — tabs, splits, sizes —
// as the page describes it. The host does not read it; it keeps it, so
// the page decides its own shape and an older page's file still loads in
// a newer one, which upgrades it.
type stateFile struct {
	path string
	mu   sync.Mutex
}

// load is the saved state, or {} for none. A file that is not a JSON
// object — truncated by a crash mid-write on a filesystem that allowed
// it, or edited by hand — is set aside as desktop.json.bad rather than
// lost or left to fail every start.
func (s *stateFile) load(logf func(string, ...any)) json.RawMessage {
	if s.path == "" {
		return json.RawMessage("{}")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			logf("desktop: %v", err)
		}
		return json.RawMessage("{}")
	}
	if !isObject(b) {
		logf("desktop: %s is not a layout; kept as %s.bad", s.path, filepath.Base(s.path))
		_ = os.Rename(s.path, s.path+".bad")
		return json.RawMessage("{}")
	}
	return b
}

// save replaces it, whole or not at all.
func (s *stateFile) save(state json.RawMessage) *Error {
	if !isObject(state) {
		return &Error{Code: "bad_request", Message: "the state is not a JSON object"}
	}
	if len(state) > maxState {
		return &Error{Code: "bad_request", Message: "the state is larger than a layout should be"}
	}
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".desktop-*.json")
	if err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	_, werr := tmp.Write(state)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), s.path)
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return &Error{Code: "internal", Message: werr.Error()}
	}
	return nil
}

func isObject(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 0 && b[0] == '{' && json.Valid(b)
}
