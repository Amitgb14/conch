package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/agentsetup"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// The skill teaches agents commands; each must be one conch has, with the
// flags it takes. A skill that drifts from the CLI teaches an agent to
// fail. Checked against the usage text, which lists every command.
func TestSkillCommandsExist(t *testing.T) {
	// Only code — fenced blocks and `spans` — holds commands; prose says
	// "a conch pane" without meaning one.
	var code []string
	for _, m := range regexp.MustCompile("(?s)```[a-z]*\n(.*?)```").FindAllStringSubmatch(string(agentsetup.Skill), -1) {
		code = append(code, strings.Split(m[1], "\n")...)
	}
	for _, m := range regexp.MustCompile("`([^`\n]+)`").FindAllStringSubmatch(string(agentsetup.Skill), -1) {
		code = append(code, m[1])
	}
	re := regexp.MustCompile(`(?:^|[\s;&|(])conch ((?:agent |server )?[a-z]+)(.*)`)
	usageLines := strings.Split(usage, "\n")
	seen := 0
	for _, snippet := range code {
		m := re.FindStringSubmatch(snippet)
		if m == nil {
			continue
		}
		cmd, rest := m[1], m[2]
		var line string
		for i, l := range usageLines {
			if strings.Contains(l, "conch "+cmd+" ") || strings.HasSuffix(strings.TrimSpace(l), "conch "+cmd) || strings.Contains(l, "conch "+cmd+"\t") {
				// A command's flags may continue on its next lines.
				line = strings.Join(usageLines[i:min(i+4, len(usageLines))], " ")
				break
			}
		}
		if line == "" {
			t.Errorf("the skill uses `conch %s`, which conch doesn't have", cmd)
			continue
		}
		seen++
		for _, flag := range regexp.MustCompile(`(?:^|\s)(-[a-z]+)`).FindAllStringSubmatch(rest, -1) {
			if !strings.Contains(line, "["+flag[1]) && !strings.Contains(line, " "+flag[1]+" ") {
				t.Errorf("the skill passes %s to `conch %s`, which doesn't take it:\n%s", flag[1], cmd, line)
			}
		}
	}
	if seen < 8 {
		t.Fatalf("found only %d commands in the skill: the pattern has gone wrong", seen)
	}
}

func TestA4AgentSkill(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	var applied bool
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method != proto.MethodAgentSkill {
			return nil, nil
		}
		var p proto.AgentSkillParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			t.Error(err)
		}
		applied = p.Apply
		return proto.AgentSkillResult{Applied: p.Apply, Changes: []proto.SkillChange{
			{Path: "/h/.claude/skills/conch/SKILL.md", Agents: []string{"claude"}, Action: proto.SyncCreate},
			{Path: "/h/.agents/skills/conch/SKILL.md", Agents: []string{"codex", "gemini", "opencode"}, Action: proto.SyncSkip,
				Detail: "a conch skill there isn't conch's own; left as it is"},
		}}, nil
	})
	var err error
	out, _ := a4Capture(t, "", func() { err = runAgent([]string{"skill"}) })
	if err != nil || applied {
		t.Fatalf("plan: %v applied=%v", err, applied)
	}
	for _, want := range []string{
		"create  /h/.claude/skills/conch/SKILL.md  claude\n",
		"skip    /h/.agents/skills/conch/SKILL.md  codex, gemini, opencode — a conch skill there isn't conch's own; left as it is\n",
		"run again with -apply to write it\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan output lacks %q:\n%s", want, out)
		}
	}
	out, _ = a4Capture(t, "", func() { err = runAgent([]string{"skill", "-apply", "-agent", "Claude, codex"}) })
	var p proto.AgentSkillParams
	srv.params(t, proto.MethodAgentSkill, &p)
	if err != nil || !p.Apply || strings.Join(p.Agents, ",") != "claude,codex" || p.Remove || strings.Contains(out, "-apply") {
		t.Fatalf("apply: %q %v %+v", out, err, p)
	}
	a4Capture(t, "", func() { err = runAgent([]string{"skill", "-remove", "-apply"}) })
	p = proto.AgentSkillParams{}
	srv.params(t, proto.MethodAgentSkill, &p)
	if err != nil || !p.Remove || len(p.Agents) != 0 {
		t.Fatalf("remove: %v %+v", err, p)
	}

	// A file that couldn't be written fails the command, after saying which.
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		return proto.AgentSkillResult{Applied: true, Changes: []proto.SkillChange{
			{Path: "/h/a", Agents: []string{"claude"}, Action: proto.SyncCreate, Error: "permission denied"},
			{Path: "/h/b", Agents: []string{"codex"}, Action: proto.SyncCreate}}}, nil
	})
	out, _ = a4Capture(t, "", func() { err = runAgent([]string{"skill", "-apply"}) })
	if err == nil || err.Error() != "1 of 2 could not be written" || !strings.Contains(out, "failed: permission denied") {
		t.Fatalf("failure: %q %v", out, err)
	}
	// The server's own refusal (an unknown agent) comes through.
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		return nil, proto.Errorf(proto.ErrBadRequest, "conch doesn't know where aider keeps skills")
	})
	if err := runAgent([]string{"skill", "-agent", "aider"}); err == nil || !strings.Contains(err.Error(), "aider keeps skills") {
		t.Fatalf("unknown agent: %v", err)
	}
	if err := runAgent([]string{"skill", "extra"}); err == nil || !strings.Contains(err.Error(), "usage: conch agent skill") {
		t.Fatalf("usage: %v", err)
	}
}

func TestA4AgentSkillOldServer(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = []string{"pane.v1", proto.CapAgentSync}
		return h
	})
	if err := runAgent([]string{"skill"}); err == nil || !strings.Contains(err.Error(), "predates `conch agent skill`") {
		t.Fatalf("old server: %v", err)
	}
	if called(srv, proto.MethodAgentSkill) {
		t.Fatal("asked anyway")
	}
}
