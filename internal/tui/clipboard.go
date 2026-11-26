package tui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// copyText puts text on the clipboard and says how far it got.
func copyText(text string) tea.Cmd {
	return func() tea.Msg {
		if text == "" {
			return nil
		}
		r := putClipboard(text)
		return flashMsg(r.say(fmt.Sprintf("%d characters", utf8.RuneCountInString(text))))
	}
}

// clipOutcome is how far a copy is known to have got.
type clipOutcome int

const (
	clipFailed clipOutcome = iota // nothing took it
	// clipSent: written to the terminal as OSC 52, which answers nothing —
	// Terminal.app and GNOME's ignore it, and others ask to be allowed.
	clipSent
	clipCopied // a clipboard tool said it took it
)

// clipResult is what a copy came to, and what to do instead where conch
// cannot know that it worked.
type clipResult struct {
	outcome clipOutcome
	instead string
}

// say puts a copy of what into words, claiming only what is known.
func (r clipResult) say(what string) string {
	switch r.outcome {
	case clipCopied:
		return "copied " + what
	case clipSent:
		return "sent " + what + " to your terminal · nothing to paste? " + r.instead
	}
	return "could not copy " + what
}

// putClipboard is the copying itself: OSC 52 asks the terminal to do it,
// which also works over SSH, and where the terminal ignores OSC 52 (macOS
// Terminal.app) a local clipboard tool covers it.
//
// It is a variable because it reaches out of the process: the test suite
// replaces it, so running the tests cannot put a fixture on the
// developer's own clipboard — which it used to, through the escape
// sequence alone.
var putClipboard = func(text string) clipResult {
	return writeClipboard(text, runtime.GOOS, os.Getenv, os.Stdout, runClipboardTool)
}

func runClipboardTool(tool []string, text string) error {
	cmd := exec.Command(tool[0], tool[1:]...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// writeClipboard copies with what it is given, so that a test can say what
// each platform does without touching a clipboard.
func writeClipboard(text, goos string, getenv func(string) string, terminal io.Writer, run func(tool []string, text string) error) clipResult {
	osc := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
	if getenv("TMUX") != "" {
		osc = "\x1bPtmux;" + strings.ReplaceAll(osc, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	_, writeErr := io.WriteString(terminal, osc)

	instead := "shift-drag instead"
	if getenv("SSH_TTY") == "" { // the local clipboard is the user's
		tools := clipboardTools(goos, getenv)
		missing := 0
		for _, tool := range tools {
			err := run(tool, text)
			if err == nil {
				return clipResult{outcome: clipCopied}
			}
			if errors.Is(err, exec.ErrNotFound) {
				missing++
			}
		}
		// A desktop with none of the tools: one would settle it for good.
		if goos != "darwin" && len(tools) > 0 && missing == len(tools) {
			instead = "install " + clipboardPackage(tools[0][0])
		}
	}
	if writeErr != nil {
		return clipResult{outcome: clipFailed}
	}
	return clipResult{outcome: clipSent, instead: instead}
}

func clipboardPackage(tool string) string {
	if tool == "wl-copy" {
		return "wl-clipboard"
	}
	return tool
}

func clipboardTools(goos string, getenv func(string) string) [][]string {
	if goos == "darwin" {
		return [][]string{{"pbcopy"}}
	}
	var tools [][]string
	if getenv("WAYLAND_DISPLAY") != "" {
		tools = append(tools, []string{"wl-copy"})
	}
	if getenv("DISPLAY") != "" {
		tools = append(tools, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	}
	return tools
}

// openAndCopy opens a link and copies it, so a browser that does not open
// still leaves the link. One message says both: two would race, and the
// copy's was the one lost.
func openAndCopy(link string) tea.Cmd {
	return func() tea.Msg {
		r := putClipboard(link)
		if err := openInBrowser(link); err != nil {
			return errMsg{fmt.Errorf("%w (%s: %s)", err, r.say("the link"), link)}
		}
		return flashMsg("opened " + ansi.Truncate(link, 60, "…") + " · " + r.say("the link"))
	}
}

// openURL opens a link in the local browser.
func openURL(url string) tea.Cmd {
	return func() tea.Msg {
		if err := openInBrowser(url); err != nil {
			return errMsg{err}
		}
		return flashMsg("opened " + url)
	}
}

// openInBrowser hands a link to the desktop. A variable for the same
// reason as putClipboard: tests must not open windows.
var openInBrowser = func(url string) error {
	tool := "xdg-open"
	if runtime.GOOS == "darwin" {
		tool = "open"
	}
	return exec.Command(tool, url).Start()
}
