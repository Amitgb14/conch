package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTildifyThroughSymlinkedHome(t *testing.T) {
	real, _ := filepath.EvalSymlinks(t.TempDir())
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	m := Model{}
	for in, want := range map[string]string{
		link:                           "~",
		filepath.Join(link, "src/api"): "~/src/api",
		real:                           "~",
		filepath.Join(real, "src/api"): "~/src/api",
		real + "x/other":               real + "x/other", // a sibling that only shares the prefix
		"/elsewhere":                   "/elsewhere",
	} {
		if got := m.tildify(localMachine, in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
	// A remote machine's home is used as the server reports it.
	box := newMachine("box", "box", "x")
	box.server.Home = "/home/dev"
	m.machines = []*machine{box}
	if m.tildify("box", "/home/dev/src") != "~/src" || m.tildify("box", "/home/devx") != "/home/devx" || m.tildify("nope", "/a") != "/a" {
		t.Fatal("remote")
	}
}
