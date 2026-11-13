package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

const sshUsage = `usage: conch ssh TARGET [SSH OPTION...]
       conch ssh -copy-key TARGET [SSH OPTION...]
       conch ssh -check-key TARGET [SSH OPTION...]`

// runSSHLogin is `conch ssh`: a terminal on this computer logged in to a
// host with ssh, outside every project — what the TUI's H starts, built
// the same way (remote.LoginCommand: conch's ssh config, a connection of
// its own, the host last after --), so the tree, the files view and a
// second session read it as one. Options after the target are ssh's own
// (-p 2222, -o Setting=value), checked as the TUI's dialog checks them.
// It prints the pane's ID, for whatever started it to show.
//
// -copy-key starts instead a terminal that puts this computer's public key
// on the host (making one first when there is none), so it logs in without
// a password from then on — what the TUI's K does. ssh asks for the
// password there, in the terminal, so nothing else ever sees it.
//
// -check-key starts nothing: it logs in in batch mode, where ssh never
// asks, and prints key (the key was taken), password (every key was
// refused) or unreachable, with what ssh said. It exits 0 for all three;
// the word is the answer.
func runSSHLogin(args []string) error {
	mode := ""
	if len(args) > 0 && (args[0] == "-copy-key" || args[0] == "-check-key") {
		mode, args = args[0], args[1:]
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New(sshUsage)
	}
	if onRemoteMachine() {
		return errors.New("an ssh session starts on this computer: run conch ssh without -m")
	}
	target := args[0]
	if mode == "-check-key" {
		return checkKeyLogin(target, args[1:])
	}
	command, err := remote.LoginCommand(target, args[1:]...)
	name := sshPaneName(target)
	if mode == "-copy-key" {
		command, _, err = remote.CopyKeyCommand(target, args[1:])
		name = "copy key to " + sshPaneName(target)
	}
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
	params := proto.PaneCreateParams{Name: name, Command: command, Cwd: home, Cols: 120, Rows: 40, NoProject: true}
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

// checkKeyTimeout is how long -check-key waits for ssh; a variable so a
// test can make a host that never answers quick.
var checkKeyTimeout = 15 * time.Second

// checkKeyLogin is conch ssh -check-key: one batch-mode login, given a
// quarter of a minute, and its outcome as a word.
func checkKeyLogin(target string, args []string) error {
	command, err := remote.CheckKeyCommand(target, args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkKeyTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, &stderr
	err = cmd.Run()
	word := remote.KeyLoginResult(err, stderr.String())
	if ctx.Err() != nil {
		word = "unreachable"
	}
	said := strings.TrimSpace(stderr.String())
	if i := strings.LastIndexByte(said, '\n'); i >= 0 {
		said = said[i+1:]
	}
	if word == "key" || said == "" {
		fmt.Println(word)
	} else {
		fmt.Println(word, said)
	}
	return nil
}
