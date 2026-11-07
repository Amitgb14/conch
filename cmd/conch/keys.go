package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/keymap"
)

// `conch keys` — tmux's list-keys. It is the other half of [keys.bind]:
// the names are only usable if something says what they are, and a key
// that was rebound or unbound is worth seeing without starting the TUI,
// since the TUI is where a wrong binding is hardest to work out.

const keysUsage = "usage: conch keys [-bound] [-toml] [-json]"

func cmdKeys(args []string) error {
	fs := flag.NewFlagSet("keys", flag.ContinueOnError)
	bound := fs.Bool("bound", false, "only the keys that are bound to something")
	asTOML := fs.Bool("toml", false, "print the whole table as a [keys.bind] you can edit")
	asJSON := fs.Bool("json", false, "print the table as JSON, as Conch.app reads it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(keysUsage)
	}
	cfg, err := config.Load()
	if err != nil {
		// The keys are still worth printing: they are the defaults, and
		// saying so is better than refusing over an unrelated typo.
		fmt.Fprintf(os.Stderr, "%s could not be read (%v): these are the defaults\n", config.File(), err)
		cfg = config.Default()
	}
	binds, problems := keymap.ResolveFor(cfg.Keys.Bind, cfg.Keys.Prefix)
	if *asJSON {
		return keysJSON(cfg.Keys.Prefix, binds, problems)
	}
	if *asTOML {
		// Printed so that it can be pasted whole, prefix and all, and
		// read back as what it says. One key an action, which is how a
		// file can hold it: an action that answers to several (v % |)
		// comes out as the first, so pasting this settles it on that one.
		fmt.Printf("# conch's prefix table, as %s holds it: paste it and edit a line.\n", config.File())
		fmt.Printf("# One key an action, so the alternatives (v %% |) settle on the first.\n")
		fmt.Printf("[keys]\nprefix = %q\n\n[keys.bind]\n", cfg.Keys.Prefix)
		for _, a := range keymap.Actions() {
			fmt.Printf("%-18s = %-10q # %s\n", a, binds.Key(a), keymap.What(a))
		}
		return keysProblems(problems)
	}
	fmt.Printf("%s, then:\n\n", cfg.Keys.Prefix)
	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tACTION\tDOES\t")
	for _, a := range keymap.Actions() {
		keys := keymap.ShowAll(binds.Keys(a))
		switch {
		case keys == "" && *bound:
			continue
		case keys == "":
			keys = "—"
		}
		note := ""
		switch {
		case !binds.Bound(a):
			note = "  (unbound)"
			// Or its key went to something else, which is not the same
			// thing and is the likelier surprise.
			for _, k := range keymap.Defaults[a] {
				if other := binds.Action(k); other != "" {
					note = fmt.Sprintf("  (%s is %s now)", keymap.Show(k), other)
					break
				}
			}
		case binds.Rebound(a):
			note = fmt.Sprintf("  (was %s)", keymap.ShowAll(keymap.Defaults[a]))
		}
		fmt.Fprintf(w, "%s\t%s\t%s%s\t\n", keys, a, keymap.What(a), note)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Printf("\nRebind one under [keys.bind] in %s — conch keys -toml prints the lot.\n", config.File())
	fmt.Println("A digit is the tab at that place and ctrl/alt+arrows resize; those two are not bound keys.")
	return keysProblems(problems)
}

// keysProblems is what was wrong with the table, as the command's error so
// that a script notices.
func keysProblems(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return errors.New("[keys.bind] in " + config.File() + ": " + strings.Join(problems, "; "))
}

// keysTable is conch keys -json: the prefix and the resolved table, which
// is how Conch.app takes its prefix keys from the TUI's settings rather
// than reading [keys.bind] a second way. An unbound action is listed with
// no keys, so the app can say it is unbound rather than guess.
type keysTable struct {
	Prefix   string        `json:"prefix"`
	Keys     []keysBinding `json:"keys"`
	Problems []string      `json:"problems,omitempty"`
}

type keysBinding struct {
	Action string   `json:"action"`
	Keys   []string `json:"keys"`
	What   string   `json:"what"`
}

// keysJSON prints the table. Problems are in it rather than the exit
// status: a reader of JSON wants the keys that did resolve either way,
// and a failed exit would cost it them.
func keysJSON(prefix string, binds keymap.Map, problems []string) error {
	t := keysTable{Prefix: keymap.Normalize(prefix), Keys: []keysBinding{}, Problems: problems}
	for _, a := range keymap.Actions() {
		ks := binds.Keys(a)
		if ks == nil {
			ks = []string{}
		}
		t.Keys = append(t.Keys, keysBinding{Action: string(a), Keys: ks, What: keymap.What(a)})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(t)
}
