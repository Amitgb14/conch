package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

const sshUsage = "usage: conch ssh TARGET [SSH OPTION...]"

// runSSHLogin is `conch ssh`: a terminal on this computer logged in to a
// host with ssh, outside every project — what the TUI's H starts, built
// the same way (remote.LoginCommand: conch's ssh config, a connection of
// its own, the host last after --), so the tree, the files view and a
// second session read it as one. Options after the target are ssh's own
// (-p 2222, -o Setting=value), checked as the TUI's dialog checks them.
// It prints the pane's ID, for whatever started it to show.
func runSSHLogin(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New(sshUsage)
	}
	if onRemoteMachine() {
		return errors.New("an ssh session starts on this computer: run conch ssh without -m")
	}
	target := args[0]
	command, err := remote.LoginCommand(target, args[1:]...)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	var info proto.PaneInfo
	params := proto.PaneCreateParams{Name: sshPaneName(target), Command: command, Cwd: home, Cols: 120, Rows: 40, NoProject: true}
	if err := call(c, proto.MethodPaneCreate, params, &info); err != nil {
		return err
	}
	fmt.Println(info.ID)
	return nil
}

// sshPaneName is how the session is named, as the TUI names one: an
// ssh:// address without its scheme, so it doesn't read "ssh ssh://…".
func sshPaneName(target string) string {
	if rest, ok := strings.CutPrefix(target, "ssh://"); ok && strings.TrimRight(rest, "/") != "" {
		return strings.TrimRight(rest, "/")
	}
	return target
}
