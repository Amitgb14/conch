package server_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// gitInitIdentity gives the server's git an author of the test's own and
// none of the developer's configuration.
func gitInitIdentity(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, v := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(v, "T")
	}
	for _, v := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(v, "t@example.com")
	}
}

// TestProjectGitInit: a project added as a plain folder is made a git
// repository on request — the file already there left untracked — and a
// branch can be made in it straight away, with the clients told at once.
func TestProjectGitInit(t *testing.T) {
	gitInitIdentity(t)
	c, dir := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !slices.Contains(proto.Capabilities, proto.CapProjectGitInit) {
		t.Fatal("project.git_init.v1 not announced")
	}
	notes := filepath.Join(dir, "notes")
	os.MkdirAll(notes, 0o755)
	os.WriteFile(filepath.Join(notes, "todo.txt"), []byte("mine\n"), 0o644)
	var proj proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: notes}, &proj); err != nil {
		t.Fatal(err)
	}
	if proj.Git {
		t.Fatalf("plain folder added as git: %+v", proj)
	}

	// A branch in a plain folder is refused with the way out named.
	err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "feat"}, nil)
	if err == nil || !strings.Contains(err.Error(), "conch project git-init") {
		t.Fatalf("worktree in a plain folder: %v", err)
	}

	var got proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectGitInit, proto.ProjectRef{ID: proj.ID}, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Git || got.Base != "main" {
		t.Fatalf("after git_init: %+v", got)
	}
	// Told without waiting for a tick: an event saying git is there.
	waitProject(t, c, func(p proto.ProjectInfo) bool { return p.ID == proj.ID && p.Git })
	out, _ := exec.Command("git", "-C", notes, "status", "--porcelain").Output()
	if strings.TrimSpace(string(out)) != "?? todo.txt" {
		t.Fatalf("the file already there: %q", out)
	}
	if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "feat"}, nil); err != nil {
		t.Fatalf("worktree after git_init: %v", err)
	}

	// Again: it is a repository already.
	err = c.Call(ctx, proto.MethodProjectGitInit, proto.ProjectRef{ID: proj.ID}, nil)
	if err == nil || !strings.Contains(err.Error(), "already a git repository") {
		t.Fatalf("second git_init: %v", err)
	}
	// A project nobody added.
	if err := c.Call(ctx, proto.MethodProjectGitInit, proto.ProjectRef{ID: "rnope"}, nil); err == nil {
		t.Fatal("git_init of an unknown project")
	}
}

// TestProjectGitInitRefuses: a folder that has become part of another
// repository is not made one nested in it, a folder that is gone is said
// to be, and with no author identity the folder is left plain and the
// error says how to set one.
func TestProjectGitInitRefuses(t *testing.T) {
	gitInitIdentity(t)
	c, dir := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	add := func(path string) proto.ProjectInfo {
		t.Helper()
		os.MkdirAll(path, 0o755)
		var p proto.ProjectInfo
		if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: path}, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Inside another repository, made after the folder was added.
	outer := filepath.Join(dir, "outer")
	inner := add(filepath.Join(outer, "inner"))
	git(t, outer, "init", "-q", "-b", "main")
	err := c.Call(ctx, proto.MethodProjectGitInit, proto.ProjectRef{ID: inner.ID}, nil)
	if err == nil || !strings.Contains(err.Error(), "inside the git repository") {
		t.Fatalf("nested: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(inner.Path, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nested repository made: %v", err)
	}

	// Gone.
	gone := add(filepath.Join(dir, "gone"))
	os.RemoveAll(gone.Path)
	if err := c.Call(ctx, proto.MethodProjectGitInit, proto.ProjectRef{ID: gone.ID}, nil); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Fatalf("gone: %v", err)
	}

	// No identity: git refuses to guess one.
	plain := add(filepath.Join(dir, "plain"))
	for _, v := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(v, "")
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.useConfigOnly")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	err = c.Call(ctx, proto.MethodProjectGitInit, proto.ProjectRef{ID: plain.ID}, nil)
	if err == nil || !strings.Contains(err.Error(), "git config --global user.email") {
		t.Fatalf("no identity: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(plain.Path, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".git left behind: %v", err)
	}
	var list proto.ProjectList
	if err := c.Call(ctx, proto.MethodProjectList, nil, &list); err != nil {
		t.Fatal(err)
	}
	for _, p := range list.Projects {
		if p.ID == plain.ID && p.Git {
			t.Fatal("plain folder shown as git after a failed init")
		}
	}
}
