package sessions

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The proof the plan asks for: Claude Code itself resumes a session conch
// wrote, with its history. Skipped without claude on PATH.
//
// It runs claude in a scratch HOME and CLAUDE_CONFIG_DIR with every way of
// signing in left out, so it is never logged in and never calls the model
// or spends anyone's usage: Claude Code loads the session, appends the new
// prompt to it, and only then finds it cannot send it. The prompt's line
// chaining back to the last line conch wrote is what says the history was
// loaded; a session Claude Code could not read says "No conversation
// found" instead.
func TestClaudeResumesWrittenSession(t *testing.T) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("no claude on PATH")
	}
	env, cfg := importEnv(t)
	dir, err := os.MkdirTemp("", "proof")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	dir, _ = filepath.EvalSymlinks(dir) // Claude Code names the folder by the real path
	id, path, err := ClaudeWriter{Env: env}.Write(context.Background(), conductorConversation(dir), dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--resume", id, "-p", "say ok")
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + env.Home, "TMPDIR=" + os.TempDir(),
		"CLAUDE_CONFIG_DIR=" + cfg,
		"DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	}
	out, _ := cmd.CombinedOutput() // not logged in: it exits 1, by design
	if ctx.Err() != nil {
		t.Fatalf("claude did not finish: %s", out)
	}
	if strings.Contains(string(out), "No conversation found") {
		t.Fatalf("Claude Code did not find the session conch wrote:\n%s", out)
	}

	// Every line Claude Code now has, wherever it put them.
	parents := map[string]string{}
	var prompt string
	files, _ := filepath.Glob(filepath.Join(cfg, "projects", "*", "*.jsonl"))
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(nil, 1<<24)
		for sc.Scan() {
			var l struct {
				Type    string  `json:"type"`
				UUID    string  `json:"uuid"`
				Parent  *string `json:"parentUuid"`
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(sc.Bytes(), &l) != nil || l.UUID == "" {
				continue
			}
			if l.Parent != nil {
				parents[l.UUID] = *l.Parent
			}
			if l.Type == "user" && strings.Contains(string(l.Message.Content), "say ok") {
				prompt = l.UUID
			}
		}
		fh.Close()
	}
	if prompt == "" {
		t.Fatalf("Claude Code did not take the prompt into the session (%v):\n%s", files, out)
	}
	at, steps := prompt, 0
	for at != "u10" && steps < 100 {
		p, ok := parents[at]
		if !ok {
			t.Fatalf("the new prompt does not follow the history conch wrote: the chain stops at %s\n%s", at, out)
		}
		at, steps = p, steps+1
	}
	if at != "u10" {
		t.Fatal("no way back from the new prompt to the last line conch wrote")
	}
	t.Logf("claude %s resumed %s; it said: %s", strings.TrimSpace(version(bin)), path, strings.TrimSpace(string(out)))
}

func version(bin string) string {
	out, _ := exec.Command(bin, "--version").Output()
	return string(out)
}
