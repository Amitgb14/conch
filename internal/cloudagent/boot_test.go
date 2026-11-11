package cloudagent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/relay"
)

const testMachine = "m_0123456789abcdef"

func writeBoot(t *testing.T, dir, body string, perm os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, "boot.json")
	if err := os.WriteFile(p, []byte(body), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, perm); err != nil { // past the umask
		t.Fatal(err)
	}
	return p
}

func TestReadBoot(t *testing.T) {
	dir := t.TempDir()
	good := `{"machine_id":"` + testMachine + `","api":"https://api.example.com","machine_token":"ccm_x","conch_version":"0.1.12"}`
	b, err := ReadBoot(writeBoot(t, dir, good, 0o600))
	if err != nil {
		t.Fatal(err)
	}
	if b.MachineID != testMachine || b.API != "https://api.example.com" || b.MachineToken != "ccm_x" || b.ConchVersion != "0.1.12" {
		t.Fatalf("read %+v", b)
	}
	// conch_version may be absent: there is nothing to upgrade to.
	if _, err := ReadBoot(writeBoot(t, dir, `{"machine_id":"`+testMachine+`","api":"http://127.0.0.1:1","machine_token":"t"}`, 0o400)); err != nil {
		t.Fatalf("0400 without a version: %v", err)
	}
}

func TestReadBootRefuses(t *testing.T) {
	dir := t.TempDir()
	good := `{"machine_id":"` + testMachine + `","api":"https://api.example.com","machine_token":"ccm_x"}`
	for _, c := range []struct {
		name, body string
		perm       os.FileMode
		want       string
	}{
		{"world-readable", good, 0o644, "readable by others"},
		{"group-readable", good, 0o640, "readable by others"},
		{"world-writable", good, 0o602, "readable by others"},
		{"not json", `{"machine_id":`, 0o600, "unexpected end"},
		{"empty", ``, 0o600, "unexpected end"},
		{"no token", `{"machine_id":"` + testMachine + `","api":"https://a.example"}`, 0o600, "missing machine_token"},
		{"nothing", `{}`, 0o600, "missing api, machine_id, machine_token"},
		{"bad id", `{"machine_id":"m_X","api":"https://a.example","machine_token":"t"}`, 0o600, "not a machine id"},
		{"bad api", `{"machine_id":"` + testMachine + `","api":"ftp://a.example","machine_token":"t"}`, 0o600, "not an http(s) URL"},
		{"no host", `{"machine_id":"` + testMachine + `","api":"https://","machine_token":"t"}`, 0o600, "not an http(s) URL"},
		{"spaced token", `{"machine_id":"` + testMachine + `","api":"https://a.example","machine_token":"a b"}`, 0o600, "white space"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ReadBoot(writeBoot(t, dir, c.body, c.perm))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
		})
	}
	if _, err := ReadBoot(filepath.Join(dir, "absent.json")); err == nil || !strings.Contains(err.Error(), "not there yet") {
		t.Fatalf("missing: %v", err)
	}
	if _, err := ReadBoot(dir); err == nil {
		t.Fatal("a directory: want an error")
	}
	big := writeBoot(t, dir, `{"machine_id":"`+strings.Repeat(" ", maxBoot)+`"}`, 0o600)
	if _, err := ReadBoot(big); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("oversize: %v", err)
	}
}

func TestLoadKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "noise.key")
	k1, made, err := LoadKey(path)
	if err != nil || !made {
		t.Fatalf("first: made %v err %v", made, err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v, want 0600", st.Mode(), err)
	}
	if st, _ := os.Stat(filepath.Dir(path)); st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v, want 0700", st.Mode())
	}
	k2, made, err := LoadKey(path)
	if err != nil || made {
		t.Fatalf("second: made %v err %v", made, err)
	}
	if relay.EncodeKey(k1.Public) != relay.EncodeKey(k2.Public) || len(k1.Public) != 32 {
		t.Fatal("the key changed between loads")
	}
	// The public half is the private half's: what clients pin is what the
	// tunnel answers with.
	again, _ := relay.KeyFromPrivate(relay.EncodeKey(k2.Private))
	if relay.EncodeKey(again.Public) != relay.EncodeKey(k1.Public) {
		t.Fatal("public half not derived from the private")
	}
}

func TestLoadKeyRefuses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "noise.key")
	if _, _, err := LoadKey(path); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0o644)
	if _, _, err := LoadKey(path); err == nil || !strings.Contains(err.Error(), "leaked") {
		t.Fatalf("0644: %v", err)
	}
	bad := filepath.Join(dir, "bad.key")
	os.WriteFile(bad, []byte("not a key\n"), 0o600)
	if _, _, err := LoadKey(bad); err == nil {
		t.Fatal("garbage: want an error")
	}
	empty := filepath.Join(dir, "empty.key")
	os.WriteFile(empty, nil, 0o600)
	if _, _, err := LoadKey(empty); err == nil {
		t.Fatal("empty: want an error")
	}
	// A parent that cannot be made.
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o600)
	if _, _, err := LoadKey(filepath.Join(file, "noise.key")); err == nil {
		t.Fatal("under a file: want an error")
	}
}

// TestBootAndKeyAreTheFilesThemselves: boot.json and the key are read
// only as files of their own — a symlink is refused, and a FIFO is refused
// at once rather than waited on for ever.
func TestBootAndKeyAreTheFilesThemselves(t *testing.T) {
	dir := t.TempDir()
	real := writeBoot(t, dir, `{"machine_id":"`+testMachine+`","api":"https://cp.example","machine_token":"t"}`, 0o600)
	if _, err := ReadBoot(real); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	os.Symlink(real, link)
	if _, err := ReadBoot(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("boot.json through a symlink: %v", err)
	}
	fifo := filepath.Join(dir, "fifo.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := ReadBoot(fifo); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a file") {
			t.Fatalf("a FIFO: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO hung")
	}

	key := filepath.Join(dir, "noise.key")
	if _, _, err := LoadKey(key); err != nil {
		t.Fatal(err)
	}
	klink := filepath.Join(dir, "link.key")
	os.Symlink(key, klink)
	if _, _, err := LoadKey(klink); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("the key through a symlink: %v", err)
	}
	// A dangling symlink is not made through, either.
	dangling := filepath.Join(dir, "dangling.key")
	os.Symlink(filepath.Join(dir, "elsewhere"), dangling)
	if _, _, err := LoadKey(dangling); err == nil {
		t.Fatal("a key made through a dangling symlink")
	}
	if _, err := os.Stat(filepath.Join(dir, "elsewhere")); err == nil {
		t.Fatal("the symlink's target was written")
	}
}

// TestCredentialKeepsGroups: a process started as the user keeps its
// supplementary groups, which Go would otherwise clear.
func TestCredentialKeepsGroups(t *testing.T) {
	me, err := CurrentUser()
	if err != nil {
		t.Skip(err)
	}
	if len(me.Groups) == 0 {
		t.Fatalf("no groups for %s", me.Name)
	}
	other := *me
	other.UID = os.Geteuid() + 1
	attrs := other.credential()
	if attrs == nil || !slices.Equal(attrs.Credential.Groups, me.Groups) {
		t.Fatalf("credential %+v, want groups %v", attrs, me.Groups)
	}
}
