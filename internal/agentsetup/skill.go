package agentsetup

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Skill is conch's own skill: how an agent in a conch pane starts, prompts
// and reads other agents. It is installed only when asked (InstallSkill),
// into each agent's own skills folder in the home, where it sits beside the
// person's skills and replaces none of them.
//
//go:embed skill/SKILL.md
var Skill []byte

// skillName is the skill's folder, and the name agents know it by.
const skillName = "conch"

// skillMark is how conch knows a SKILL.md there is its own to update or
// remove: a skill of the same name the person wrote is left alone.
var skillMark = []byte("installed-by: conch")

// SkillChange is what installing or removing the skill does to one file.
// Agents are the ones that read it: Codex, Gemini and OpenCode share
// ~/.agents/skills, Claude has its own.
type SkillChange struct {
	Path   string
	Agents []string
	Action string // create, update, same, remove or skip
	Detail string
	Error  string
}

// SkillTargets is where each agent in agents reads user skills from: one
// SKILL.md per folder, with the agents that read it. An empty agents means
// every agent conch knows the skills folder of.
func SkillTargets(e Env, agents []string) ([]SkillChange, error) {
	if len(agents) == 0 {
		agents = SyncNames()
	}
	byPath := map[string]*SkillChange{}
	var order []string
	for _, a := range agents {
		files, ok := userFiles(e, a)
		if !ok {
			return nil, fmt.Errorf("conch doesn't know where %s keeps skills; it can install for %v", a, SyncNames())
		}
		if len(files.skills) == 0 {
			continue
		}
		// The first folder is the one the agent shares, when it shares one.
		path := filepath.Join(files.skills[0], skillName, "SKILL.md")
		if byPath[path] == nil {
			byPath[path] = &SkillChange{Path: path}
			order = append(order, path)
		}
		byPath[path].Agents = append(byPath[path].Agents, a)
	}
	out := make([]SkillChange, 0, len(order))
	for _, p := range order {
		sort.Slice(byPath[p].Agents, func(i, j int) bool { return rankAgent(byPath[p].Agents[i]) < rankAgent(byPath[p].Agents[j]) })
		out = append(out, *byPath[p])
	}
	return out, nil
}

// InstallSkill puts conch's skill where agents read it, or with remove
// takes conch's own copy away. Without apply it only says what it would
// do. A SKILL.md there that conch didn't write is skipped, never replaced.
func InstallSkill(e Env, agents []string, remove, apply bool) ([]SkillChange, error) {
	if e.Home == "" {
		return nil, errors.New("no home directory to install the skill in")
	}
	changes, err := SkillTargets(e, agents)
	if err != nil {
		return nil, err
	}
	for i := range changes {
		c := &changes[i]
		old, err := os.ReadFile(c.Path)
		exists := err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			c.Action, c.Error = ActionSkip, err.Error()
			continue
		}
		ours := exists && bytes.Contains(old, skillMark)
		switch {
		case exists && !ours:
			c.Action, c.Detail = ActionSkip, "a conch skill there isn't conch's own; left as it is"
			continue
		case remove && !exists:
			c.Action, c.Detail = ActionSame, "not installed"
			continue
		case remove:
			c.Action = ActionRemove
		case !exists:
			c.Action = ActionCreate
		case bytes.Equal(old, Skill):
			c.Action = ActionSame
			continue
		default:
			c.Action, c.Detail = ActionUpdate, "from an older conch"
		}
		if !apply {
			continue
		}
		if err := writeSkill(c.Path, remove); err != nil {
			c.Error = err.Error()
		}
	}
	return changes, nil
}

func writeSkill(path string, remove bool) error {
	if remove {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// The folder was conch's; leave it only if something else is in it.
		_ = os.Remove(filepath.Dir(path))
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".conch-tmp"
	if err := os.WriteFile(tmp, Skill, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path) // never a half-written skill for an agent to read
}

// ActionRemove is the skill taken away again.
const ActionRemove = "remove"
