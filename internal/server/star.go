package server

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/Amitgb14/conch/internal/proto"
)

// A starred session is one somebody means to come back to. A project
// collects hundreds of conversations and the useful one is remembered by
// its first line, which is no way to find it a week later; a star is how
// it says so.
//
// The mark is conch's and lives here, not in the agent's store: those
// files are the agent's — Claude's JSONL, Codex's rollouts — and conch
// reads them and writes nothing. It is the server's rather than the
// TUI's for the reason review comments are: a phone, a second terminal
// and the command line are looking at the same sessions, and a star kept
// in one of them is a star the others cannot see.

// starMax bounds the file. Somebody who stars a thousand conversations
// has not found a way to find one.
const starMax = 2000

// starBook is which sessions are starred, by agent and id.
type starBook struct {
	file string

	mu   sync.Mutex
	on   map[string]bool
	seen []string // in the order they were starred, for trimming the oldest
}

// starKey names a session: its agent's store and its id within it. Two
// agents can hold the same id, and they are different conversations.
func starKey(agent, id string) string { return agent + "\x00" + id }

type starFile struct {
	Starred []starEntry `json:"starred"`
}

type starEntry struct {
	Agent string `json:"agent"`
	ID    string `json:"id"`
}

func loadStars(configDir string) *starBook {
	b := &starBook{file: filepath.Join(configDir, "stars.json"), on: map[string]bool{}}
	raw, err := os.ReadFile(b.file)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("stars: %v", err)
		}
		return b
	}
	var saved starFile
	if err := json.Unmarshal(raw, &saved); err != nil {
		log.Printf("stars: %s: %v", b.file, err)
		return b
	}
	for _, e := range saved.Starred {
		if e.Agent == "" || e.ID == "" {
			continue
		}
		key := starKey(e.Agent, e.ID)
		if !b.on[key] {
			b.on[key], b.seen = true, append(b.seen, key)
		}
	}
	return b
}

// set stars a session or stops. It answers whether anything changed, so
// a caller does not write the file for a star that was already there.
func (b *starBook) set(agent, id string, on bool) (changed bool, err *proto.Error) {
	if agent == "" || id == "" {
		return false, proto.Errorf(proto.ErrBadRequest, "a session is an agent and an id")
	}
	b.mu.Lock()
	key := starKey(agent, id)
	switch {
	case on == b.on[key]:
		b.mu.Unlock()
		return false, nil
	case on:
		b.on[key] = true
		b.seen = append(b.seen, key)
		// The oldest star goes when there are too many, which is kinder
		// than refusing: somebody starring is saying "this one", not
		// asking about a limit they have never seen.
		for len(b.seen) > starMax {
			delete(b.on, b.seen[0])
			b.seen = b.seen[1:]
		}
	default:
		delete(b.on, key)
		for i, k := range b.seen {
			if k == key {
				b.seen = append(b.seen[:i], b.seen[i+1:]...)
				break
			}
		}
	}
	saved := b.snapshotLocked()
	b.mu.Unlock()
	b.write(saved)
	return true, nil
}

// has reports whether a session is starred.
func (b *starBook) has(agent, id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.on[starKey(agent, id)]
}

// mark fills Starred in on a list of sessions, in place.
func (b *starBook) mark(sessions []proto.SessionInfo) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range sessions {
		s := &sessions[i]
		// No guard on an empty id: set refuses one and load skips one,
		// so the book holds no such key and the lookup answers false.
		s.Starred = b.on[starKey(s.Agent, s.ID)]
	}
}

func (b *starBook) snapshotLocked() starFile {
	out := starFile{Starred: make([]starEntry, 0, len(b.seen))}
	for _, key := range b.seen {
		agent, id, found := splitStarKey(key)
		if found {
			out.Starred = append(out.Starred, starEntry{Agent: agent, ID: id})
		}
	}
	return out
}

func splitStarKey(key string) (agent, id string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

func (b *starBook) write(saved starFile) {
	raw, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		log.Printf("stars: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(b.file), 0o700); err != nil {
		log.Printf("stars: %v", err)
		return
	}
	tmp := b.file + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		log.Printf("stars: %v", err)
		return
	}
	// Renamed into place, so a server stopped mid-write leaves the stars
	// it had rather than half a file.
	if err := os.Rename(tmp, b.file); err != nil {
		log.Printf("stars: %v", err)
	}
}

// starSession is the method: mark a session, or stop.
func (s *Server) starSession(p proto.SessionStarParams) (any, *proto.Error) {
	if _, err := s.stars.set(p.Agent, p.ID, p.On); err != nil {
		return nil, err
	}
	return nil, nil
}
