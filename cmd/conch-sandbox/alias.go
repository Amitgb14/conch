package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Amitgb14/conch/internal/config"
)

// The aliases live in a file of their own, not in ssh/config beside it:
// conch writes that one whole at every start (internal/remote), and it
// includes ~/.ssh/config, so pointing ~/.ssh/config back at it would make
// each include the other.

// aliasFile is conch-sandbox's ssh aliases.
func aliasFile() string { return filepath.Join(config.Dir(), "ssh", "sandboxes") }

// knownHostsFile keeps the sandboxes' host keys apart from the person's:
// a provider hands a deleted sandbox's address to the next, and a host key
// remembered under the address would then refuse it.
func knownHostsFile() string { return filepath.Join(config.Dir(), "ssh", "sandboxes_known_hosts") }

// aliasName is the ssh alias of provider's sandbox id.
func aliasName(provider, id string) string { return "conch-" + provider + "-" + id }

const (
	aliasHeader = "# ssh aliases for conch's sandboxes, written by conch-sandbox. Each block between\n" +
		"# its >>> and <<< lines is conch-sandbox's to rewrite; anything else here is left as it is.\n"
	beginMark = "# >>> conch-sandbox "
	endMark   = "# <<< conch-sandbox "
)

// alias is one Host block.
type alias struct {
	Provider, ID string
	Host         string
	Port         int
	User         string
	Identity     string // the private key, for the key kind
	Token        bool   // User is a token, a secret
	Expires      *time.Time
	// Root is the checkout in the sandbox, or its home; kept so start can
	// say it again (Orca's projectRoot).
	Root string
}

// metaMark starts the line in each block that says whose it is, so a
// caller that knows only the alias — the herdr app, a herdr workspace —
// gets the provider and id back without guessing where a name with - in
// it ends and an id with - in it begins.
const metaMark = "# conch-sandbox "

func (al alias) meta() string {
	m := metaMark + "provider=" + al.Provider + " id=" + al.ID
	if al.Root != "" {
		m += " root=" + url.QueryEscape(al.Root)
	}
	return m
}

// parseMeta reads a meta line; ok is false for any other line.
func parseMeta(line string) (provider, id, root string, ok bool) {
	rest, found := strings.CutPrefix(line, metaMark)
	if !found {
		return "", "", "", false
	}
	for _, f := range strings.Fields(rest) {
		k, v, _ := strings.Cut(f, "=")
		switch k {
		case "provider":
			provider = v
		case "id":
			id = v
		case "root":
			root, _ = url.QueryUnescape(v)
		}
	}
	return provider, id, root, provider != "" && id != ""
}

// owner is the sandbox behind one of conch-sandbox's aliases.
type owner struct{ Provider, ID, Root string }

// aliasOwner is the sandbox name is the alias of, read from its block's
// meta line. A name with no block of conch-sandbox's — or one whose meta
// line does not name it back — is not conch-sandbox's to act on.
func aliasOwner(name string) (owner, error) {
	lines, err := readAliases()
	if err != nil {
		return owner{}, err
	}
	for i, l := range lines {
		if l != beginMark+name {
			continue
		}
		for _, m := range lines[i+1:] {
			if m == endMark+name || strings.HasPrefix(m, beginMark) {
				break
			}
			if p, id, root, ok := parseMeta(m); ok && aliasName(p, id) == name {
				return owner{Provider: p, ID: id, Root: root}, nil
			}
		}
	}
	return owner{}, fmt.Errorf("%s is not one of conch-sandbox's ssh aliases (%s)", name, aliasFile())
}

func (al alias) name() string { return aliasName(al.Provider, al.ID) }

// configValue is v as one ssh_config argument. A value with a newline or a
// quote in it — from a provider's reply — could add lines of its own to
// the file, so it is refused rather than escaped.
func configValue(what, v string) (string, error) {
	if v == "" {
		return "", fmt.Errorf("no ssh %s", what)
	}
	for _, r := range v {
		if r == '"' || unicode.IsControl(r) {
			return "", fmt.Errorf("the ssh %s the provider gave cannot go in an ssh config", what)
		}
	}
	if strings.ContainsAny(v, " \t") {
		return `"` + v + `"`, nil
	}
	return v, nil
}

