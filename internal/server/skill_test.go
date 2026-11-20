package server

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Amitgb14/conch/internal/agentsetup"
	"github.com/Amitgb14/conch/internal/proto"
)

// agent.skill writes into the server's own home — so `conch -m` installs
// it on that machine — and from an agent's pane is refused, the person's
// setup being theirs.
func TestAgentSkillMethod(t *testing.T) {
	home := a5IsolateEnv(t)
	s, _ := a5Server(t)
	run := func(c *client, p proto.AgentSkillParams) (proto.AgentSkillResult, *proto.Error) {
		res, perr := s.dispatch(c, proto.Message{Method: proto.MethodAgentSkill, Params: proto.Marshal(p)})
		if perr != nil {
			return proto.AgentSkillResult{}, perr
		}
		return res.(proto.AgentSkillResult), nil
	}
	claude := filepath.Join(home, ".claude", "skills", "conch", "SKILL.md")

	res, perr := run(&client{}, proto.AgentSkillParams{})
	// Every one of conch's skills, for each of the three folders, each
	// named — the first is conch's own in Claude's.
	if perr != nil || res.Applied || len(res.Changes) != 3*len(agentsetup.Skills) || res.Changes[0].Action != proto.SyncCreate ||
		res.Changes[0].Path != claude || res.Changes[0].Skill != "conch" || res.Changes[1].Skill != "conch-autopilot" {
		t.Fatalf("plan: %+v %v", res, perr)
	}
	if _, err := os.Stat(claude); err == nil {
		t.Fatal("a plan wrote the skill")
	}
	if res, perr = run(&client{}, proto.AgentSkillParams{Apply: true}); perr != nil || !res.Applied {
		t.Fatalf("apply: %+v %v", res, perr)
	}
	if b, _ := os.ReadFile(claude); !bytes.Equal(b, agentsetup.Skill) {
		t.Fatal("not written")
	}
	if _, perr := run(&client{}, proto.AgentSkillParams{Agents: []string{"aider"}}); perr == nil || perr.Code != proto.ErrBadRequest {
		t.Fatalf("unknown agent: %v", perr)
	}

	work := t.TempDir()
	agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	if _, perr := run(&client{pane: "p1"}, proto.AgentSkillParams{Remove: true, Apply: true}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("from an agent: %v", perr)
	}
	if _, err := os.Stat(claude); err != nil {
		t.Fatal("removed from an agent's pane")
	}
}

// agent.skill with Skills writes only those; agent.library.fetch downloads
// a skill into this machine's library (from a local repository standing in
// for GitHub), and says which to pick when a repository holds several.
func TestAgentSkillPickAndLibraryFetch(t *testing.T) {
	home := a5IsolateEnv(t)
	s, _ := a5Server(t)
	res, perr := s.dispatch(&client{}, proto.Message{Method: proto.MethodAgentSkill,
		Params: proto.Marshal(proto.AgentSkillParams{Skills: []string{"conch-review"}, Agents: []string{"claude"}, Apply: true})})
	if perr != nil {
		t.Fatal(perr)
	}
	for _, c := range res.(proto.AgentSkillResult).Changes {
		if c.Skill != "conch-review" {
			t.Errorf("wrote %s too", c.Skill)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "conch-review", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, perr := s.dispatch(&client{}, proto.Message{Method: proto.MethodAgentSkill,
		Params: proto.Marshal(proto.AgentSkillParams{Skills: []string{"nope"}})}); perr == nil {
		t.Fatal("an unknown skill was taken")
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	gh := t.TempDir()
	t.Setenv("CONCH_GITHUB_URL", "file://"+gh)
	work := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for _, p := range []string{"skills/pdf/SKILL.md", "skills/xlsx/SKILL.md"} {
		os.MkdirAll(filepath.Join(work, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(work, p), []byte("a skill"), 0o644)
	}
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-q", "-m", "x")
	os.MkdirAll(filepath.Join(gh, "acme"), 0o755)
	git("clone", "-q", "--bare", work, filepath.Join(gh, "acme", "skills.git"))

	fetch := func(c *client, p proto.LibraryFetchParams) (proto.LibraryFetchResult, *proto.Error) {
		res, perr := s.dispatch(c, proto.Message{Method: proto.MethodLibraryFetch, Params: proto.Marshal(p)})
		if perr != nil {
			return proto.LibraryFetchResult{}, perr
		}
		return res.(proto.LibraryFetchResult), nil
	}
	r, perr := fetch(&client{}, proto.LibraryFetchParams{Source: "acme/skills"})
	if perr != nil || len(r.Found) != 2 || r.Path != "" {
		t.Fatalf("several: %+v %v", r, perr)
	}
	r, perr = fetch(&client{}, proto.LibraryFetchParams{Source: "acme/skills/skills/pdf"})
	if perr != nil || r.Name != "pdf" || r.Source != "acme/skills/skills/pdf" {
		t.Fatalf("one: %+v %v", r, perr)
	}
	if b, err := os.ReadFile(filepath.Join(r.Path, "SKILL.md")); err != nil || string(b) != "a skill" {
		t.Fatalf("fetched skill: %q %v", b, err)
	}
	if _, perr := fetch(&client{}, proto.LibraryFetchParams{Source: "acme/missing"}); perr == nil {
		t.Fatal("a repository that isn't there was fetched")
	}
}
