package tui

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

func TestNotifyCommandGetsTheAlert(t *testing.T) {
	out := filepath.Join(t.TempDir(), "alert")
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	cmd := notifyCommand(context.Background(), `cat > "`+out+`"; printf '%s|%s' "$CONCH_NOTIFY_TITLE" "$CONCH_NOTIFY_BODY" >> "`+out+`"`,
		"claude is waiting", `needs "permission"`, at)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	line, env, _ := strings.Cut(string(b), "\n")
	var got struct {
		Title, Body string
		At          time.Time
	}
	if err := json.Unmarshal([]byte(line), &got); err != nil || got.Title != "claude is waiting" || got.Body != `needs "permission"` || !got.At.Equal(at) {
		t.Fatalf("stdin %q: %+v %v", line, got, err)
	}
	if env != `claude is waiting|needs "permission"` {
		t.Fatalf("env %q", env)
	}
	// A command that hangs is stopped when its time is up.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_ = notifyCommand(ctx, "sleep 30", "t", "b", at).Run()
	if time.Since(start) > 5*time.Second {
		t.Fatal("a hanging command held the alert")
	}
}

func TestNotifyRunsTheCommand(t *testing.T) {
	var ran []*exec.Cmd
	old := runOutward
	runOutward = func(cmd *exec.Cmd) { ran = append(ran, cmd) }
	t.Cleanup(func() { runOutward = old })

	cfg := config.NotifyCfg{Enabled: true, Command: "curl -d @- ntfy.sh/x"}
	cmd := notify(cfg, "title", "body")
	if cmd == nil {
		t.Fatal("a command alone is enough to notify")
	}
	cmd()
	if len(ran) != 1 || !strings.Contains(strings.Join(ran[0].Args, " "), "ntfy.sh/x") {
		t.Fatalf("ran %v", ran)
	}
	// Off is off, and blank is nothing.
	if notify(config.NotifyCfg{Enabled: false, Command: "x"}, "t", "b") != nil {
		t.Fatal("notified while disabled")
	}
	if notify(config.NotifyCfg{Enabled: true, Command: "   "}, "t", "b") != nil {
		t.Fatal("a blank command is a command")
	}
}
