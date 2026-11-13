// Package accounts keeps the agent accounts a machine has besides each
// agent's own: a work and a personal Claude subscription, two Max plans.
//
// An account is a configuration folder of the agent's, named by the
// variable the agent reads to move it (CLAUDE_CONFIG_DIR, CODEX_HOME):
// its sign-in, settings and conversations all live there, so starting the
// agent with that variable set is starting it signed in as that account.
// An agent without such a variable keeps the one account it has, rather
// than conch guessing at where its sign-in lives.
//
// The book is the server's, in $CONCH_HOME/accounts.json, not config.toml:
// the sign-ins are this machine's, the server is what starts agents, and a
// client holding config.toml in memory would write an older copy back.
package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
)

// vars is the variable each agent reads for its configuration folder.
var vars = map[string]string{
	"claude": "CLAUDE_CONFIG_DIR",
	"codex":  "CODEX_HOME",
}

// Default is the name that asks for the agent's own account, the one it
// uses when nothing is set.
const Default = "default"

// Var is the variable that moves agent's configuration folder, "" for an
// agent that keeps one account.
func Var(agent string) string { return vars[agent] }

// Supports reports whether agent can have accounts of its own.
func Supports(agent string) bool { return vars[agent] != "" }

// Agents lists the agents that can have accounts, sorted.
func Agents() []string {
	out := make([]string, 0, len(vars))
	for a := range vars {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// Label is an account as people read it: "" is the default.
func Label(name string) string {
	if name == "" {
		return Default
	}
	return name
}

// Account is one of an agent's accounts.
type Account struct {
	Agent string `json:"agent"`
	Name  string `json:"name"`
	Dir   string `json:"dir"`
}

// Pin says which account agents started in Path, or below it, use. Path
// is a project, a worktree or a task's folder; the longest pin wins.
// Account names an account by name, whichever agent has one by it: a
// project pinned to "work" uses Claude's work and Codex's work.
type Pin struct {
	Path    string `json:"path"`
	Account string `json:"account"`
}

// Book is every account, pin and the failover setting.
type Book struct {
	Accounts []Account `json:"accounts,omitempty"`
	Pins     []Pin     `json:"pins,omitempty"`
	// FailoverAt is the percentage of a plan window past which new agents
	// start on the agent's next account instead; 0 never moves them.
	FailoverAt int `json:"failover_at,omitempty"`
}

// File is where the book is kept under conch's folder.
func File(home string) string { return filepath.Join(home, "accounts.json") }

// Load reads the book; a missing or empty file is an empty book.
func Load(path string) (Book, error) {
	var b Book
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return b, nil
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return Book{}, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

// Save writes the book, readable by its owner alone.
func (b Book) Save(path string) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return config.WriteFileAtomic(path, append(data, '\n'), 0o600)
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

// ValidName says what is wrong with an account's name, if anything. A
// name is a folder's and a flag's value, so it is kept to a word.
func ValidName(name string) error {
	if strings.EqualFold(name, Default) {
		return fmt.Errorf("%q is the agent's own account, there already", Default)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("an account's name is a word of up to 32 letters, digits, '.', '_' or '-': %q", name)
	}
	return nil
}

// Get finds agent's account called name.
func (b Book) Get(agent, name string) (Account, bool) {
	for _, a := range b.Accounts {
		if a.Agent == agent && a.Name == name {
			return a, true
		}
	}
	return Account{}, false
}

// Of lists agent's accounts in the order they were added.
func (b Book) Of(agent string) []Account {
	var out []Account
	for _, a := range b.Accounts {
		if a.Agent == agent {
			out = append(out, a)
		}
	}
	return out
}

func (b Book) named(name string) bool {
	for _, a := range b.Accounts {
		if a.Name == name {
			return true
		}
	}
	return false
}

// expand makes ~ the home folder.
func expand(dir string) string {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(dir, "~"))
		}
	}
	return dir
}

