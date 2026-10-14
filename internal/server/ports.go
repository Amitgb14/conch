package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/Amitgb14/conch/internal/proto"
)

// Every checkout gets a block of ports of its own, handed to the panes
// started in it as CONCH_PORT: two agents each starting `npm run dev` in
// their own worktree otherwise fight over 3000, and the loser either fails
// or, worse, quietly talks to the other one's server. A block is kept for
// as long as its worktree is on disk, so a dev server restarted tomorrow
// comes back on the same address.
//
// The range sits under the ephemeral ports both macOS (49152 up) and Linux
// (32768 up, but rarely reached that low) hand out, and over the ones
// people configure by hand.
const (
	portFirst = 41000
	portLast  = 48990 // the first port of the last block
)

// portBook is which block each worktree holds, kept in ports.json beside
// projects.json so a reload or a restart gives the same answers.
type portBook struct {
	file string

	mu    sync.Mutex
	bases map[string]int // worktree path → first port
}

// portsFree reports whether nothing on this machine listens on the block
// starting at base. A variable so tests can say which blocks are taken.
var portsFree = func(base int) bool {
	for p := base; p < base+proto.PortBlock; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			return false
		}
		ln.Close()
	}
	return true
}

// portExists reports whether a worktree is still on disk; a variable for
// the same reason.
var portExists = func(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func loadPortBook(configDir string) *portBook {
	b := &portBook{file: filepath.Join(configDir, "ports.json"), bases: map[string]int{}}
	raw, err := os.ReadFile(b.file)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("ports: %v", err)
		}
		return b
	}
	var saved struct {
		Worktrees map[string]int `json:"worktrees"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		log.Printf("ports: %s: %v", b.file, err)
		return b
	}
	for path, base := range saved.Worktrees {
		if validBase(base) {
			b.bases[path] = base
		}
	}
	return b
}

func validBase(base int) bool {
	return base >= portFirst && base <= portLast && (base-portFirst)%proto.PortBlock == 0
}

// of is the block a worktree holds, or 0.
func (b *portBook) of(path string) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bases[path]
}

// assign gives a worktree a block, or the one it already holds. Blocks of
// worktrees no longer on disk are taken back first, so the range is only
// ever as full as the checkouts that exist. 0 when every block is taken.
func (b *portBook) assign(path string) int {
	if b == nil || path == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if base, ok := b.bases[path]; ok {
		return base
	}
	taken := map[int]bool{}
	for p, base := range b.bases {
		if !portExists(p) {
			delete(b.bases, p)
			continue
		}
		taken[base] = true
	}
	for base := portFirst; base <= portLast; base += proto.PortBlock {
		if taken[base] || !portsFree(base) {
			continue
		}
		b.bases[path] = base
		b.saveLocked()
		return base
	}
	log.Printf("ports: no free block for %s", path)
	return 0
}

// release takes a worktree's block back.
func (b *portBook) release(path string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.bases[path]; !ok {
		return
	}
	delete(b.bases, path)
	b.saveLocked()
}

func (b *portBook) saveLocked() {
	saved := struct {
		Worktrees map[string]int `json:"worktrees"`
	}{Worktrees: b.bases}
	raw, _ := json.MarshalIndent(saved, "", "  ")
	tmp := b.file + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		log.Printf("ports: save: %v", err)
		return
	}
	if err := os.Rename(tmp, b.file); err != nil {
		log.Printf("ports: save: %v", err)
	}
}

// portEnv is what a pane started in a checkout holding base is told.
func portEnv(base int) []string {
	if base == 0 {
		return nil
	}
	return []string{fmt.Sprintf("CONCH_PORT=%d", base), fmt.Sprintf("CONCH_PORT_COUNT=%d", proto.PortBlock)}
}
