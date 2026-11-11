package cloud

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// No test reaches a real Keychain: the default is nil in a test
	// binary already; this says it out loud.
	SetKeychain(nil)
	pollUnit, waitPoll, eventsRetry = time.Millisecond, 10*time.Millisecond, 10*time.Millisecond
	os.Exit(m.Run())
}

// isolate keeps a test off the developer's conch: its own CONCH_HOME and
// HOME, and no inherited session.
func isolate(t *testing.T) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("CONCH_HOME", filepath.Join(dir, "conch"))
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv(URLEnv, "")
	return dir
}

// memKeychain is a Keychain in memory.
type memKeychain struct {
	mu    sync.Mutex
	items map[string]string
}

func (k *memKeychain) Get(a string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.items[a]
	if !ok {
		return "", fmt.Errorf("no item")
	}
	return v, nil
}

func (k *memKeychain) Set(a, s string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.items == nil {
		k.items = map[string]string{}
	}
	k.items[a] = s
	return nil
}

func (k *memKeychain) Delete(a string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.items, a)
	return nil
}
