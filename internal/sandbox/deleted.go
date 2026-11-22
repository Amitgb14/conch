package sandbox

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
// (#199), and conch sandbox ls then showed it as a stray that costs until
// somebody deletes it. So what conch or conch-sandbox deletes is
// remembered, for long enough to cover that, and ls shows those as
// deleting. It lives here so both programs keep the one record.

// DeletingFor is how long a deleted sandbox is shown as deleting while its
// provider still lists it.
const DeletingFor = 10 * time.Minute

// DeletedFile is the record.
func DeletedFile() string { return filepath.Join(config.Dir(), "sandboxes-deleted.json") }

// readDeleted is the record, by provider:id; a missing or damaged file is
// an empty one, since it only softens what ls says.
func readDeleted() map[string]time.Time {
	m := map[string]time.Time{}
	if b, err := os.ReadFile(DeletedFile()); err == nil {
		if json.Unmarshal(b, &m) != nil {
			m = map[string]time.Time{}
		}
	}
	return m
}

func writeDeleted(m map[string]time.Time) {
	if len(m) == 0 {
		os.Remove(DeletedFile())
		return
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = config.WriteFileAtomic(DeletedFile(), append(b, '\n'), 0o600)
}

// NoteDeleted records that provider's id has just been deleted.
func NoteDeleted(provider, id string, at time.Time) {
	m := readDeleted()
	m[provider+":"+id] = at
	writeDeleted(m)
}

// Deleted is provider's sandboxes deleted within DeletingFor of now.
func Deleted(provider string, now time.Time) map[string]bool {
	out := map[string]bool{}
	for k, at := range readDeleted() {
		if id, ok := strings.CutPrefix(k, provider+":"); ok && now.Sub(at) < DeletingFor {
			out[id] = true
		}
	}
	return out
}

// ForgetDeleted drops provider's records that are too old or whose sandbox
// it no longer lists: deleted by now, so nothing to soften, and a sandbox
// of that id seen later is somebody's own.
func ForgetDeleted(provider string, listed map[string]bool, now time.Time) {
	m := readDeleted()
	changed := false
	for k, at := range m {
		id, ok := strings.CutPrefix(k, provider+":")
		if ok && !listed[id] || now.Sub(at) >= DeletingFor {
			delete(m, k)
			changed = true
		}
	}
	if changed {
		writeDeleted(m)
	}
}
