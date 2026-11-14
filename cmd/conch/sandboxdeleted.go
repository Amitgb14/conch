package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// A provider may delete in the background: Daytona goes on listing a
// sandbox as started for some seconds after its delete has been taken
// (#199), and ls then showed it as a stray that costs until somebody
// deletes it. So conch remembers what it deleted, for long enough to
// cover that, and ls shows those as deleting.

// deletingFor is how long a sandbox conch deleted is shown as deleting
// while its provider still lists it.
const deletingFor = 10 * time.Minute

func deletedSandboxesFile() string {
	return filepath.Join(config.Dir(), "sandboxes-deleted.json")
}

// readDeleted is the record, by provider:id; a missing or damaged file is
// an empty one, since it only softens what ls says.
func readDeleted() map[string]time.Time {
	m := map[string]time.Time{}
	if b, err := os.ReadFile(deletedSandboxesFile()); err == nil {
		if json.Unmarshal(b, &m) != nil {
			m = map[string]time.Time{}
		}
	}
	return m
}

func writeDeleted(m map[string]time.Time) {
	if len(m) == 0 {
		os.Remove(deletedSandboxesFile())
		return
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	config.WriteFileAtomic(deletedSandboxesFile(), append(b, '\n'), 0o600)
}

// noteDeletedSandbox records that conch has just deleted provider's id.
func noteDeletedSandbox(provider, id string, at time.Time) {
	m := readDeleted()
	m[provider+":"+id] = at
	writeDeleted(m)
}

// deletedSandboxes is provider's sandboxes conch deleted within
// deletingFor of now.
func deletedSandboxes(provider string, now time.Time) map[string]bool {
	out := map[string]bool{}
	for k, at := range readDeleted() {
		if id, ok := strings.CutPrefix(k, provider+":"); ok && now.Sub(at) < deletingFor {
			out[id] = true
		}
	}
	return out
}

// forgetDeleted drops provider's records that are too old or whose
// sandbox it no longer lists: deleted by now, so nothing to soften, and a
// sandbox of that id seen later is somebody's own.
func forgetDeleted(provider string, listed map[string]bool, now time.Time) {
	m := readDeleted()
	changed := false
	for k, at := range m {
		id, ok := strings.CutPrefix(k, provider+":")
		if ok && !listed[id] || now.Sub(at) >= deletingFor {
			delete(m, k)
			changed = true
		}
	}
	if changed {
		writeDeleted(m)
	}
}
