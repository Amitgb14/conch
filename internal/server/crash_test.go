package server_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// The server ended on 2026-11-19 on one pane's output: a scroll region
// past the bottom of a screen that had shrunk to 26 rows, then a scroll
// inside it, and the emulator indexed row 30. The same output now leaves
// the server answering, and the pane drawing.
func TestScrollRegionPastTheScreenLeavesTheServerUp(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	c, dir := startServer(t)
	script := `printf 'top\033[1;30r\033[T\033[S\033[5H\033[3L\033[3M\033[1;40r\033[M\033[r\n'; echo Survived-Mark; sleep 30`
	var info proto.PaneInfo
	if err := call(t, c, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", script}, Cwd: dir, NoProject: true, Rows: 26, Cols: 80}, &info); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var r proto.PaneReadResult
		if call(t, c, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &r) == nil && strings.Contains(strings.Join(r.Lines, "\n"), "Survived-Mark") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never printed its marker: %q", r.Lines)
		}
	}
	var list proto.PaneList
	if err := call(t, c, proto.MethodPaneList, nil, &list); err != nil {
		t.Fatalf("the server stopped answering: %v", err)
	}
}
