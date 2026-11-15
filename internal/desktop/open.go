// Package desktop is conch's side of the desktop: which program opens a
// link on the desktop it runs on (the TUI and the commands both open
// links, and each had its own copy of the choice), and the bridge that
// desktop apps talk to (`conch desktop`, bridge.go).
package desktop

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Host is what the choice depends on, so a test can be any machine.
type Host struct {
	GOOS     string
	Getenv   func(string) string
	Release  func() string // the kernel release, as /proc reports it
	LookPath func(string) bool
}

// Local is the machine conch is running on.
func Local() Host {
	return Host{
		GOOS:    runtime.GOOS,
		Getenv:  os.Getenv,
		Release: func() string { return readFile("/proc/sys/kernel/osrelease") },
		LookPath: func(name string) bool {
			_, err := exec.LookPath(name)
			return err == nil
		},
	}
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// IsWSL says whether this is Linux under Windows' Subsystem for Linux:
// the distribution's variable, which WSL sets for what it starts, or the
// kernel's release, which names Microsoft in both WSL 1 and 2 — the
// variable is not passed on by everything (a service, sudo).
func (h Host) IsWSL() bool {
	if h.GOOS != "linux" {
		return false
	}
	if h.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	return strings.Contains(strings.ToLower(h.Release()), "microsoft")
}

// OpenCommand is the command that opens url in the desktop's browser.
//
// On WSL the Linux way usually leads nowhere: xdg-open is there, but no
// browser is installed in the distribution, so a link conch opened did
// nothing. Windows' own browser is reached through wslview where wslu is
// installed, else through the URL handler every Windows has — rundll32
// rather than `cmd /c start`, which reads & in a link as its own.
func (h Host) OpenCommand(url string) []string {
	if h.GOOS == "darwin" {
		return []string{"open", url}
	}
	if h.IsWSL() {
		if h.LookPath("wslview") {
			return []string{"wslview", url}
		}
		if h.LookPath("rundll32.exe") {
			return []string{"rundll32.exe", "url.dll,FileProtocolHandler", url}
		}
	}
	return []string{"xdg-open", url}
}

// Open opens url in the browser of the machine conch runs on, without
// waiting for it.
func Open(url string) error {
	argv := Local().OpenCommand(url)
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
