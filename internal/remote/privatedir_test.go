package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The control sockets' folder has a fixed name under /tmp that anyone can
// make first. conch keeps its connections there only when it is this
// user's own directory, closed to everybody else.
func TestPrivateDir(t *testing.T) {
	base := t.TempDir()

	fresh := filepath.Join(base, "fresh")
	if err := privateDir(fresh); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(fresh); !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("made %v", fi.Mode())
	}
	if err := privateDir(fresh); err != nil {
		t.Fatalf("again: %v", err)
	}

	// Ours but open to others (made under a loose umask, or by an older
	// conch): closed up.
	open := filepath.Join(base, "open")
	os.Mkdir(open, 0o755)
	os.Chmod(open, 0o777)
	if err := privateDir(open); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(open); fi.Mode().Perm() != 0o700 {
		t.Fatalf("left %v", fi.Mode())
	}

	// A link, even to a directory of our own: whoever made the link
	// chooses where it points.
	link := filepath.Join(base, "link")
	os.Symlink(fresh, link)
	if err := privateDir(link); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("link: %v", err)
	}
	file := filepath.Join(base, "file")
	os.WriteFile(file, nil, 0o600)
	if err := privateDir(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file: %v", err)
	}

	// Somebody else's. Tests cannot chown, so this process plays another
	// user.
	old := myUID
	myUID = func() int { return os.Getuid() + 1 }
	t.Cleanup(func() { myUID = old })
	if err := privateDir(fresh); err == nil || !strings.Contains(err.Error(), "another user") {
		t.Fatalf("another user's: %v", err)
	}

	// No parent to make it in.
	myUID = old
	if err := privateDir(filepath.Join(base, "missing", "ctl")); err == nil {
		t.Fatal("made under a missing parent")
	}
}