// Add makes agent an account called name in dir — by default a folder of
// its own under home, conch's folder — and makes the folder. Signing in
// is the agent's: start it on the account and log in once.
func (b *Book) Add(agent, name, dir, home string) (Account, error) {
	if !Supports(agent) {
		return Account{}, fmt.Errorf("%s keeps one account: conch knows no way to give it a second (only %s can have several)", agent, strings.Join(Agents(), " and "))
	}
	if err := ValidName(name); err != nil {
		return Account{}, err
	}
	if _, ok := b.Get(agent, name); ok {
		return Account{}, fmt.Errorf("%s already has an account called %s", agent, name)
	}
	if dir == "" {
		dir = filepath.Join(home, "accounts", agent, name)
	}
	dir = expand(dir)
	if !filepath.IsAbs(dir) {
		return Account{}, fmt.Errorf("an account's folder must be an absolute path: %q", dir)
	}
	dir = filepath.Clean(dir)
	for _, a := range b.Of(agent) {
		if a.Dir == dir {
			return Account{}, fmt.Errorf("%s's account %s already uses %s", agent, a.Name, dir)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Account{}, err
	}
	a := Account{Agent: agent, Name: name, Dir: dir}
	b.Accounts = append(b.Accounts, a)
	return a, nil
}

// Remove forgets agent's account called name. Its folder — the sign-in
// and every conversation in it — is left where it is. A pin naming an
// account no agent has any longer goes with it.
func (b *Book) Remove(agent, name string) error {
	i := -1
	for j, a := range b.Accounts {
		if a.Agent == agent && a.Name == name {
			i = j
		}
	}
	if i < 0 {
		return fmt.Errorf("%s has no account called %s", agent, name)
	}
	b.Accounts = append(b.Accounts[:i:i], b.Accounts[i+1:]...)
	if !b.named(name) {
		pins := b.Pins[:0:0]
		for _, p := range b.Pins {
			if p.Account != name {
				pins = append(pins, p)
			}
		}
		b.Pins = pins
	}
	return nil
}

// Pin has agents started in path use the account called name; "" takes
// the pin away and Default pins the agents' own accounts.
func (b *Book) Pin(path, name string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("a pin needs an absolute path: %q", path)
	}
	path = filepath.Clean(path)
	i := -1
	for j, p := range b.Pins {
		if p.Path == path {
			i = j
		}
	}
	if name == "" {
		if i < 0 {
			return fmt.Errorf("%s is not pinned to an account", path)
		}
		b.Pins = append(b.Pins[:i:i], b.Pins[i+1:]...)
		return nil
	}
	if strings.EqualFold(name, Default) {
		name = Default
	} else if !b.named(name) {
		return fmt.Errorf("no agent has an account called %s (conch account add AGENT %s)", name, name)
	}
	if i >= 0 {
		b.Pins[i].Account = name
		return nil
	}
	b.Pins = append(b.Pins, Pin{Path: path, Account: name})
	return nil
}

// PinFor is the account dir is pinned to, and the pin's path: the
// longest pin that is dir or holds it.
func (b Book) PinFor(dir string) (name, path string) {
	if dir == "" {
		return "", ""
	}
	dir = filepath.Clean(dir)
	for _, p := range b.Pins {
		if (dir == p.Path || strings.HasPrefix(dir, strings.TrimSuffix(p.Path, "/")+"/")) && len(p.Path) > len(path) {
			name, path = p.Account, p.Path
		}
	}
	return name, path
}

// Choice is the account an agent starts on.
type Choice struct {
	// Account is its name, "" for the agent's own; Dir its folder.
	Account string
	Dir     string
	// Pinned says a pin chose it.
	Pinned bool
	// PassedOver is the account it would have started on had that one not
	// passed FailoverAt (Default for the agent's own).
	PassedOver string
}

// Env is what starts agent on the account: its folder variable, or
// nothing for the agent's own account.
func (c Choice) Env(agent string) []string {
	if c.Account == "" || Var(agent) == "" {
		return nil
	}
	return []string{Var(agent) + "=" + c.Dir}
}

// Usage says how much of an account's plan is used, in percent: the
// fullest of its windows, 0 when nothing is known.
type Usage func(agent, account string) float64

// Choose picks the account a new agent starts on in dir: the one asked
// for by name, else dir's pin, else the agent's own. With FailoverAt set,
// one that is not asked for by name and has passed it gives way to the
// next of the agent's accounts below it — in the order they were added,
// the agent's own first — and stays when every one has.
func (b Book) Choose(agent, asked, dir string, used Usage) (Choice, error) {
	if !Supports(agent) {
		if asked != "" && !strings.EqualFold(asked, Default) {
			return Choice{}, fmt.Errorf("%s keeps one account, so it cannot start on %s", agent, asked)
		}
		return Choice{}, nil
	}
	if asked != "" {
		if strings.EqualFold(asked, Default) {
			return Choice{}, nil
		}
		a, ok := b.Get(agent, asked)
		if !ok {
			return Choice{}, fmt.Errorf("no %s account called %s (conch account list)", agent, asked)
		}
		return Choice{Account: a.Name, Dir: a.Dir}, nil
	}
	var ch Choice
	if name, _ := b.PinFor(dir); name != "" {
		if a, ok := b.Get(agent, name); ok {
			ch = Choice{Account: a.Name, Dir: a.Dir, Pinned: true}
		} else if name == Default {
			ch.Pinned = true
		}
	}
	if b.FailoverAt <= 0 || used == nil || used(agent, ch.Account) < float64(b.FailoverAt) {
		return ch, nil
	}
	ring := append([]Account{{Agent: agent}}, b.Of(agent)...)
	at := 0
	for i, a := range ring {
		if a.Name == ch.Account {
			at = i
		}
	}
	for k := 1; k < len(ring); k++ {
		a := ring[(at+k)%len(ring)]
		if used(agent, a.Name) < float64(b.FailoverAt) {
			return Choice{Account: a.Name, Dir: a.Dir, PassedOver: Label(ch.Account)}, nil
		}
	}
	return ch, nil
}
