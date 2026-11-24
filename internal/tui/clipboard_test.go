package tui

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

type brokenTerminal struct{}

func (brokenTerminal) Write([]byte) (int, error) { return 0, errors.New("closed") }

// conch says copied only when something said it took the text; OSC 52
// answers nothing, and was reported as a copy all the same.
func TestCopyClaimsOnlyWhatItKnows(t *testing.T) {
	missing := fmt.Errorf("start: %w", exec.ErrNotFound)
	failed := errors.New("exit status 1")
	for _, c := range []struct {
		name, goos string
		env        map[string]string
		tools      map[string]error // by tool; one not here took it
		want       clipOutcome
		instead    string
		ran        string
	}{
		{"mac", "darwin", nil, nil, clipCopied, "", "pbcopy"},
		{"mac, pbcopy failing", "darwin", nil, map[string]error{"pbcopy": failed}, clipSent, "shift-drag instead", "pbcopy"},
		{"mac, pbcopy missing", "darwin", nil, map[string]error{"pbcopy": missing}, clipSent, "shift-drag instead", "pbcopy"},
		{"mac over ssh", "darwin", map[string]string{"SSH_TTY": "/dev/ttys1"}, nil, clipSent, "shift-drag instead", ""},
		{"headless linux", "linux", nil, nil, clipSent, "shift-drag instead", ""},
		{"linux over ssh with a display", "linux", map[string]string{"SSH_TTY": "/dev/pts/1", "DISPLAY": ":10"}, nil, clipSent, "shift-drag instead", ""},
		{"wayland", "linux", map[string]string{"WAYLAND_DISPLAY": "w0"}, nil, clipCopied, "", "wl-copy"},
		{"wayland, no tool", "linux", map[string]string{"WAYLAND_DISPLAY": "w0"}, map[string]error{"wl-copy": missing}, clipSent, "install wl-clipboard", "wl-copy"},
		{"x11", "linux", map[string]string{"DISPLAY": ":0"}, nil, clipCopied, "", "xclip"},
		{"x11, only xsel", "linux", map[string]string{"DISPLAY": ":0"}, map[string]error{"xclip": missing}, clipCopied, "", "xclip xsel"},
		{"x11, neither", "linux", map[string]string{"DISPLAY": ":0"}, map[string]error{"xclip": missing, "xsel": missing}, clipSent, "install xclip", "xclip xsel"},
		{"x11, installed but failing", "linux", map[string]string{"DISPLAY": ":0"}, map[string]error{"xclip": failed, "xsel": missing}, clipSent, "shift-drag instead", "xclip xsel"},
		{"both displays, falls to x11", "linux", map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "w0"}, map[string]error{"wl-copy": failed}, clipCopied, "", "wl-copy xclip"},
	} {
		var out bytes.Buffer
		var ran []string
		got := writeClipboard("héllo", c.goos, func(k string) string { return c.env[k] }, &out,
			func(tool []string, text string) error {
				if text != "héllo" {
					t.Errorf("%s: %s was given %q", c.name, tool[0], text)
				}
				ran = append(ran, tool[0])
				return c.tools[tool[0]]
			})
		if got != (clipResult{outcome: c.want, instead: c.instead}) {
			t.Errorf("%s: got %+v", c.name, got)
		}
		if strings.Join(ran, " ") != c.ran {
			t.Errorf("%s: ran %v", c.name, ran)
		}
		if out.String() != "\x1b]52;c;aMOpbGxv\a" {
			t.Errorf("%s: the terminal was sent %q", c.name, out.String())
		}
	}
}

func TestCopyWithATerminalThatCannotBeWrittenTo(t *testing.T) {
	none := func(string) string { return "" }
	ok := func([]string, string) error { return nil }
	if got := writeClipboard("x", "linux", none, brokenTerminal{}, ok); got.outcome != clipFailed {
		t.Errorf("nothing took it: %+v", got)
	}
	if got := writeClipboard("x", "darwin", none, brokenTerminal{}, ok); got.outcome != clipCopied {
		t.Errorf("pbcopy took it: %+v", got)
	}
}

func TestCopyThroughTmux(t *testing.T) {
	var out bytes.Buffer
	env := func(k string) string {
		if k == "TMUX" {
			return "/tmp/tmux"
		}
		return ""
	}
	if got := writeClipboard("hi", "linux", env, &out, nil); got.outcome != clipSent {
		t.Errorf("tmux answers nothing either: %+v", got)
	}
	if out.String() != "\x1bPtmux;\x1b\x1b]52;c;aGk=\a\x1b\\" {
		t.Errorf("tmux was sent %q", out.String())
	}
}

// Every place that copies passes on what is known, none saying copied for
// a text that was only sent.
func TestEveryCopySaysWhatHappened(t *testing.T) {
	a2Isolate(t)
	oldCopy, oldOpen := putClipboard, openInBrowser
	t.Cleanup(func() { putClipboard, openInBrowser = oldCopy, oldOpen })
	openInBrowser = func(string) error { return nil }

	for _, c := range []struct {
		r    clipResult
		want string
	}{
		{clipResult{outcome: clipSent, instead: "shift-drag instead"}, "sent "},
		{clipResult{outcome: clipFailed}, "could not copy "},
	} {
		putClipboard = func(string) clipResult { return c.r }
		check := func(where, said string) {
			if !strings.Contains(said, c.want) || strings.Contains(said, "copied") {
				t.Errorf("%s said %q, want %q", where, said, c.want)
			}
		}
		said := func(msg any) string { s, _ := msg.(flashMsg); return string(s) }

		check("copyText", said(copyText("some text")()))
		check("copyPaneRef", said(copyPaneRef("p3")()))
		check("openAndCopy", said(openAndCopy("https://example.com/x")()))

		m := a2Model()
		if cmd := m.receivePreview(previewDoneMsg{url: "https://example.com/p"}); cmd == nil {
			t.Fatal("no preview command")
		} else {
			check("a preview", said(cmd()))
		}
		m.receivePreview(previewDoneMsg{url: "https://example.com/p", err: errors.New("nothing answers")})
		check("a preview that failed", m.flash)
		if strings.Contains(m.flash, "on your clipboard") {
			t.Errorf("a preview that failed claimed the clipboard: %q", m.flash)
		}
	}

	openInBrowser = func(string) error { return errors.New("no browser") }
	putClipboard = func(string) clipResult { return clipResult{outcome: clipSent, instead: "shift-drag instead"} }
	msg, ok := openAndCopy("https://example.com/x")().(errMsg)
	if !ok || !strings.Contains(msg.err.Error(), "https://example.com/x") || !strings.Contains(msg.err.Error(), "sent the link") {
		t.Errorf("a browser that failed: %#v", msg)
	}
}

func TestSentIsWhatANarrowBarKeeps(t *testing.T) {
	said := clipResult{outcome: clipSent, instead: "shift-drag instead"}.say("420 characters")
	if got := ansi.Truncate(said, 20, "…"); !strings.HasPrefix(got, "sent 420") {
		t.Errorf("cut to 20 it reads %q", got)
	}
}
