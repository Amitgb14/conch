package tui

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// A provider conch does not know is adapted in a terminal of its own,
// running the command that does the whole of it.
func TestAdaptDialogRunsTheCommand(t *testing.T) {
	m, peer := a1Fixture(t, true)
	mu := newSandboxMenu(nil)
	mu.items[len(mu.items)-1].run(m)
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(d.title, "Add a sandbox provider") || !strings.Contains(strings.Join(d.text, " "), "never given to the agent") {
		t.Fatalf("dialog %#v", m.overlay)
	}
	if cmd := d.submit(m, []string{"  ", "claude", ""}); cmd != nil || !strings.Contains(m.flash, "URL") {
		t.Fatalf("no url: %q", m.flash)
	}
	msgs := a2Run(d.submit(m, []string{"https://api.acme.dev/x?a=1&b='2'", "codex", "acme"}))
	if len(msgs) != 1 {
		t.Fatalf("submit: %#v", msgs)
	}
	if _, ok := msgs[0].(createdMsg); !ok {
		t.Fatalf("submit: %#v", msgs)
	}
	var p proto.PaneCreateParams
	json.Unmarshal(peer.waitMethod(t, proto.MethodPaneCreate, "").Params, &p)
	if !p.NoProject || p.Name != "adapt a provider" || len(p.Command) != 3 {
		t.Fatalf("pane %+v", p)
	}
	script := p.Command[2]
	for _, want := range []string{"sandbox adapt -agent codex -name acme 'https://api.acme.dev/x?a=1&b=", "read _"} {
		if !strings.Contains(script, want) {
			t.Errorf("want %q in %q", want, script)
		}
	}
	// Whatever is typed reaches the command as one argument each, never as
	// shell: echo stands in for conch.
	cmd := adaptScript("/bin/echo", "u v;touch x $(id)", "", " ")
	run := exec.Command(cmd[0], cmd[1:]...)
	run.Dir = t.TempDir()
	out, _ := run.Output() // read meets the end of stdin and fails: that is fine here
	if !strings.HasPrefix(string(out), "sandbox adapt u v;touch x $(id)\n\n[enter closes this]") {
		t.Fatalf("quoting: %q", out)
	}
}

// The Sandboxes settings offer it too.
func TestAdaptFromSettings(t *testing.T) {
	m, _ := a1Fixture(t, true)
	s := &settings{}
	var found *settingItem
	for i, it := range s.sandboxItems(m) {
		if strings.Contains(it.label, "Add a provider") {
			found = &s.sandboxItems(m)[i]
		}
	}
	if found == nil {
		t.Fatal("no Add a provider in Sandboxes")
	}
	found.run(m)
	if d, ok := m.overlay.(*dialog); !ok || !strings.Contains(d.title, "Add a sandbox provider") {
		t.Fatalf("settings item opened %#v", m.overlay)
	}
}
