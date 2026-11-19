package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
)

// ssh, herdr, the herdr app and Orca all read ~/.ssh/config, and none of
// them reads conch-sandbox's aliases unless it includes their file. So
// the line goes at the top of ~/.ssh/config, before any Host, where it
// applies to every connection — and only with the person's say-so: it is
// their file. Without it no alias works, so conch-sandbox stops and says
// so rather than writing aliases nothing reads.

// userSSHConfig is ~/.ssh/config.
func userSSHConfig() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home folder to find ~/.ssh/config in: %w", err)
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// includeLine is the line ~/.ssh/config needs.
func includeLine() string {
	p := aliasFile()
	if strings.ContainsAny(p, " \t") {
		p = `"` + p + `"`
	}
	return "Include " + p
}

// includes says whether ssh config text includes the alias file: by its
// path, with ~, relative to ~/.ssh as ssh reads it, or by a pattern that
// matches it.
func includes(text, home string) bool {
	want := filepath.Clean(aliasFile())
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		i := strings.IndexAny(line, " \t=")
		if i < 0 || !strings.EqualFold(line[:i], "include") {
			continue
		}
		rest := strings.TrimLeft(line[i:], " \t")
		rest = strings.TrimLeft(strings.TrimPrefix(rest, "="), " \t")
		for _, arg := range splitArgs(rest) {
			switch {
			case strings.HasPrefix(arg, "~/"):
				arg = filepath.Join(home, arg[2:])
			case !filepath.IsAbs(arg):
				arg = filepath.Join(home, ".ssh", arg)
			}
			if ok, _ := filepath.Match(filepath.Clean(arg), want); ok {
				return true
			}
		}
	}
	return false
}

// splitArgs splits ssh_config arguments, which may be double-quoted.
func splitArgs(s string) []string {
	var out []string
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
		if s[0] == '"' {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				return append(out, s[1:])
			}
			out = append(out, s[1:end+1])
			s = s[end+2:]
			continue
		}
		end := strings.IndexAny(s, " \t")
		if end < 0 {
			end = len(s)
		}
		out = append(out, s[:end])
		s = s[end:]
	}
	return out
}

// includePresent says whether ~/.ssh/config includes the alias file now.
func includePresent() bool {
	path, err := userSSHConfig()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(path)
	return err == nil && includes(string(b), filepath.Dir(filepath.Dir(path)))
}

// ensureInclude makes sure ~/.ssh/config includes the alias file: it is
// there already, or yes says to add it, or the person at the terminal
// agrees. With nobody to ask it refuses, saying what to run.
func (a *app) ensureInclude(yes bool) error {
	again := a.again()
	path, err := userSSHConfig()
	if err != nil {
		return err
	}
	home := filepath.Dir(filepath.Dir(path))
	// A ~/.ssh/config kept in a dotfiles repository is a link: write
	// through it, not over it.
	target := path
	if t, err := filepath.EvalSymlinks(path); err == nil {
		target = t
	}
	old, err := os.ReadFile(target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read ~/.ssh/config: %w", err)
	}
	if includes(string(old), home) {
		return nil
	}
	if !yes {
		if !a.terminal {
			return fmt.Errorf("ssh reads conch-sandbox's aliases only if ~/.ssh/config includes them, and it does not. "+
				"Run %s --yes to add the line, or put this line at the top of ~/.ssh/config yourself:\n  %s", again, includeLine())
		}
		fmt.Fprintf(a.stderr, "ssh reads conch-sandbox's aliases only if ~/.ssh/config includes them.\nAdd this line to the top of %s?\n  %s\n[Y/n] ", path, includeLine())
		answer, _ := a.readLine()
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "" && a != "y" && a != "yes" {
			return fmt.Errorf("not added, so no alias was written. Add this line at the top of ~/.ssh/config, or run %s --yes:\n  %s", again, includeLine())
		}
	}
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(target); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	text := includeLine() + "\n"
	if len(old) > 0 {
		text += "\n" + string(old)
	}
	if err := config.WriteFileAtomic(target, []byte(text), mode); err != nil {
		return fmt.Errorf("write ~/.ssh/config: %w", err)
	}
	fmt.Fprintf(a.stderr, "Added %q to the top of %s.\n", includeLine(), path)
	return nil
}

// again is the command line that was run, to suggest running again: with
// --yes, said after it.
func (a *app) again() string {
	words := []string{"conch-sandbox"}
	for _, w := range a.line {
		if w == "" || strings.ContainsAny(w, " \t'\"$`\\|&;<>()*?[]#~!{}") {
			w = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}
