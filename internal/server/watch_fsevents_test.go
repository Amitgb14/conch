//go:build darwin && cgo

package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFSEventsPath: FSEvents reports paths relative to the device root, so
// without a leading separator, and resolves symlinks on the way — /var comes
// back as /private/var. Both have to be undone, or nothing matches the
// worktree the stream was opened on and every event is dropped.
func TestFSEventsPath(t *testing.T) {
	for _, tc := range []struct {
		name, root, in, want string
	}{
		{"the missing leading separator", "/src/api", "src/api/main.go", "/src/api/main.go"},
		{"already absolute", "/src/api", "/src/api/main.go", "/src/api/main.go"},
		{"the worktree itself", "/src/api", "src/api", "/src/api"},
		{"a path it cleans", "/src/api", "src/api/./sub//main.go", "/src/api/sub/main.go"},
		{"outside any root is left alone", "/src/api", "other/place.go", "/other/place.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fseventsPath(tc.root, tc.in); got != tc.want {
				t.Fatalf("fseventsPath(%q, %q) = %q, want %q", tc.root, tc.in, got, tc.want)
			}
		})
	}
}

// TestFSEventsPathThroughASymlink is the case that matters on macOS: the
// temporary directories every test uses are reached through /var, which is a
// link to /private/var, and the stream reports the resolved side.
func TestFSEventsPathThroughASymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == link {
		t.Skip("nothing was resolved, so there is no case to test")
	}

	// A stream opened on the link reports the resolved path; it has to come
	// back under the link, which is the root the watcher knows.
	got := fseventsPath(link, resolved[1:]+"/src/main.go")
	want := filepath.Join(link, "src", "main.go")
	if got != want {
		t.Fatalf("fseventsPath(%q, resolved…) = %q, want %q", link, got, want)
	}
	// The root itself maps back too.
	if got := fseventsPath(link, resolved[1:]); got != link {
		t.Fatalf("the root came back as %q, want %q", got, link)
	}
	// A path already under the link is left as it is.
	under := filepath.Join(link, "a.go")
	if got := fseventsPath(link, under); got != under {
		t.Fatalf("a path under the root became %q", got)
	}
}
