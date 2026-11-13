package server

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// What an agent may start.
//
// Scoping keeps an agent to its own work, but pane.create also says *what*
// runs: a command of the caller's own, flags for the agent, its
// environment. An agent that asks the person before each command could
// otherwise start a second agent told to ask nobody
// (--dangerously-skip-permissions, --yolo), or a pane running whatever it
// liked — one tool call around every prompt the person relies on, which
// is exactly what an injected instruction would try. So an agent calling
// from its pane may pick which agent to start and its model, and nothing
// else. It may still start a plain shell, but not type into one — that
// would be the same launch a keystroke later — so it types only into
// panes running an agent (typingInScope, scope.go). The person, the TUI
// and scripts are not held to it, and
// [agents] unrestricted_launch in config.toml lifts it for somebody who
// wants agents to start agents however they like.

// unrestrictedLaunch is [agents] unrestricted_launch as config.toml has it
// now, read at each launch so a change takes effect without a restart;
// tests put their own here.
var unrestrictedLaunch = func() bool {
	c, err := config.Load()
	return err == nil && c.Agents.UnrestrictedLaunch
}

// modelName is a model as an agent's --model takes it: opus,
// claude-opus-4-1[1m], openai/gpt-5, gemini-2.5-pro. It never starts with
// "-", so it cannot be read as a flag of its own.
var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+\[\]-]{0,127}$`)

// launchAllowed refuses what an agent in a pane may not start, and
// otherwise returns cp as it will be run: a --model it gave is written
// again by conch, quoted, since agent_args reaches a shell.
func (s *Server) launchAllowed(c *client, cp proto.PaneCreateParams) (proto.PaneCreateParams, *proto.Error) {
	caller, ok := s.scopedCaller(c)
	if !ok {
		return cp, nil
	}
	args, why := agentLaunch(cp)
	if why == "" {
		cp.AgentArgs = args
		return cp, nil
	}
	if unrestrictedLaunch() {
		return cp, nil
	}
	return cp, outOfScope(caller, "%s: an agent may choose which agent to start and its --model, nothing else ([agents] unrestricted_launch in config.toml allows more)", why)
}

// agentLaunch is cp's agent_args as conch will pass them when cp is
// something an agent may start, else what it is asking for that it may
// not.
func agentLaunch(cp proto.PaneCreateParams) (string, string) {
	switch {
	case len(cp.Command) > 0 && !plainShell(cp.Command):
		return "", fmt.Sprintf("start a pane running %q", strings.Join(cp.Command, " "))
	case len(cp.Env) > 0:
		return "", "set a pane's environment"
	}
	model, ok := onlyModel(cp.AgentArgs)
	if !ok {
		return "", fmt.Sprintf("start an agent with %q", strings.TrimSpace(cp.AgentArgs))
	}
	if model == "" {
		return "", ""
	}
	return "--model " + adapter.ShellQuote(model), ""
}

// plainShell is the login shell an older conch new asked for by name when
// it was given no command: what the server starts anyway.
func plainShell(cmd []string) bool {
	return len(cmd) == 2 && cmd[0] == config.DefaultShell() && cmd[1] == "-l"
}

// onlyModel reads args as nothing, or as --model NAME (or --model=NAME),
// NAME bare or in quotes as conch new and the MCP tool write it. Anything
// else — another flag, a second word, a shell's $ or ; — is not a model.
func onlyModel(args string) (string, bool) {
	words, ok := shellWords(args)
	switch {
	case !ok:
		return "", false
	case len(words) == 0:
		return "", true
	case len(words) == 1 && strings.HasPrefix(words[0], "--model="):
		m := strings.TrimPrefix(words[0], "--model=")
		return m, modelName.MatchString(m)
	case len(words) == 2 && words[0] == "--model":
		return words[1], modelName.MatchString(words[1])
	}
	return "", false
}

// shellWords splits s as a shell would split plain words: spaces between
// them, '…' and "…" taken as they are. Anything a shell would expand or
// act on — $, `, \, ;, |, &, <, >, (, ), a newline — is refused rather
// than interpreted, since a model's name has none of them.
func shellWords(s string) ([]string, bool) {
	var words []string
	var cur strings.Builder
	in := false // inside a word
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == ' ' || ch == '\t':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		case ch == '\'' || ch == '"':
			end := strings.IndexByte(s[i+1:], ch)
			if end < 0 {
				return nil, false
			}
			quoted := s[i+1 : i+1+end]
			if ch == '"' && strings.ContainsAny(quoted, "$`\\") {
				return nil, false
			}
			cur.WriteString(quoted)
			in = true
			i += end + 1
		case strings.IndexByte("$`\\;|&<>()\n\r{}*?!#~", ch) >= 0:
			return nil, false
		default:
			cur.WriteByte(ch)
			in = true
		}
	}
	if in {
		words = append(words, cur.String())
	}
	return words, true
}
