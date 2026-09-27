package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// The case this exists for: Claude Code asks you to open a login URL, and
// draws it inside its own box, wrapped over three rows. Selecting it with
// the mouse brings the border and the line breaks along — "Unknown scope:
// user:sessio│ns:claude_code" — so conch puts it back together instead.
func TestPaneLinksAcrossABox(t *testing.T) {
	url := "https://claude.ai/oauth/authorize?code=true&client_id=9d1c250a&scope=user%3Aprofile+user%3Asessions%3Aclaude_code&state=85A7wEYR91"
	screen := claudeBox(t, 45, "Browser didn't open? Use the url below to sign in:", "", url)
	screen = append(screen, "", "Paste code here if prompted >")
	links := paneLinks(screen)
	if len(links) != 1 || links[0] != url {
		t.Fatalf("links %q\nwant %q\nscreen:\n%s", links, url, strings.Join(screen, "\n"))
	}
	// Nothing of the box is left in it.
	for _, bad := range []string{"│", " ", "\n"} {
		if strings.Contains(links[0], bad) {
			t.Fatalf("the link kept %q: %s", bad, links[0])
		}
	}
}

// claudeBox draws text in a box inner cells wide, wrapping as an agent's
// own interface does: on a space where it can, mid-word where it cannot.
func claudeBox(t *testing.T, inner int, paras ...string) []string {
	t.Helper()
	out := []string{"╭" + strings.Repeat("─", inner+2) + "╮"}
	add := func(s string) {
		out = append(out, "│ "+s+strings.Repeat(" ", inner-len([]rune(s)))+" │")
	}
	for _, p := range paras {
		switch {
		case p == "":
			add("")
			continue
		}
		for len(p) > inner {
			cut := inner
			if i := strings.LastIndex(p[:inner+1], " "); i > 0 && strings.Contains(p, " ") {
				cut = i
			}
			add(strings.TrimRight(p[:cut], " "))
			p = strings.TrimLeft(p[cut:], " ")
		}
		add(p)
	}
	out = append(out, "╰"+strings.Repeat("─", inner+2)+"╯")
	for _, l := range out {
		if len([]rune(l)) != inner+4 {
			t.Fatalf("the box is ragged (%d, want %d): %q", len([]rune(l)), inner+4, l)
		}
	}
	return out
}

