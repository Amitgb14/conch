package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Writers racing on one file: whichever lands last, the file is exactly
// one writer's bytes — never one's with another's tail, which is what a
// shared temporary name produced — and no temporary file is left behind.
func TestWriteFileAtomicUnderRacingWriters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ui.json")
	payloads := [][]byte{}
	for i := 0; i < 8; i++ {
		payloads = append(payloads, bytes.Repeat([]byte{byte('a' + i)}, 1000+i*7919)) // all different lengths
	}
	for round := 0; round < 25; round++ {
		var wg sync.WaitGroup
		for _, p := range payloads {
			wg.Add(1)
			go func(p []byte) {
				defer wg.Done()
				if err := WriteFileAtomic(path, p, 0o600); err != nil {
					t.Error(err)
				}
			}(p)
		}
		wg.Wait()
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		whole := false
		for _, p := range payloads {
			whole = whole || bytes.Equal(got, p)
		}
		if !whole {
			t.Fatalf("round %d: %d bytes that are no one writer's", round, len(got))
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("left behind: %s", strings.Join(names, ", "))
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
}

// A folder that is not there is an error, and leaves nothing behind.
func TestWriteFileAtomicMissingFolder(t *testing.T) {
	if err := WriteFileAtomic(filepath.Join(t.TempDir(), "nope", "x"), []byte("x"), 0o600); err == nil {
		t.Fatal("wrote into a folder that is not there")
	}
}
