package server

import (
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sshtest"
)

// fakeSSHHere puts internal/sshtest on hand as ssh and returns it.
func fakeSSHHere(t *testing.T) *sshtest.Host { return sshtest.Install(t) }

func sftpRuns(t *testing.T, h *sshtest.Host) int { return h.Sessions() }

func TestSSHFilesConnectTimesOut(t *testing.T) {
	fakeSSHHere(t)
	old := sshFilesConnect
	sshFilesConnect = 200 * time.Millisecond
	defer func() { sshFilesConnect = old }()
	sf := newSSHFiles()
	start := time.Now()
	_, perr := sf.session("p1", remote.Login{Host: "hang"})
	if perr == nil || !strings.Contains(perr.Message, "timed out") {
		t.Fatalf("got %v", perr)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("waited for ssh well past the timeout")
	}
	// A failure is not kept: the next ask tries again.
	sshFilesConnect = old
	t.Setenv("CONCH_FAKE_SSH_HOME", t.TempDir())
	if _, perr := sf.session("p1", remote.Login{Host: "box"}); perr != nil {
		t.Fatal(perr)
	}
	sf.drop("p1")
}

func TestSSHFilesSweepAndReconnect(t *testing.T) {
	log := fakeSSHHere(t)
	sf := newSSHFiles()
	box := remote.Login{Host: "box"}
	fsys, perr := sf.session("p1", box)
	if perr != nil {
		t.Fatal(perr)
	}
	if _, err := fsys.ReadDir("."); err != nil {
		t.Fatal(err)
	}
	// Used just now: kept.
	sf.sweep(time.Now())
	if _, perr := sf.session("p1", box); perr != nil || sftpRuns(t, log) != 1 {
		t.Fatalf("not kept: %v, %d runs", perr, sftpRuns(t, log))
	}
	// Idle long enough: closed, and made again when wanted.
	sf.sweep(time.Now().Add(sshFilesIdle))
	if len(sf.open) != 0 {
		t.Fatal("an idle session was kept")
	}
	if _, perr := sf.session("p1", box); perr != nil || sftpRuns(t, log) != 2 {
		t.Fatalf("not made again: %v, %d runs", perr, sftpRuns(t, log))
	}

	// ssh going away (the network, say) is noticed, and the next ask
	// logs in again rather than failing on a dead session.
	ss := sf.open["p1"]
	ss.cmd.Process.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for !ss.dead.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if _, perr := sf.session("p1", box); perr != nil || sftpRuns(t, log) != 3 {
		t.Fatalf("not made again after ssh died: %v, %d runs", perr, sftpRuns(t, log))
	}
	// Another login on the same pane: another session.
	if _, perr := sf.session("p1", remote.Login{Host: "box", Opts: []string{"-p", "2"}}); perr != nil || sftpRuns(t, log) != 4 {
		t.Fatalf("a new login reused the old: %v, %d runs", perr, sftpRuns(t, log))
	}
	sf.drop("p1")
	sf.drop("p1") // twice is nothing
	if len(sf.open) != 0 {
		t.Fatal("dropped, still open")
	}
}

func TestSSHFilesError(t *testing.T) {
	for _, c := range []struct{ stderr, want string }{
		{"Warning: added\nme@h: Permission denied (publickey).\n", "h wants a password or a key"},
		{"subsystem request failed on channel 0", "h has no sftp server"},
		{"ssh: Could not resolve hostname h: nodename nor servname provided\n\n", "h: ssh: Could not resolve hostname"},
		{"", "h: EOF"},
	} {
		if got := sshFilesError("h", c.stderr, errString("EOF")); !strings.HasPrefix(got, c.want) {
			t.Errorf("%q: %q", c.stderr, got)
		}
	}
	var tb tailBuffer
	tb.Write([]byte(strings.Repeat("x", 9<<10)))
	tb.Write([]byte("end"))
	if s := tb.String(); len(s) > 8<<10 || !strings.HasSuffix(s, "end") {
		t.Fatalf("tail %d bytes", len(s))
	}
}

type errString string

func (e errString) Error() string { return string(e) }
