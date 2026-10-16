// Package sshtest is a stand-in for ssh in tests, so the files of a host a
// terminal is logged in to (fs.ssh.v1) are tested without a network: the
// test binary, run under the name ssh, logs in by waiting as a shell would,
// and asked for sftp serves a folder of this machine as the host's home.
//
// A test package calls Main from TestMain and Install before it starts a
// terminal or a server.
package sshtest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

// Main runs the stand-in when this binary was started as ssh, and never
// returns then. Hosts called denied, nosftp and hang fail as ssh does: a
// password wanted, no sftp server, and no answer at all.
func Main() {
	if filepath.Base(os.Args[0]) == "ssh" {
		os.Exit(run(os.Args[1:]))
	}
}

// Host is the stand-in, installed for one test.
type Host struct {
	SSH  string // the program, to run as a terminal's command
	Home string // what the host's home is
	log  string
}

// Install makes the stand-in the ssh conch runs ($CONCH_SSH), with a fresh
// home for its host.
func Install(t *testing.T) *Host {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := &Host{SSH: filepath.Join(t.TempDir(), "ssh"), log: filepath.Join(t.TempDir(), "ssh.log")}
	if err := os.Symlink(exe, h.SSH); err != nil {
		t.Fatal(err)
	}
	h.Home, _ = filepath.EvalSymlinks(t.TempDir())
	t.Setenv("CONCH_SSH", h.SSH)
	t.Setenv("CONCH_FAKE_SSH_HOME", h.Home)
	t.Setenv("CONCH_FAKE_SSH_LOG", h.log)
	return h
}

// Runs is every command line the stand-in was run with, one a line.
func (h *Host) Runs() string {
	b, _ := os.ReadFile(h.log)
	return string(b)
}

// Sessions counts the sftp sessions made.
func (h *Host) Sessions() int { return strings.Count(h.Runs(), " sftp\n") }

func run(args []string) int {
	if log := os.Getenv("CONCH_FAKE_SSH_LOG"); log != "" {
		if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(f, strings.Join(args, " "))
			f.Close()
		}
	}
	n := len(args)
	if n < 2 || args[n-1] != "sftp" || !slices.Contains(args, "-s") {
		fmt.Println("logged in")
		io.Copy(io.Discard, os.Stdin) // a shell, until the terminal closes
		return 0
	}
	switch args[n-2] {
	case "denied":
		fmt.Fprintln(os.Stderr, "me@denied: Permission denied (publickey,password).")
		return 255
	case "nosftp":
		fmt.Fprintln(os.Stderr, "subsystem request failed on channel 0")
		return 1
	case "hang":
		time.Sleep(time.Minute)
		return 0
	}
	if err := os.Chdir(os.Getenv("CONCH_FAKE_SSH_HOME")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	srv, err := sftp.NewServer(struct {
		io.Reader
		io.WriteCloser
	}{os.Stdin, os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := srv.Serve(); err != nil && err != io.EOF {
		return 1
	}
	return 0
}