func TestPaneLinks(t *testing.T) {
	for _, c := range []struct {
		name  string
		lines []string
		want  []string
	}{
		{"one on a line", []string{"open https://example.com/x now"}, []string{"https://example.com/x"}},
		{"trailing prose", []string{"see https://example.com/x."}, []string{"https://example.com/x"}},
		{"two on a line", []string{"http://a.test/1 and https://b.test/2"}, []string{"http://a.test/1", "https://b.test/2"}},
		{"the same one twice", []string{"https://a.test/1", "https://a.test/1"}, []string{"https://a.test/1"}},
		{"none", []string{"nothing here", "ftp://a.test/x"}, nil},
		{"scheme alone", []string{"https://"}, nil},
		{"a wrap that is prose, not a URL", []string{
			"https://example.com/start",
			" and then some prose",
		}, []string{"https://example.com/start"}},
		{"a wrap onto a blank line stops", []string{"https://example.com/abc", ""}, []string{"https://example.com/abc"}},
		{"ends at a border on the next row", []string{"https://example.com/abc", "│ not part of it"}, []string{"https://example.com/abc"}},
		{"three rows of it", []string{"https://a.test/one", "two-and-more-xxxxy", "three four"}, []string{"https://a.test/onetwo-and-more-xxxxythree"}},
		{"an ansi-coloured link", []string{"\x1b[34mhttps://example.com/x\x1b[0m here"}, []string{"https://example.com/x"}},
	} {
		got := paneLinks(c.lines)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// A screen full of them offers as many as it says and no more.
	var many []string
	for i := 0; i < linksMax+5; i++ {
		many = append(many, "https://example.com/"+strings.Repeat("a", i+1)+" x")
	}
	if got := paneLinks(many); len(got) != linksMax {
		t.Fatalf("a screen of links gave %d", len(got))
	}
	// Nothing at all, and lines of nothing.
	if got := paneLinks(nil); got != nil {
		t.Fatalf("no lines: %q", got)
	}
	if got := paneLinks([]string{"", "   ", "│ │"}); got != nil {
		t.Fatalf("empty lines: %q", got)
	}
}

// ctrl+b u offers what it found, opens it and copies it; with nothing to
// offer it says so rather than opening an empty menu.
func TestOpenLinksKey(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.rows = []row{{id: "pane:p1", kind: kindPane, machine: localMachine, paneID: "p1"}}
	m.cursor = "pane:p1"

	// Nothing open: it says which key to press once it is.
	if cmd := m.openLinks(m.rows[0]); cmd != nil || !strings.Contains(m.flash, "open the pane first") {
		t.Fatalf("no frame: %q", m.flash)
	}
	m.viewing = "p1"
	m.frame = &proto.Frame{Lines: []string{"no links at all"}}
	if cmd := m.openLinks(m.rows[0]); cmd != nil || !strings.Contains(m.flash, "no links on this pane's screen") {
		t.Fatalf("no links: %q", m.flash)
	}
	m.frame = &proto.Frame{Lines: []string{
		"│ https://claude.ai/oauth/authorize?code=true&sco │",
		"│ pe=user%3Asessions%3Aclaude_code                │",
		"and https://docs.example/help too",
	}}
	if cmd := m.openLinks(m.rows[0]); cmd != nil {
		t.Fatal("it should only open the menu")
	}
	mu, ok := m.overlay.(*menu)
	if !ok || mu.title != "Links on screen" || len(mu.items) != 2 {
		t.Fatalf("menu: %#v", m.overlay)
	}
	// A long link is shortened for the row, but what opens is the whole of it.
	if got := a2MenuLabels(mu); !strings.Contains(got, "1 https://claude.ai/oauth/authorize?code=true&scope=user%3Asessions%3Acla…") ||
		!strings.Contains(got, "2 https://docs.example/help") {
		t.Fatalf("labels %q", got)
	}
	// Choosing one copies it and opens it; the commands are returned, not run.
	cmd := mu.items[0].run(m)
	if cmd == nil {
		t.Fatal("choosing a link did nothing")
	}
	// The key reaches it through the prefix, with the pane in front of you.
	m.overlay = nil
	m.focus = focusMain
	m.prefixArmed = true
	next, _ := m.handleMainKey(a2Key("u"))
	mm := next.(Model)
	if _, ok := mm.overlay.(*menu); !ok {
		t.Fatalf("ctrl+b u gave %#v (flash %q)", mm.overlay, mm.flash)
	}
	// And from the pane's own menu, which is where the mouse finds it.
	mu = newRowMenu(mm, mm.rows[0], 0, 0)
	if got := a2MenuLabels(mu); !strings.Contains(got, "Open a link it printed ("+mm.cfg.Keys.Prefix+" u)") {
		t.Fatalf("the pane menu: %q", got)
	}
}

// The URL from the report that started this: Claude Code's login link,
// wrapped inside its box over four rows of a 60-cell pane.
func TestPaneLinksTheRealLoginURL(t *testing.T) {
	url := "https://claude.ai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&response_type=code" +
		"&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback&scope=org%3Acreate_api_key+user%3Aprofile" +
		"+user%3Ainference+user%3Asessions%3Aclaude_code+user%3Amcp_servers&code_challenge=Jmh_oDidZdMQJj1fazVxru0UepcsUIo5D0bv7vYsvvQ" +
		"&code_challenge_method=S256&state=85A7wEYR91ChXaRs6-lCWIVY-8ASt1CWOQX4hFuGhHU"
	for _, inner := range []int{40, 56, 72, 100} {
		screen := claudeBox(t, inner, "Browser didn't open? Use the url below to sign in:", "", url)
		got := paneLinks(screen)
		if len(got) != 1 || got[0] != url {
			t.Fatalf("%d cells wide: got %q", inner, got)
		}
	}
	// And the scope that came back broken is whole again.
	screen := claudeBox(t, 45, url)
	if got := paneLinks(screen); !strings.Contains(got[0], "user%3Asessions%3Aclaude_code") {
		t.Fatalf("the scope is still broken: %q", got)
	}
}

// Copying and opening reach out of the process, so the suite replaces
// them — and this is the test that says what they are given. It also
// stands guard: if the seams are ever removed, this fails rather than
// quietly putting a fixture on the developer's clipboard.
func TestCopyingAndOpeningGoThroughTheSeams(t *testing.T) {
	a2Isolate(t)
	lastClipboard, lastOpened = "", ""
	if msg := copyText("a line to copy")(); msg != flashMsg("copied 14 characters") {
		t.Fatalf("copy said %v", msg)
	}
	if lastClipboard != "a line to copy" {
		t.Fatalf("the clipboard got %q", lastClipboard)
	}
	if msg := copyText("")(); msg != nil {
		t.Fatalf("copying nothing said %v", msg)
	}
	if msg := openURL("https://example.com/x")(); msg != flashMsg("opened https://example.com/x") {
		t.Fatalf("open said %v", msg)
	}
	if lastOpened != "https://example.com/x" {
		t.Fatalf("the browser got %q", lastOpened)
	}
	// A browser that will not start is reported, not swallowed.
	old := openInBrowser
	openInBrowser = func(string) error { return errors.New("no browser") }
	t.Cleanup(func() { openInBrowser = old })
	if _, ok := openURL("https://example.com/x")().(errMsg); !ok {
		t.Fatal("a browser that failed said nothing")
	}
	// Choosing a link from the menu does both.
	lastClipboard, lastOpened = "", ""
	openInBrowser = func(url string) error { lastOpened = url; return nil }
	m := a2Model()
	m.rows = []row{{id: "pane:p1", kind: kindPane, machine: localMachine, paneID: "p1"}}
	m.cursor, m.viewing = "pane:p1", "p1"
	m.frame = &proto.Frame{Lines: []string{"open https://example.com/login?code=1 to sign in"}}
	m.openLinks(m.rows[0])
	mu := m.overlay.(*menu)
	a2Run(mu.items[0].run(m))
	if lastOpened != "https://example.com/login?code=1" || lastClipboard != lastOpened {
		t.Fatalf("opened %q, copied %q", lastOpened, lastClipboard)
	}
}
