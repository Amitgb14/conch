// Fake `claude` for website screenshots: draws a Claude Code-like screen,
// reports hooks and token usage to conch, and never talks to any API.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	c  = "\x1b[38;2;215;119;87m"
	g  = "\x1b[32m"
	d  = "\x1b[2m"
	b  = "\x1b[1m"
	x  = "\x1b[0m"
	bl = "\x1b[38;2;177;185;249m"
)

func hook(event, transcript string) {
	in := fmt.Sprintf(`{"hook_event_name":%q,"session_id":"s-%s","transcript_path":%q}`, event, os.Getenv("CONCH_PANE_ID"), transcript)
	cmd := exec.Command("conch", "report", "claude-hook")
	cmd.Stdin = strings.NewReader(in)
	_ = cmd.Run()
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("2.1.4 (Claude Code)")
		return
	}
	prompt := os.Args[len(os.Args)-1]
	dir := filepath.Join(os.Getenv("HOME"), "transcripts")
	os.MkdirAll(dir, 0o755)
	tr := filepath.Join(dir, os.Getenv("CONCH_PANE_ID")+".jsonl")
	usage := func(in, cw, cr, out int, cost float64) {
		os.WriteFile(tr, []byte(fmt.Sprintf(`{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-5","usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`+"\n"+`{"type":"cost-state","totalCostUSD":%.2f}`+"\n", in, cw, cr, out, cost)), 0o644)
	}
	p := func(s string) { fmt.Print(strings.ReplaceAll(s, "\n", "\r\n")) }

	switch {
	case strings.Contains(prompt, "flaky"):
		p("\x1b]0;✳ Fix flaky login test\a\x1b[2J\x1b[H")
		usage(2400, 8100, 27500, 6100, 0.35)
		hook("UserPromptSubmit", tr)
		p(d + "> " + x + prompt + "\n\n" +
			c + "⏺" + x + " The login test races the session cleanup goroutine: the\n  sweeper can delete the session between " + b + "Login" + x + " and " + b + "Me" + x + ".\n\n" +
			c + "⏺" + x + " " + b + "Read" + x + "(auth/session_test.go)\n  ⎿  Read 84 lines\n\n" +
			c + "⏺" + x + " Let me run it 50 times to confirm it fails intermittently.\n\n" +
			"╭──────────────────────────────────────────────────────────────╮\n" +
			"│ " + b + "Bash command" + x + "                                                 │\n" +
			"│                                                              │\n" +
			"│   go test -run TestLogin -count=50 ./auth/...                │\n" +
			"│   Run the login test 50 times to reproduce the race          │\n" +
			"│                                                              │\n" +
			"│ Do you want to proceed?                                      │\n" +
			"│ " + bl + "❯ 1. Yes" + x + "                                                     │\n" +
			"│   2. Yes, and don't ask again for go test commands           │\n" +
			"│   3. No, and tell Claude what to do differently (esc)        │\n" +
			"╰──────────────────────────────────────────────────────────────╯\n")
		hook("PermissionRequest", tr)
		for {
			time.Sleep(time.Hour)
		}
	default:
		p("\x1b]0;✳ Add health check\a\x1b[2J\x1b[H")
		usage(3100, 9800, 32400, 12200, 0.42)
		hook("UserPromptSubmit", tr)
		p(d + "> " + x + prompt + "\n\n" +
			c + "⏺" + x + " I'll add a " + b + "/healthz" + x + " endpoint that pings the database and\n  returns the build version.\n\n" +
			c + "⏺" + x + " " + b + "Update" + x + "(internal/http/routes.go)\n  ⎿  Updated internal/http/routes.go with 2 additions\n" +
			"       41      r.Get(\"/v1/users\", s.listUsers)\n" +
			"       42 " + g + "+    r.Get(\"/healthz\", s.health)" + x + "\n" +
			"       43 " + g + "+    r.Get(\"/readyz\", s.ready)" + x + "\n\n" +
			c + "⏺" + x + " " + b + "Write" + x + "(internal/http/health.go)\n  ⎿  Wrote 38 lines to internal/http/health.go\n\n" +
			c + "⏺" + x + " " + b + "Bash" + x + "(go test ./internal/http/...)\n  ⎿  ok    github.com/acme/api/internal/http   0.412s\n\n")
		hook("PreToolUse", tr)
		spin := []string{"✻", "✶", "✳", "✢", "·", "✢", "✳", "✶"}
		for i := 0; ; i++ {
			p(fmt.Sprintf("\r%s%s%s Writing the handler test… %s(%ds · ↓ 1.2k tokens · esc to interrupt)%s", c, spin[i%len(spin)], x, d, 38+i/2, x))
			time.Sleep(500 * time.Millisecond)
		}
	}
}
