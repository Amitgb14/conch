package remote

import (
	"os/exec"
	"slices"
	"testing"
)

func TestParseLogin(t *testing.T) {
	for _, c := range []struct {
		argv []string
		host string
		opts []string
		ok   bool
	}{
		{[]string{"ssh", "busybox"}, "busybox", nil, true},
		{[]string{"/usr/bin/ssh", "aghadge@10.0.0.115"}, "aghadge@10.0.0.115", nil, true},
		// Where and how to log in is kept, joined or not.
		{[]string{"ssh", "-p", "2222", "-i", "~/.ssh/k", "-l", "me", "host"}, "host", []string{"-p", "2222", "-i", "~/.ssh/k", "-l", "me"}, true},
		{[]string{"ssh", "-p2222", "-oPort=2", "-J", "jump", "-F", "/c", "host"}, "host", []string{"-p", "2222", "-o", "Port=2", "-J", "jump", "-F", "/c"}, true},
		// Flags in a bunch, a value at the end of one.
		{[]string{"ssh", "-4Ctp", "22", "host"}, "host", []string{"-4", "-C", "-p", "22"}, true},
		// The terminal's own business is left out.
		{[]string{"ssh", "-t", "-A", "-L", "8080:localhost:80", "-D", "1080", "-R", "9:x:9", "-E", "/log", "-e", "none", "host", "top", "-b"}, "host", nil, true},
		{[]string{"ssh", "-o", "RequestTTY=yes", "-o", "LocalForward 8080 x:80", "-o", "User=me", "host"}, "host", []string{"-o", "User=me"}, true},
		{[]string{"ssh", "-o", "BatchMode=no", "host"}, "host", nil, true},
		// After -- the host, whatever it looks like.
		{[]string{"ssh", "-F", "/cfg", "-o", "ControlPath=none", "--", "box"}, "box", []string{"-F", "/cfg", "-o", "ControlPath=none"}, true},
		// Not a login.
		{[]string{"ssh"}, "", nil, false},
		{[]string{"ssh", "-V"}, "", nil, false},
		{[]string{"ssh", "-G", "host"}, "", nil, false},
		{[]string{"ssh", "-O", "exit", "host"}, "", nil, false},
		{[]string{"ssh", "-Q", "cipher"}, "", nil, false},
		{[]string{"ssh", "-p"}, "", nil, false},
		{[]string{"ssh", "--"}, "", nil, false},
		{[]string{"ssh", "-v"}, "", nil, false},
		{[]string{"scp", "host:x", "."}, "", nil, false},
		{[]string{"sshd", "host"}, "", nil, false},
		{nil, "", nil, false},
	} {
		l, ok := ParseLogin(c.argv)
		if ok != c.ok || l.Host != c.host || !slices.Equal(l.Opts, c.opts) {
			t.Errorf("%q: got %q %q %v, want %q %q %v", c.argv, l.Host, l.Opts, ok, c.host, c.opts, c.ok)
		}
	}
}

func TestFilesCommand(t *testing.T) {
	t.Setenv("CONCH_SSH", "/fake/ssh")
	got := FilesCommand(Login{Host: "me@box", Opts: []string{"-p", "22"}})
	want := []string{"/fake/ssh", "-p", "22", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", "-o", "RequestTTY=no", "-s", "--", "me@box", "sftp"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestRunCommand(t *testing.T) {
	t.Setenv("CONCH_SSH", "/fake/ssh")
	got := RunCommand(Login{Host: "me@box", Opts: []string{"-p", "22"}}, []string{"sh", "-c", `exec vi "$1"`, "sh", "/home/me/it's a file"})
	want := []string{"/fake/ssh", "-p", "22", "-t", "--", "me@box", `'sh' '-c' 'exec vi "$1"' 'sh' '/home/me/it'\''s a file'`}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
	// What the host's sh makes of it is the words given, quote and all.
	out, err := exec.Command("/bin/sh", "-c", "printf '%s|' "+got[len(got)-1]).Output()
	if err != nil || string(out) != `sh|-c|exec vi "$1"|sh|/home/me/it's a file|` {
		t.Fatalf("%q %v", out, err)
	}
}
