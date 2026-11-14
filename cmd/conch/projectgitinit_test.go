package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// conch project git-init, by path and by ID, against a real server: a plain
// folder becomes a repository; one already is, or no project at all, is
// said rather than done.
func TestProjectGitInitCommand(t *testing.T) {
	dir := a4Env(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, v := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(v, "T")
	}
	for _, v := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(v, "t@example.com")
	}
	startMoveServer(t, config.SocketPath(), filepath.Join(dir, "state"))
	c, err := client.Dial(config.SocketPath(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	add := func(name string) proto.ProjectInfo {
		path := filepath.Join(dir, name)
		os.MkdirAll(path, 0o755)
		var p proto.ProjectInfo
		if err := call(c, proto.MethodProjectAdd, proto.ProjectAddParams{Path: path}, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	byPath, byID := add("notes"), add("draft")

	if err := runProject([]string{"git-init"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("no argument: %v", err)
	}
	if err := runProject([]string{"git-init", byPath.Path}); err != nil {
		t.Fatal(err)
	}
	if err := runProject([]string{"git-init", byID.ID}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []proto.ProjectInfo{byPath, byID} {
		if _, err := os.Stat(filepath.Join(p.Path, ".git")); err != nil {
			t.Fatalf("%s: %v", p.Name, err)
		}
	}
	if err := runProject([]string{"git-init", byID.ID}); err == nil || !strings.Contains(err.Error(), "already a git repository") {
		t.Fatalf("again: %v", err)
	}
	if err := runProject([]string{"git-init", filepath.Join(dir, "nowhere")}); err == nil || !strings.Contains(err.Error(), "no project") {
		t.Fatalf("no project: %v", err)
	}
}
