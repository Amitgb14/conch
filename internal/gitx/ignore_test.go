package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Whether git ignores a folder, asked as conch asks git everything (with
// literal pathspecs), and for a folder that is empty or not there yet —
// which IgnoredDirs leaves out.
func TestIgnoresDir(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, ".gitignore", "node_modules/\nbuild\n*.log\n")
	os.MkdirAll(filepath.Join(repo, "node_modules"), 0o755) // empty
	ctx := context.Background()
	for rel, want := range map[string]bool{
		"node_modules": true, "node_modules/": true, "build": true, "sub/build": true,
		"src": false, "not-yet": false, "logs.log": true,
	} {
		got, err := IgnoresDir(ctx, repo, rel)
		if err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", rel, got, err, want)
		}
	}
	if _, err := IgnoresDir(ctx, t.TempDir(), "x"); err == nil {
		t.Error("outside a checkout: no error")
	}
}
