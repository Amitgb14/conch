package integration

import (
	"slices"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
)

// KindHook is an action of somebody else's, shared through a registry: a
// command run on events, as an [[actions]] entry with `on` naming events
// is (config/hooks.go). Unlike every other kind it runs something — a
// command on this machine, with the person's rights — which is why
// installing one shows the command first and an update that changes it is
// asked about again, -y or not. It reaches no service of its own and
// holds no key.
const KindHook = "hook"

// HookSpec is a hook file's [hook].
type HookSpec struct {
	// Run is the command, as sh -lc reads it.
	Run string `toml:"run"`
	// On is the events it runs on (config.HookEvents).
	On []string `toml:"on"`
	// Timeout is seconds, 0 for conch's default.
	Timeout int `toml:"timeout"`
	// Variables are the CONCH_* variables it reads, said to whoever
	// installs it so they know what it is told.
	Variables []string `toml:"variables"`
}

// HookVariables is every variable a hook is given about its event.
var HookVariables = []string{"CONCH_EVENT", "CONCH_ACTION", "CONCH_PROJECT", "CONCH_PROJECT_ID", "CONCH_ROOT",
	"CONCH_BRANCH", "CONCH_WORKTREE", "CONCH_PANE", "CONCH_AGENT", "CONCH_MESSAGE", "CONCH_PR", "CONCH_PR_URL",
	"CONCH_PR_TITLE", "CONCH_ERROR", "CONCH_SOCKET"}

func (f *File) hookProblems(add func(string, ...any)) {
	none := func(set bool, what string) {
		if set {
			add("%s: a hook file runs a command and reaches no service, so it has none", what)
		}
	}
	none(f.API != "", "api")
	none(f.BaseURL != "", "base_url")
	none(f.KeyEnv != "", "key_env")
	none(f.KeyHelp != "", "key_help")
	none(f.AuthHeader != "" || f.AuthValue != "" || f.Auth.Type != "" || f.Auth.Header != "" || f.Auth.Value != "" ||
		len(f.Auth.Scopes) > 0 || f.Auth.UserEnv != "", "[auth]")
	none(len(f.FollowHosts) > 0, "follow_hosts")
	none(len(f.States) > 0, "[states]")
	none(f.Project.ID != "", "[project]")
	none(len(f.Detect.Files)+len(f.Detect.Remotes)+len(f.Detect.Checks) > 0, "[detect]")
	none(f.Source.Reader != "" || f.Source.Path != "" || f.Source.Store != "" || len(f.Source.Platforms) > 0 || f.Source.MinReader != 0, "[source]")
	for _, op := range f.Operations() {
		add("[%s]: not an operation of kind hook, which has none", op)
	}
	h := f.Hook
	if strings.TrimSpace(h.Run) == "" {
		add("[hook] run: the command it runs")
	}
	if len(h.On) == 0 {
		add("[hook] on: the events it runs on, from %s", strings.Join(config.HookEvents, ", "))
	}
	for _, ev := range h.On {
		if !config.IsHookEvent(ev) {
			add("[hook] on %q: one of %s", ev, strings.Join(config.HookEvents, ", "))
		}
	}
	if h.Timeout < 0 {
		add("[hook] timeout %d: seconds, or left out for conch's default", h.Timeout)
	}
	for _, v := range h.Variables {
		if !slices.Contains(HookVariables, v) {
			add("[hook] variables %q: one of %s", v, strings.Join(HookVariables, ", "))
		}
	}
}

// Action is the hook as the action it runs as, named by its label.
func (f *File) Action() config.Action {
	return config.Action{Name: f.DisplayName(), Run: f.Hook.Run, On: slices.Clone(f.Hook.On), Timeout: f.Hook.Timeout}
}

// Hooks is every installed hook file as an action, and what is wrong with
// the ones left out.
func Hooks() ([]config.Action, []string) {
	files, bad := All()
	var out []config.Action
	for _, f := range files {
		if f.Kind == KindHook {
			out = append(out, f.Action())
		}
	}
	return out, bad
}

// SameHook says whether two hook files run the same thing on the same
// events: an update that does not is asked about again.
func SameHook(a, b *File) bool {
	return a != nil && b != nil && a.Hook.Run == b.Hook.Run && slices.Equal(a.Hook.On, b.Hook.On)
}
