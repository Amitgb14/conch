package agentsetup

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// conch's skills: how an agent in a conch pane starts, prompts and reads
// other agents (conch), and the ways of working built on that — several
// agents at once (conch-team), a review-and-fix loop (conch-review),
// shipping a branch (conch-ship) and a ticket taken all the way to a
// merged pull request (conch-factory). They are installed only when asked
// (InstallSkills), into each agent's own skills folder in the home, where
// they sit beside the person's skills and replace none of them.
//
//go:embed skills
var skillFiles embed.FS

// BundledSkill is one of conch's skills: its name, which is also its
// folder's, and its SKILL.md.
type BundledSkill struct {
	Name string
	Body []byte
}

// Skills are conch's skills, conch's own first, then by name.
var Skills = loadSkills()

// Skill is conch's own skill, the one the others build on.
var Skill = Skills[0].Body

// skillName is the skill's folder, and the name agents know it by.
const skillName = "conch"

func loadSkills() []BundledSkill {
	dirs, err := fs.ReadDir(skillFiles, "skills")
	if err != nil {
		panic(err)
	}
	var out []BundledSkill
	for _, d := range dirs {
		b, err := fs.ReadFile(skillFiles, "skills/"+d.Name()+"/SKILL.md")
		if err != nil {
			panic(err)
		}
		out = append(out, BundledSkill{Name: d.Name(), Body: b})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Name == skillName) != (out[j].Name == skillName) {
			return out[i].Name == skillName
		}
		return out[i].Name < out[j].Name
	})
	if len(out) == 0 || out[0].Name != skillName {
		panic("conch's own skill is missing from skills/")
	}
	return out
}

// BundledSkillNamed is the skill called name, and whether conch has one.
func BundledSkillNamed(name string) (BundledSkill, bool) {
	for _, s := range Skills {
		if s.Name == name {
			return s, true
		}
	}
	return BundledSkill{}, false
}

// skillMark is how conch knows a SKILL.md there is its own to update or
// remove: a skill of the same name the person wrote is left alone. It
// counts only in the frontmatter (isOurSkill), where conch puts it.
var skillMark = []byte("installed-by: conch")

// isOurSkill reports whether a SKILL.md is conch's: its frontmatter, and
// only that, carries the mark on a line of its own.
func isOurSkill(b []byte) bool {
	if !bytes.HasPrefix(b, []byte("---\n")) {
		return false
	}
	end := bytes.Index(b[4:], []byte("\n---"))
	if end < 0 {
		return false
	}
	for _, line := range bytes.Split(b[4:4+end], []byte("\n")) {
		if bytes.Equal(bytes.TrimSpace(line), skillMark) {
			return true
		}
	}
	return false
}

// SkillChange is what installing or removing the skill does to one file.
// Agents are the ones that read it: Codex, Gemini and OpenCode share
// ~/.agents/skills; Claude and Devin have their own.
type SkillChange struct {
	Skill  string // which of conch's skills
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
	return skillTargets(e, agents, skillName)
}

func skillTargets(e Env, agents []string, name string) ([]SkillChange, error) {
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
		path := filepath.Join(files.skills[0], name, "SKILL.md")
		if byPath[path] == nil {
			byPath[path] = &SkillChange{Skill: name, Path: path}
			order = append(order, path)
		}
		if !contains(byPath[path].Agents, a) {
			byPath[path].Agents = append(byPath[path].Agents, a)
		}
	}
	out := make([]SkillChange, 0, len(order))
	for _, p := range order {
		sort.Slice(byPath[p].Agents, func(i, j int) bool { return rankAgent(byPath[p].Agents[i]) < rankAgent(byPath[p].Agents[j]) })
		out = append(out, *byPath[p])
	}
	return out, nil
}

// InstallSkill puts conch's own skill where agents read it, or with remove
// takes conch's own copy away. Without apply it only says what it would
// do. A SKILL.md there that conch didn't write is skipped, never replaced.
func InstallSkill(e Env, agents []string, remove, apply bool) ([]SkillChange, error) {
	return installSkill(e, Skills[0], agents, remove, apply)
}

// InstallSkills is InstallSkill for every one of conch's skills: a change
// per skill per folder, grouped by folder so a folder's skills sit
// together, in the order SkillTargets gives the folders.
func InstallSkills(e Env, agents []string, remove, apply bool) ([]SkillChange, error) {
	var out []SkillChange
	for _, sk := range Skills {
		changes, err := installSkill(e, sk, agents, remove, apply)
		if err != nil {
			return nil, err
		}
		out = append(out, changes...)
	}
	folder := func(c SkillChange) string { return filepath.Dir(filepath.Dir(c.Path)) }
	rank := map[string]int{}
	for _, c := range out {
		if _, ok := rank[folder(c)]; !ok {
			rank[folder(c)] = len(rank)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return rank[folder(out[i])] < rank[folder(out[j])] })
	return out, nil
}

func installSkill(e Env, sk BundledSkill, agents []string, remove, apply bool) ([]SkillChange, error) {
	if e.Home == "" {
		return nil, errors.New("no home directory to install the skill in")
	}
	changes, err := skillTargets(e, agents, sk.Name)
	if err != nil {
		return nil, err
	}
	for i := range changes {
		c := &changes[i]
		// A link — to a dotfiles repository, say — is the person's
		// arrangement: neither written through nor replaced.
		if linked(c.Path) || linked(filepath.Dir(c.Path)) {
			c.Action, c.Detail = ActionSkip, "it is a link into somewhere else (a dotfiles repository?): conch leaves it alone"
			continue
		}
		old, err := os.ReadFile(c.Path)
		exists := err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			c.Action, c.Error = ActionSkip, err.Error()
			continue
		}
		ours := exists && isOurSkill(old)
		switch {
		case exists && !ours:
			c.Action, c.Detail = ActionSkip, "a "+sk.Name+" skill there isn't conch's own; left as it is"
			continue
		case remove && !exists:
			c.Action, c.Detail = ActionSame, "not installed"
			continue
		case remove:
			c.Action = ActionRemove
		case !exists:
			c.Action = ActionCreate
		case bytes.Equal(old, sk.Body):
			c.Action = ActionSame
			continue
		default:
			c.Action, c.Detail = ActionUpdate, "from an older conch"
		}
		if !apply {
			continue
		}
		if err := writeSkill(c.Path, sk.Body, remove); err != nil {
			c.Error = err.Error()
		}
	}
	return changes, nil
}

func writeSkill(path string, body []byte, remove bool) error {
	tmp := path + ".conch-tmp"
	_ = os.Remove(tmp) // a copy an interrupted write left
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
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path) // never a half-written skill for an agent to read
}

func linked(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}
