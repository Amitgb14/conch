package server_test

import (
	"path/filepath"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// An Antigravity CLI account over the wire: added with the name of the
// variable holding its Gemini key, listed with it, and an agent started
// on it is given its folder and that name — which its launch line reads
// (internal/adapter) — and never a key.
func TestAntigravityAccountOverTheSocket(t *testing.T) {
	c, dir := startServer(t)
	if miss := c.MissingCapabilities([]string{proto.CapAccountKeys}); len(miss) > 0 {
		t.Fatalf("not announced: %v", miss)
	}
	badRequest(t, c.Call(t.Context(), proto.MethodAccountAdd, proto.AccountAddParams{Agent: "antigravity", Name: "work"}, nil), "no key")
	badRequest(t, c.Call(t.Context(), proto.MethodAccountAdd, proto.AccountAddParams{Agent: "antigravity", Name: "work", KeyEnv: "AIza-not-a-name"}, nil), "a key, not its name")
	badRequest(t, c.Call(t.Context(), proto.MethodAccountAdd, proto.AccountAddParams{Agent: "claude", Name: "work", KeyEnv: "KEY"}, nil), "a key for claude")

	var acc proto.AccountInfo
	if err := c.Call(t.Context(), proto.MethodAccountAdd, proto.AccountAddParams{Agent: "antigravity", Name: "work", KeyEnv: "WORK_GEMINI_KEY"}, &acc); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "accounts", "antigravity", "work")
	if acc.Dir != want || acc.KeyEnv != "WORK_GEMINI_KEY" {
		t.Fatalf("%+v", acc)
	}
	listed := false
	for _, a := range accountList(t, c).Accounts {
		if a.Agent == "antigravity" && a.Name == "work" {
			listed = a.KeyEnv == "WORK_GEMINI_KEY" && a.SignedIn == nil
		}
	}
	if !listed {
		t.Errorf("not listed with its key's name: %+v", accountList(t, c).Accounts)
	}

	run := func(cp proto.PaneCreateParams) (proto.PaneInfo, string) {
		out := filepath.Join(t.TempDir(), "env")
		cp.Command = []string{"/bin/sh", "-c", `printf 'dir=[%s] key=[%s]\n' "$CONCH_AGY_DIR" "$CONCH_AGY_KEY_ENV" > "$0"; exec cat`, out}
		cp.Cols, cp.Rows = 80, 10
		var info proto.PaneInfo
		if err := c.Call(t.Context(), proto.MethodPaneCreate, cp, &info); err != nil {
			t.Fatal(err)
		}
		return info, waitRan(t, out, "]\n")
	}
	if info, env := run(proto.PaneCreateParams{Agent: "antigravity", Account: "work", Cwd: dir}); info.Account != "work" || env != "dir=["+want+"] key=[WORK_GEMINI_KEY]\n" {
		t.Errorf("work: %q %q", info.Account, env)
	}
	// Its own account is the Google sign-in it has: nothing set.
	if info, env := run(proto.PaneCreateParams{Agent: "antigravity", Cwd: dir}); info.Account != "" || env != "dir=[] key=[]\n" {
		t.Errorf("own: %q %q", info.Account, env)
	}
	// A pin naming "work" reaches agy's work as it does Claude's.
	if err := c.Call(t.Context(), proto.MethodAccountPin, proto.AccountPinParams{Path: dir, Account: "work"}, nil); err != nil {
		t.Fatal(err)
	}
	if info, env := run(proto.PaneCreateParams{Agent: "antigravity", Cwd: dir}); info.Account != "work" || env != "dir=["+want+"] key=[WORK_GEMINI_KEY]\n" {
		t.Errorf("pinned: %q %q", info.Account, env)
	}
}
