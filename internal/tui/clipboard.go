package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// copyText puts text on the clipboard.
func copyText(text string) tea.Cmd {
	return func() tea.Msg {
		if text == "" {
			return nil
		}
		putClipboard(text)
		return flashMsg(fmt.Sprintf("copied %d characters", utf8.RuneCountInString(text)))
	}
}

// putClipboard is the copying itself: OSC 52 asks the terminal to do it,
// which also works over SSH, and where the terminal ignores OSC 52 (macOS
// Terminal.app) a local clipboard tool covers it.
//
// It is a variable because it reaches out of the process: the test suite
// replaces it, so running the tests cannot put a fixture on the
// developer's own clipboard — which it used to, through the escape
// sequence alone.
var putClipboard = func(text string) {
	osc := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
	if os.Getenv("TMUX") != "" {
		osc = "\x1bPtmux;" + strings.ReplaceAll(osc, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	_, _ = os.Stdout.WriteString(osc)

	if os.Getenv("SSH_TTY") == "" { // the local clipboard is the user's
		for _, tool := range clipboardTools() {
			if _, err := exec.LookPath(tool[0]); err != nil {
				continue
			}
			cmd := exec.Command(tool[0], tool[1:]...)
			cmd.Stdin = strings.NewReader(text)
			if cmd.Run() == nil {
				break
			}
		}
	}
}

func clipboardTools() [][]string {
	if runtime.GOOS == "darwin" {
		return [][]string{{"pbcopy"}}
	}
	var tools [][]string
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		tools = append(tools, []string{"wl-copy"})
	}
	if os.Getenv("DISPLAY") != "" {
		tools = append(tools, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	}
	return tools
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
