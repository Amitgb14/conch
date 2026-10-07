package adapter

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
)

// Two tiers of agent, said plainly rather than implied.
//
// Supported is the contract AGENTS.md sets: an adapter written here,
// detection rules read off the real agent, sessions that list, resume and
// delete, a setup inspector, labels and docs. Five agents meet it, and
// writing fifteen more to that bar stalls on session formats nobody
// documents.
//
// Runs here is a manifest anybody can drop in: conch starts the agent,
// detects it in a pane and gives it a first message. What it does not
// claim, it says — sessions and the setup view answer "not read for this
// agent" rather than pretending to read a format nobody has written down.
// That honesty is the point of having a tier at all: the wider catalogs
// *detect* an agent, and conch is not going to say it supports one it
// cannot resume.
// The tiers themselves live in the protocol, which is where a client
// reads them from; these are the names this package uses for them.
const (
	TierSupported = proto.TierSupported
	TierRunsHere  = proto.TierRunsHere
)

// A manifest is the same file detect reads for its screen rules
// (<CONCH_HOME>/agents/<name>.toml), with a [run] table saying how to
// start the agent. One file an agent, because somebody adding an agent is
// answering one question — how do I run it and how do I know it is
// running — and two files would be two halves of that.
type manifest struct {
	Agent string `toml:"agent"`
	Label string `toml:"label"`
	Run   runSpec
}

// runSpec is how to start an agent: the whole of what conch needs, in the
// words of a command line rather than of this package.
type runSpec struct {
	// Binary is the program, found in Dirs and then on PATH.
	Binary string   `toml:"binary"`
	Dirs   []string `toml:"dirs"`
	// Flags go before anything the person types.
	Flags string `toml:"flags"`
	// Prompt gives a first message to the session being started. {prompt}
	// is where it goes, quoted: "{prompt}" alone appends it as an
	// argument, "--message {prompt}" puts it behind a flag.
	//
	// Left out, the agent cannot be given one — and conch says so rather
	// than passing it somewhere it does not belong. That is the common
	// case and not a formality: several agents' prompt flags are
	// *non-interactive* (aider's --message answers and exits, amp's -x
	// prints and exits), so using them for a first message would start
	// something that is over before the person looks at it.
	Prompt string `toml:"prompt"`
	// Resume reopens a saved session, {id} being its id, and ResumeLast
	// the most recent in this folder. Empty means it cannot be resumed —
	// which is said rather than guessed at.
	Resume     string `toml:"resume"`
	ResumeLast string `toml:"resume_last"`
	// Env is extra environment, "NAME=value" a line.
	Env []string `toml:"env"`
	// Install is a shell script that installs it, run visibly in a pane;
	// empty means conch will not offer to.
	Install string `toml:"install"`
}

// shippedAgents are the agents conch ships a manifest for: it starts and
// watches them, and says what it does not do with them. They are read
// from the same files detect reads its rules from, so an agent is one
// file whichever half of conch is asking.
//
// A manifest of the person's own with the same name replaces one of
// these, which is how somebody fixes a flag that has changed under us —
// and these are flags that change: the comments in each file say what was
// verified and when it was not.
func shippedAgents(taken map[string]bool) ([]Adapter, []error) {
	var out []Adapter
	var errs []error
	seen := map[string]bool{}
	entries, err := fs.ReadDir(detect.Builtin(), "manifests")
	if err != nil {
		return nil, []error{err}
	}
	for _, e := range entries {
		b, err := fs.ReadFile(detect.Builtin(), "manifests/"+e.Name())
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		a, err := agentFromManifest(b, taken, seen)
		switch {
		case err != nil:
			// A shipped manifest that will not load is conch's own fault
			// and worth saying loudly, but not worth failing to start for.
			errs = append(errs, fmt.Errorf("built-in %s: %w", e.Name(), err))
		case a != nil:
			out = append(out, a)
			seen[a.Name()] = true
		}
	}
	return out, errs
}

// manifestAgents reads the manifests in dir and returns an adapter for
// each one that says how to run an agent. A manifest with no [run] table
// is a detect-only override of a supported agent, which is what that file
// has always been for, and is left to detect.
//
// taken are the names already supported: a manifest may not take one, or
// dropping a file in would quietly replace a tested adapter with a guess.
func manifestAgents(dir string, taken map[string]bool) ([]Adapter, []error) {
	if dir == "" {
		return nil, nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.toml"))
	if err != nil {
		return nil, []error{err}
	}
	sort.Strings(files)
	var out []Adapter
	var errs []error
	seen := map[string]bool{}
	for _, f := range files {
		a, err := manifestAgent(f, taken, seen)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(f), err))
		case a != nil:
			out = append(out, a)
			seen[a.Name()] = true
		}
	}
	return out, errs
}

func manifestAgent(path string, taken, seen map[string]bool) (Adapter, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return agentFromManifest(b, taken, seen)
}

func agentFromManifest(b []byte, taken, seen map[string]bool) (Adapter, error) {
	var m manifest
	if _, err := toml.Decode(string(b), &m); err != nil {
		return nil, err
	}
	switch {
	case m.Run.Binary == "":
		return nil, nil // a detect-only override, which is detect's business
	case m.Agent == "":
		return nil, errors.New("no agent name")
	case !nameOK(m.Agent):
		return nil, fmt.Errorf("%q is not a name: lower-case letters, digits and dashes", m.Agent)
	case taken[m.Agent]:
		return nil, fmt.Errorf("%q is a supported agent; a manifest cannot replace it (rename it to add one of your own)", m.Agent)
	case seen[m.Agent]:
		return nil, fmt.Errorf("%q was already named by another manifest", m.Agent)
	}
	label := m.Label
	if label == "" {
		label = m.Agent
	}
	return &cliAgent{
		name: m.Agent, label: label, binary: m.Run.Binary,
		dirs: expandDirs(m.Run.Dirs), flags: m.Run.Flags,
		promptTmpl: m.Run.Prompt, resumeTmpl: m.Run.Resume, resumeLast: m.Run.ResumeLast,
		env: m.Run.Env, install: m.Run.Install, tier: TierRunsHere,
	}, nil
}

// nameOK is the shape of an agent's name: it goes into the protocol, a
// file name and a pane's label, so it is kept to what all three take.
func nameOK(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return s != ""
}

// expandDirs takes ~ to the home folder, as a person writing a manifest by
// hand will: the shell is not involved in reading one.
func expandDirs(dirs []string) []string {
	home, err := os.UserHomeDir()
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if err == nil && (d == "~" || strings.HasPrefix(d, "~/")) {
			d = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(d, "~"), "/"))
		}
		out = append(out, d)
	}
	return out
}

// ManifestDir is where agents of the "runs here" tier are described. It is
// detect's directory: one file an agent, holding how to run it and how to
// recognise it.
func ManifestDir(configDir string) string { return detect.ManifestDir(configDir) }
