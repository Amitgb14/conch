package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/config"
)

// `conch agent add` writes the manifest for an agent conch has never heard
// of: the second tier, where conch starts the agent and watches it and
// says plainly what it does not do with it.
//
// It writes a file rather than asking a server, because that file is the
// thing: it is read when the server starts, it can be edited by hand, and
// it is the same file detect reads its screen rules from. The command
// exists so nobody has to learn the format to try an agent once.

const agentAddUsage = "usage: conch agent add NAME -command BINARY [-label LABEL] [-prompt 'FLAG {prompt}'] " +
	"[-resume 'FLAG {id}'] [-resume-last FLAGS] [-flags FLAGS] [-dir DIR] [-env NAME=VALUE] [-force]"

func agentAdd(args []string) error {
	fs := flag.NewFlagSet("agent add", flag.ContinueOnError)
	command := fs.String("command", "", "the program to run (required)")
	label := fs.String("label", "", "the name people know it by, e.g. \"Robo Coder\"")
	prompt := fs.String("prompt", "", "how a first message is passed, with {prompt} where it goes; the default appends it as an argument")
	resume := fs.String("resume", "", "how a saved session is reopened, with {id} where it goes; left out, conch will not offer to resume it")
	resumeLast := fs.String("resume-last", "", "how the most recent session here is reopened")
	flagsFor := fs.String("flags", "", "flags conch always passes, before anything you type")
	force := fs.Bool("force", false, "overwrite a manifest of this name")
	var dirs, env repeated
	fs.Var(&dirs, "dir", "a folder to look for the program in before PATH; repeat for several")
	fs.Var(&env, "env", "NAME=VALUE for the agent's environment; repeat for several")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New(agentAddUsage)
	}
	name := rest[0]
	if err := fs.Parse(rest[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 || *command == "" {
		return errors.New(agentAddUsage)
	}
	if !agentNameOK(name) {
		return fmt.Errorf("%q is not an agent name: lower-case letters, digits and dashes", name)
	}
	// The five with adapters are not up for replacement: a file dropped in
	// must not quietly turn a tested agent into a guess.
	if supportedAgent(name) {
		return fmt.Errorf("%q is a supported agent already; pick another name to add one of your own", name)
	}

	dir := adapter.ManifestDir(config.Dir())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, name+".toml")
	if _, err := os.Stat(path); err == nil && !*force {
		return fmt.Errorf("%s already describes %q; -force overwrites it", path, name)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s, added by `conch agent add`. conch runs and watches it;\n", name)
	fmt.Fprintf(&b, "# it does not read its saved conversations (the \"runs here\" tier).\n")
	fmt.Fprintf(&b, "agent = %q\n", name)
	if *label != "" {
		fmt.Fprintf(&b, "label = %q\n", *label)
	}
	// Detection falls back to the process name, which is the one thing
	// that can be guessed: the program conch started is the program it
	// looks for.
	fmt.Fprintf(&b, "process_names = [%q]\n", filepath.Base(*command))
	fmt.Fprintf(&b, "\n[run]\nbinary = %q\n", *command)
	write := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s = %q\n", k, v)
		}
	}
	write("flags", *flagsFor)
	write("prompt", *prompt)
	write("resume", *resume)
	write("resume_last", *resumeLast)
	if len(dirs.values) > 0 {
		fmt.Fprintf(&b, "dirs = [%s]\n", quoteList(dirs.values))
	}
	if len(env.values) > 0 {
		fmt.Fprintf(&b, "env = [%s]\n", quoteList(env.values))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}

	fmt.Printf("wrote %s\n", path)
	// What it will do, and then what it will not. Said from the manifest
	// rather than in general: this command is where somebody learns what
	// the tier means, and a summary that claimed a first message when
	// none was described would be the very thing the tier exists to
	// avoid. (It claimed exactly that, once.)
	fmt.Printf("%s runs here: conch starts it and detects it in a pane.\n", name)
	var not []string
	if *prompt == "" {
		not = append(not, "given a first message (-prompt says how)")
	}
	if *resume == "" && *resumeLast == "" {
		not = append(not, "resumed (-resume or -resume-last says how)")
	}
	not = append(not, "read for saved conversations")
	fmt.Printf("It will not be %s — conch says so rather than pretending.\n", strings.Join(not, ", nor "))
	fmt.Println("Reload the server to pick it up: `conch server reload` (or r in the version box).")
	return nil
}

// repeated collects a flag given more than once.
type repeated struct{ values []string }

func (r *repeated) String() string { return strings.Join(r.values, ",") }

func (r *repeated) Set(v string) error {
	if v = strings.TrimSpace(v); v != "" {
		r.values = append(r.values, v)
	}
	return nil
}

func quoteList(vs []string) string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, fmt.Sprintf("%q", v))
	}
	return strings.Join(out, ", ")
}

func agentNameOK(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return s != ""
}

// supportedAgent reports whether a name belongs to one of the agents conch
// supports with an adapter of its own.
func supportedAgent(name string) bool {
	for _, a := range []string{"claude", "codex", "gemini", "opencode", "devin"} {
		if a == name {
			return true
		}
	}
	return false
}