// block is al's text, between its marks.
func (al alias) block() (string, error) {
	host, err := configValue("host", al.Host)
	if err != nil {
		return "", err
	}
	user, err := configValue("user", al.User)
	if err != nil {
		return "", err
	}
	known, err := configValue("known hosts file", knownHostsFile())
	if err != nil {
		return "", err
	}
	port := al.Port
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("ssh port %d", port)
	}
	var b strings.Builder
	b.WriteString(beginMark + al.name() + "\n")
	b.WriteString(al.meta() + "\n")
	fmt.Fprintf(&b, "Host %s\n  HostName %s\n  Port %d\n  User %s\n", al.name(), host, port, user)
	b.WriteString("  StrictHostKeyChecking accept-new\n")
	fmt.Fprintf(&b, "  HostKeyAlias %s\n  UserKnownHostsFile %s\n", al.name(), known)
	if al.Identity != "" {
		id, err := configValue("key", al.Identity)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "  IdentityFile %s\n  IdentitiesOnly yes\n", id)
	}
	switch {
	case al.Token && al.Expires != nil:
		fmt.Fprintf(&b, "  # the user is a token, until %s: conch-sandbox start writes a new one\n", al.Expires.UTC().Format(time.RFC3339))
	case al.Token:
		b.WriteString("  # the user is a token: conch-sandbox start writes a new one\n")
	}
	b.WriteString(endMark + al.name() + "\n")
	return b.String(), nil
}

// readAliases is the file's lines; a missing file has none.
func readAliases() ([]string, error) {
	b, err := os.ReadFile(aliasFile())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n"), nil
}

// without is lines with name's block taken out, and whether it was there.
// A block whose end mark was lost runs to the next block or the end.
func without(lines []string, name string) ([]string, bool) {
	var out []string
	found := false
	for i := 0; i < len(lines); i++ {
		if lines[i] != beginMark+name {
			out = append(out, lines[i])
			continue
		}
		found = true
		for i+1 < len(lines) && lines[i] != endMark+name && !strings.HasPrefix(lines[i+1], beginMark) {
			i++
		}
	}
	return out, found
}

// hasAlias says whether name's block is in the file.
func hasAlias(name string) bool {
	lines, err := readAliases()
	if err != nil {
		return false
	}
	_, found := without(lines, name)
	return found
}

func writeAliases(lines []string) error {
	if err := os.MkdirAll(filepath.Dir(aliasFile()), 0o700); err != nil {
		return err
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	text := strings.Join(lines, "\n") + "\n"
	if len(lines) == 0 {
		text = aliasHeader
	}
	return config.WriteFileAtomic(aliasFile(), []byte(text), 0o600)
}

// setAlias writes al's block, in place of one of the same name.
func setAlias(al alias) error {
	block, err := al.block()
	if err != nil {
		return err
	}
	if al.Root == "" {
		// A rewrite with a fresh token keeps the checkout it was made with.
		if o, err := aliasOwner(al.name()); err == nil {
			al.Root = o.Root
			if block, err = al.block(); err != nil {
				return err
			}
		}
	}
	lines, err := readAliases()
	if err != nil {
		return err
	}
	lines, _ = without(lines, al.name())
	if len(lines) == 0 {
		lines = strings.Split(strings.TrimRight(aliasHeader, "\n"), "\n")
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	lines = append(lines, "")
	lines = append(lines, strings.Split(strings.TrimRight(block, "\n"), "\n")...)
	return writeAliases(lines)
}

// removeAlias takes name's block out, and the host keys kept for it, and
// says whether it was there. The file stays, header and all, so the
// Include in ~/.ssh/config still names a file.
func removeAlias(name string) (bool, error) {
	lines, err := readAliases()
	if err != nil {
		return false, err
	}
	lines, found := without(lines, name)
	if found {
		// A blank line was put before the block; leave no run of them.
		var out []string
		for i, l := range lines {
			if strings.TrimSpace(l) == "" && i > 0 && strings.TrimSpace(lines[i-1]) == "" {
				continue
			}
			out = append(out, l)
		}
		if err := writeAliases(out); err != nil {
			return true, err
		}
	}
	return found, forgetHostKeys(name)
}

// forgetHostKeys drops the host keys remembered for alias: the next
// sandbox to be given its name is a different machine.
func forgetHostKeys(alias string) error {
	b, err := os.ReadFile(knownHostsFile())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var keep []string
	changed := false
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if hostsName(line, alias) {
			changed = true
			continue
		}
		keep = append(keep, line)
	}
	if !changed {
		return nil
	}
	text := strings.Join(keep, "\n")
	if text != "" {
		text += "\n"
	}
	return config.WriteFileAtomic(knownHostsFile(), []byte(text), 0o600)
}

// hostsName says whether a known_hosts line is for alias, plain or with a
// port ([alias]:2222).
func hostsName(line, alias string) bool {
	f := strings.Fields(line)
	if len(f) == 0 {
		return false
	}
	for _, h := range strings.Split(f[0], ",") {
		if h == alias || strings.HasPrefix(h, "["+alias+"]:") {
			if _, err := strconv.Atoi(strings.TrimPrefix(h, "["+alias+"]:")); h == alias || err == nil {
				return true
			}
		}
	}
	return false
}
