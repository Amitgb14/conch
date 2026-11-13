package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Amitgb14/conch/internal/remote"
)

var machinePaneRef = regexp.MustCompile(`^([^\s:]+):(p[0-9]+)$`)

// paneArgs says which of a command's arguments name a pane, by index, so
// a ref is looked for there and nowhere else: `conch send p5 look at
// busybox:p3` is text for p5, not a pane on busybox. It reads each
// command's arguments as that command does — its flags, and which of them
// take a value — and a command or subcommand that takes no pane gives
// none.
func paneArgs(cmd string, args []string) []int {
	switch cmd {
	case "read", "rename", "close", "redraw":
		// No flags: the pane is the first argument, whatever it looks like.
		if len(args) > 0 {
			return []int{0}
		}
	case "attach":
		// Its own loop over -hold, -raw and -view (attach.go).
		for i, a := range args {
			switch a {
			case "-hold", "--hold", "-raw", "--raw", "-view", "--view":
				continue
			}
			return []int{i}
		}
	case "send":
		return firstPositional(args, 0, nil)
	case "wait":
		return firstPositional(args, 0, map[string]bool{"state": true, "timeout": true})
	case "show":
		// Every pane it is to show.
		if i := firstPositional(args, 0, map[string]bool{"name": true}); i != nil {
			var out []int
			for j := i[0]; j < len(args); j++ {
				out = append(out, j)
			}
			return out
		}
	case "upload":
		// The pane is -ssh's: a terminal logged in with ssh.
		_, values := parseFlags(args, 0, map[string]bool{"to": true, "ssh": true})
		if i, ok := values["ssh"]; ok {
			return []int{i}
		}
	case "agent":
		if len(args) == 0 {
			return nil
		}
		switch args[0] {
		case "explain", "question":
			if len(args) > 1 {
				return []int{1}
			}
		case "prompt":
			return firstPositional(args, 1, map[string]bool{"until": true, "timeout": true, "at": true})
		case "answer":
			return firstPositional(args, 1, map[string]bool{"question": true})
		}
	}
	return nil
}

// firstPositional is the index of the first argument from off on that the
// flag package would leave as an argument, as a list of one, or nil.
func firstPositional(args []string, off int, valued map[string]bool) []int {
	if i, _ := parseFlags(args, off, valued); i < len(args) {
		return []int{i}
	}
	return nil
}

// parseFlags walks args from off as the flag package parses them: -name,
// --name, -name=value, and -name value for a flag that takes one (valued),
// up to "--" or the first argument that is not a flag. It returns where
// the arguments begin and where each flag's separate value is.
func parseFlags(args []string, off int, valued map[string]bool) (int, map[string]int) {
	values := map[string]int{}
	i := off
	for i < len(args) {
		a := args[i]
		if a == "--" {
			return i + 1, values
		}
		if len(a) < 2 || a[0] != '-' {
			return i, values
		}
		name := strings.TrimPrefix(a[1:], "-")
		i++
		if strings.Contains(name, "=") {
			continue
		}
		if valued[name] && i < len(args) {
			values[name] = i
			i++
		}
	}
	return i, values
}

// machinePane takes a pane named machine:pN — how the TUI, Conch.app and
// the phone name a pane on another machine, and what their Copy Pane ID
// gives — as -m machine pN, so a copied ID works as it is. Only where the
// command takes a pane (paneArgs), and only a machine conch has (or
// local): text that merely looks like one is left alone. machine is the
// -m already given; a ref on another machine than that one is an error.
// A command reaches one machine, so refs to two machines are refused, and
// so is a bare pN beside a ref elsewhere: it means a pane of -m's machine
// (this computer without -m), and would have been looked for on the
// ref's instead.
func machinePane(cmd string, args []string, machine string) ([]string, string, error) {
	var out []string
	given := machine
	var bare, ref string // a pane named without a machine, and the first with one
	for _, i := range paneArgs(cmd, args) {
		a := args[i]
		m := machinePaneRef.FindStringSubmatch(a)
		if m == nil {
			if bare == "" {
				bare = a
			}
			continue
		}
		on := m[1]
		if on != remote.LocalID {
			mach, ok := remote.LookupMachine(on)
			if !ok {
				continue
			}
			on = mach.ID
		}
		if machine != "" && !sameMachine(machine, on) {
			if ref != "" {
				return nil, "", fmt.Errorf("%s and %s are panes on different machines: conch %s reaches one machine at a time", ref, a, cmd)
			}
			return nil, "", fmt.Errorf("%s is a pane on %s, and -m says %s", a, m[1], machine)
		}
		if ref == "" {
			ref = a
		}
		if out == nil {
			out = append([]string{}, args...)
		}
		out[i] = m[2]
		if on != remote.LocalID {
			machine = on
		} else if machine == "" {
			machine = remote.LocalID
		}
	}
	if out == nil {
		return args, machine, nil
	}
	if bare != "" && !sameMachine(firstNonEmptyStr(given, remote.LocalID), machine) {
		where := "this computer"
		if given != "" {
			where = given
		}
		return nil, "", fmt.Errorf("%s names no machine, so it is a pane on %s, and %s is on another: conch %s reaches one machine at a time — name it as machine:%s", bare, where, ref, cmd, bare)
	}
	return out, machine, nil
}

// sameMachine says whether a, as -m or a ref gives it, is the machine
// whose ID (or local) is id.
func sameMachine(a, id string) bool {
	if a == id {
		return true
	}
	if a == remote.LocalID || id == remote.LocalID {
		return false
	}
	mach, ok := remote.LookupMachine(a)
	return ok && mach.ID == id
}
