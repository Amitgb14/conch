package adapter

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Writing and removing an ACP agent's manifest: what `conch agent add
// -acp` does, and what the server does for Settings (agent.acp.add), so
// both write the same file the same way.

// ACPManifest is an ACP agent as its manifest says it.
type ACPManifest struct {
	Name, Label, Command string
	Dirs, Env            []string
}

// ErrManifestExists is a manifest of that name already there.
var ErrManifestExists = errors.New("a manifest of that name is there already")

// oneLine refuses a value with a line break or control character in it:
// each goes into a file somebody reads, and into a shell.
func oneLineOK(what, v string) error {
	if strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("%s has a line break or control character in it", what)
	}
	return nil
}

// Check says what is wrong with m, if anything. supported says whether a
// name is one of the agents conch supports, which a manifest may not take.
func (m ACPManifest) Check(supported func(string) bool) error {
	switch {
	case !nameOK(m.Name):
		return fmt.Errorf("%q is not a name: lower-case letters, digits and dashes", m.Name)
	case supported != nil && supported(m.Name):
		return fmt.Errorf("%q is a supported agent; pick another name (%s-acp is one)", m.Name, m.Name)
	case strings.TrimSpace(m.Command) == "":
		return errors.New("no command: the one that speaks ACP on its stdin and stdout")
	}
	for what, v := range map[string]string{"the command": m.Command, "the label": m.Label} {
		if err := oneLineOK(what, v); err != nil {
			return err
		}
	}
	for _, d := range m.Dirs {
		if err := oneLineOK("a folder", d); err != nil {
			return err
		}
	}
	for _, e := range m.Env {
		name, _, ok := strings.Cut(e, "=")
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return fmt.Errorf("%q is not NAME=value", e)
		}
		if err := oneLineOK("an environment line", e); err != nil {
			return err
		}
	}
	return nil
}

// WriteACPManifest writes m to dir/NAME.toml and returns the path. An
// existing file is replaced only with replace, and only when it is an
// ACP manifest itself: a [run] agent of the same name is somebody's own.
func WriteACPManifest(dir string, m ACPManifest, replace bool, supported func(string) bool) (string, error) {
	m.Command = strings.TrimSpace(m.Command)
	if err := m.Check(supported); err != nil {
		return "", err
	}
	path := filepath.Join(dir, m.Name+".toml")
	if b, err := os.ReadFile(path); err == nil {
		if !replace {
			return "", fmt.Errorf("%s: %w", path, ErrManifestExists)
		}
		if !isACPManifest(b) {
			return "", fmt.Errorf("%s describes an agent conch runs another way; it is left alone", path)
		}
	}
	type acpTable struct {
		Command string   `toml:"command"`
		Dirs    []string `toml:"dirs,omitempty"`
		Env     []string `toml:"env,omitempty"`
	}
	type file struct {
		Agent string   `toml:"agent"`
		Label string   `toml:"label,omitempty"`
		ACP   acpTable `toml:"acp"`
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s, added by conch. It speaks the Agent Client Protocol;\n", m.Name)
	fmt.Fprintf(&b, "# conch runs `conch acp` in its pane as the client.\n")
	if err := toml.NewEncoder(&b).Encode(file{Agent: m.Name, Label: m.Label,
		ACP: acpTable{Command: m.Command, Dirs: m.Dirs, Env: m.Env}}); err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, b.Bytes()); err != nil {
		return "", err
	}
	return path, nil
}

// RemoveACPManifest removes dir/NAME.toml, if it is an ACP manifest.
func RemoveACPManifest(dir, name string) error {
	if !nameOK(name) {
		return fmt.Errorf("%q is not an agent name", name)
	}
	path := filepath.Join(dir, name+".toml")
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no agent %q was added on this machine", name)
		}
		return err
	}
	if !isACPManifest(b) {
		return fmt.Errorf("%s describes an agent conch runs another way; it is left alone", path)
	}
	return os.Remove(path)
}

func isACPManifest(b []byte) bool {
	var m manifest
	if _, err := toml.Decode(string(b), &m); err != nil {
		return false
	}
	return strings.TrimSpace(m.ACP.Command) != ""
}

// Spec is this agent as its manifest says it.
func (a *acpAgent) Spec() ACPManifest {
	return ACPManifest{Name: a.name, Label: a.label, Command: a.command, Dirs: a.rawDirs, Env: a.env}
}

// Own is whether its manifest is the person's own (agents/NAME.toml),
// which Settings can change and remove, rather than one conch ships.
func (a *acpAgent) Own() bool { return a.own }
