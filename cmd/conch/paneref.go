package main

import (
	"fmt"
	"regexp"

	"github.com/Amitgb14/conch/internal/remote"
)

// paneCommands take a pane among their arguments.
var paneCommands = map[string]bool{
	"send": true, "read": true, "attach": true, "rename": true, "close": true, "redraw": true,
	"wait": true, "agent": true, "upload": true, "view": true, "show": true,
}

var machinePaneRef = regexp.MustCompile(`^([^\s:]+):(p[0-9]+)$`)

// machinePane takes a pane named machine:pN — how the TUI, Conch.app and
// the phone name a pane on another machine, and what their Copy Pane ID
// gives — as -m machine pN, so a copied ID works as it is. Only the first
// such argument before a --, and only a machine conch has (or local): a
// command's text that merely looks like one is left alone. machine is the
// -m already given; a ref on another machine than that one is an error.
func machinePane(cmd string, args []string, machine string) ([]string, string, error) {
	if !paneCommands[cmd] {
		return args, machine, nil
	}
	for i, a := range args {
		if a == "--" {
			break
		}
		m := machinePaneRef.FindStringSubmatch(a)
		if m == nil {
			continue
		}
		on := m[1]
		if on != "local" {
			mach, ok := remote.LookupMachine(on)
			if !ok {
				continue
			}
			on = mach.ID
		}
		if machine != "" && machine != on && !(machine == "local" && on == "local") {
			if mach, ok := remote.LookupMachine(machine); !ok || mach.ID != on {
				return nil, "", fmt.Errorf("%s is a pane on %s, and -m says %s", a, m[1], machine)
			}
		}
		out := append([]string{}, args...)
		out[i] = m[2]
		if on == "local" {
			if machine == "" {
				machine = "local"
			}
			return out, machine, nil
		}
		return out, on, nil
	}
	return args, machine, nil
}
