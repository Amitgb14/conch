package server_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func controlFree(t *testing.T, what, s string) {
	t.Helper()
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("%s %q holds %U", what, s, r)
			return
		}
	}
}

// What a program or an agent names itself is drawn by every client on
// somebody's terminal: the server gives it out without control
// characters, whatever was set.
func TestPaneTextComesOutPrintable(t *testing.T) {
	a5Env(t)
	c, dir := startServer(t)
	var info proto.PaneInfo
	// The title, set by the program: a C1 CSI and an ESC inside it.
	err := c.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{
		Name: "made\x1b]52;c;aGk=\x07\nhere", Cwd: dir, Cols: 80, Rows: 10,
		Command: []string{"/bin/sh", "-c", `printf '\033]2;ti\302\233tle\033\\'; printf 'marker-up\n'; exec sleep 30`}}, &info)
	if err != nil {
		t.Fatal(err)
	}
	controlFree(t, "name at create", info.Name)
	deadline := time.Now().Add(10 * time.Second)
	for {
		var list proto.PaneList
		c.Call(t.Context(), proto.MethodPaneList, nil, &list)
		var p proto.PaneInfo
		for _, q := range list.Panes {
			if q.ID == info.ID {
				p = q
			}
		}
		if strings.Contains(p.Title, "ti") {
			controlFree(t, "title", p.Title)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the title never came: %+v", p)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var renamed proto.PaneInfo
	if err := c.Call(t.Context(), proto.MethodPaneRename, proto.PaneRenameParams{ID: info.ID, Name: "re\x1b]2;x\x07\u009bnamed"}, &renamed); err != nil {
		t.Fatal(err)
	}
	controlFree(t, "name at rename", renamed.Name)
	if !strings.HasPrefix(renamed.Name, "re") || !strings.HasSuffix(renamed.Name, "named") {
		t.Fatalf("renamed %q", renamed.Name)
	}
}
