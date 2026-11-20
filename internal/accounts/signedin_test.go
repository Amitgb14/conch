package accounts

import (
	"os"
	"path/filepath"
	"testing"
)

func noEnv(string) string { return "" }

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSignedInClaude(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "accounts", "claude", "work")
	check := func(name, dir string, wantIn, wantKnown bool) {
		t.Helper()
		in, known := SignedIn("claude", dir, home, noEnv)
		if in != wantIn || known != wantKnown {
			t.Errorf("%s: signed in %v known %v, want %v %v", name, in, known, wantIn, wantKnown)
		}
	}
	// Nothing there yet: a folder conch made and nobody signed in to.
	check("no file", work, false, true)
	check("own, no file", "", false, true)
	// Started once without logging in: the file, but no account.
	write(t, filepath.Join(work, ".claude.json"), `{"numStartups":1,"projects":{}}`)
	check("no account", work, false, true)
	write(t, filepath.Join(work, ".claude.json"), `{"oauthAccount":null}`)
	check("null account", work, false, true)
	write(t, filepath.Join(work, ".claude.json"), `{"oauthAccount":{"accountUuid":""}}`)
	check("empty account", work, false, true)
	// Logged in.
	write(t, filepath.Join(work, ".claude.json"), `{"projects":{"/x":{}},"oauthAccount":{"accountUuid":"u-1","emailAddress":"a@b"}}`)
	check("signed in", work, true, true)
	// The agent's own: ~/.claude.json, beside ~/.claude, not in it.
	write(t, filepath.Join(home, ".claude", ".claude.json"), `{"oauthAccount":{"accountUuid":"wrong-place"}}`)
	check("own, file only inside ~/.claude", "", false, true)
	write(t, filepath.Join(home, ".claude.json"), `{"oauthAccount":{"accountUuid":"u-2"}}`)
	check("own signed in", "", true, true)
	// A file being written, or not JSON: conch cannot say.
	write(t, filepath.Join(work, ".claude.json"), `{"oauthAccount":{"accountUu`)
	check("truncated", work, false, false)
	write(t, filepath.Join(work, ".claude.json"), ``)
	check("empty file", work, false, false)
	// A folder where the file should be.
	if err := os.Remove(filepath.Join(work, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(work, ".claude.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	check("a directory", work, false, false)
	// No home to look in.
	if in, known := SignedIn("claude", "", "", noEnv); in || known {
		t.Errorf("no home: %v %v", in, known)
	}
}

func TestSignedInCodex(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "w")
	if in, known := SignedIn("codex", work, home, noEnv); in || !known {
		t.Errorf("no auth.json: %v %v", in, known)
	}
	write(t, filepath.Join(work, "auth.json"), ``)
	if in, known := SignedIn("codex", work, home, noEnv); in || !known {
		t.Errorf("empty auth.json: %v %v", in, known)
	}
	write(t, filepath.Join(work, "auth.json"), `{"tokens":{}}`)
	if in, known := SignedIn("codex", work, home, noEnv); !in || !known {
		t.Errorf("auth.json: %v %v", in, known)
	}
	// Its own account: ~/.codex, or where CODEX_HOME puts it.
	if in, _ := SignedIn("codex", "", home, noEnv); in {
		t.Error("own: signed in with no ~/.codex/auth.json")
	}
	write(t, filepath.Join(home, ".codex", "auth.json"), `{}`)
	if in, known := SignedIn("codex", "", home, noEnv); !in || !known {
		t.Errorf("own: %v %v", in, known)
	}
	moved := filepath.Join(home, "elsewhere")
	env := func(k string) string {
		if k == "CODEX_HOME" {
			return moved
		}
		return ""
	}
	if in, _ := SignedIn("codex", "", home, env); in {
		t.Error("CODEX_HOME: read ~/.codex instead")
	}
	write(t, filepath.Join(moved, "auth.json"), `{}`)
	if in, _ := SignedIn("codex", "", home, env); !in {
		t.Error("CODEX_HOME: not read")
	}
	// An agent that keeps one account: conch does not know.
	if in, known := SignedIn("gemini", work, home, noEnv); in || known {
		t.Errorf("gemini: %v %v", in, known)
	}
}
