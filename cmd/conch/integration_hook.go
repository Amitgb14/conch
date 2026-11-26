package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/integration"
)

// hookWords says what a hook file runs, on what, and what it is told:
// the whole command, since it runs with the person's rights.
func hookWords(f *integration.File) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s runs this on this machine when %s happens:\n\n", f.DisplayName(), strings.Join(f.Hook.On, " or "))
	for _, line := range strings.Split(strings.TrimRight(f.Hook.Run, "\n"), "\n") {
		fmt.Fprintf(&b, "    %s\n", line)
	}
	if len(f.Hook.Variables) > 0 {
		fmt.Fprintf(&b, "\nIt reads %s.", strings.Join(f.Hook.Variables, ", "))
	}
	if f.Hook.Timeout > 0 {
		fmt.Fprintf(&b, " Stopped after %ds.", f.Hook.Timeout)
	}
	return strings.TrimRight(b.String(), "\n")
}

// installHook installs a hook: its command is fetched and shown before
// anything is written, and -y does not skip being shown it.
func installHook(ctx context.Context, reg integration.Registry, e integration.Entry, signed, yes bool) error {
	data, file, err := integration.FetchFile(ctx, reg, e)
	if err != nil {
		return err
	}
	fmt.Println(hookWords(file))
	if !signed {
		fmt.Print("(The registry's index is unsigned.) ")
	}
	if !yes {
		if !askYes("Install it?") {
			return errors.New("nothing installed (-y installs without asking)")
		}
	} else {
		fmt.Println()
	}
	if err := integration.Install(e, data); err != nil {
		return err
	}
	fmt.Printf("installed %s %d (hook) · runs on %s; its runs are logged in %s\n", e.Name, e.Version, strings.Join(file.Hook.On, ", "),
		filepath.Join(config.Dir(), "hooks", "logs"))
	return nil
}

// hookUpdateAgreed says whether an update to name, now next, may go on:
// a hook whose command or events change is shown and asked about again,
// even with -y — a command is not changed under somebody without them
// seeing it.
func hookUpdateAgreed(name string, next *integration.File) bool {
	if next == nil || next.Kind != integration.KindHook {
		return true
	}
	now, _, err := integration.Read(filepath.Join(integration.Dir(), name+".toml"))
	if err == nil && integration.SameHook(now, next) {
		return true
	}
	fmt.Println("The new version runs something else:")
	fmt.Println(hookWords(next))
	return askYes("Update?")
}
