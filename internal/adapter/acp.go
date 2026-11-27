package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/acp"
	"github.com/Amitgb14/conch/internal/proto"
)

// An agent that speaks the Agent Client Protocol has no terminal
// interface of its own, so conch runs one in its pane: `conch acp`, the
// protocol's client, with the agent's own command behind it. A manifest
// says so with an [acp] table instead of [run]:
//
//	agent = "gemini-acp"
//	label = "Gemini (ACP)"
//	[acp]
//	command = "gemini --experimental-acp"
//
// It is still the "runs here" tier — a manifest somebody wrote, not an
// adapter tested against the agent — but it is told apart by Protocol,
// and what the tier usually lacks the protocol gives: its state is
// reported by conch acp from the protocol, a first message goes through
// session/prompt, and sessions reopen with session/load where the agent
// offers it.

// ProtocolACP is what Protocol says of an agent run over ACP.
const ProtocolACP = proto.ProtocolACP

// acpSpec is a manifest's [acp] table.
type acpSpec struct {
	// Command speaks ACP on its stdin and stdout: shell words, read by
	// the login shell, as a manifest's flags are.
	Command string   `toml:"command"`
	Dirs    []string `toml:"dirs"`
	Env     []string `toml:"env"`
	Install string   `toml:"install"`
}

type acpAgent struct {
	name, label string
	command     string
	dirs        []string
	env         []string
	install     string
	// exe is the conch that runs in the pane as the client.
	exe string
	// rawDirs are the folders as written, before ~ is expanded, and own
	// whether the manifest is the person's (see Own).
	rawDirs []string
	own     bool
}

func newACPAgent(name, label string, spec acpSpec) (*acpAgent, error) {
	words := strings.Fields(spec.Command)
	if len(words) == 0 {
		return nil, errors.New("[acp] has no command")
	}
	return &acpAgent{name: name, label: label, command: strings.TrimSpace(spec.Command),
		dirs: expandDirs(spec.Dirs), rawDirs: spec.Dirs, env: spec.Env, install: spec.Install, exe: "conch"}, nil
}

func (a *acpAgent) Name() string          { return a.name }
func (a *acpAgent) Label() string         { return a.label }
func (a *acpAgent) Env() []string         { return a.env }
func (a *acpAgent) InstallScript() string { return a.install }
func (a *acpAgent) Tier() string          { return TierRunsHere }

// Protocol is ProtocolACP: how this agent is spoken to.
func (a *acpAgent) Protocol() string { return ProtocolACP }

// CanPrompt is true: a first message is the first session/prompt.
func (a *acpAgent) CanPrompt() bool { return true }

// CanResume is true for a session by its id; whether the agent can
// load one is its own to say at initialize, and conch acp says so when
// it cannot rather than starting afresh.
func (a *acpAgent) CanResume() bool { return true }

// binary is the program the command starts, for Detect.
func (a *acpAgent) binary() string {
	words := strings.Fields(a.command)
	for len(words) > 1 && strings.Contains(words[0], "=") {
		words = words[1:] // NAME=value before the program
	}
	return words[0]
}

func (a *acpAgent) pathSetup() string {
	return (&cliAgent{dirs: a.dirs}).pathSetup()
}

// PromptArgs are conch acp's: the message is sent as the first turn.
func (a *acpAgent) PromptArgs(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	return "-prompt " + ShellQuote(prompt)
}

// ResumeArgs reopens a session by id. "The latest here" is not a thing
// the protocol has, so it is not offered.
func (a *acpAgent) ResumeArgs(id string) string {
	if id == "" {
		return ""
	}
	return "-load " + ShellQuote(id)
}

// Command runs conch acp in the pane, with the agent's command after
// "--" for the shell to split: what the person wrote in the manifest is
// read as they would read it typed.
func (a *acpAgent) Command(shell, args string) []string {
	line := a.pathSetup() + "exec " + ShellQuote(a.exe) + " acp -agent " + ShellQuote(a.name) + " -label " + ShellQuote(a.label)
	if args = strings.TrimSpace(args); args != "" {
		line += " " + args
	}
	line += " -- " + a.command
	return []string{shell, "-lc", line}
}

// Detect is whether the command's program is installed.
func (a *acpAgent) Detect(ctx context.Context, shell string) Availability {
	return (&cliAgent{binary: a.binary(), dirs: a.dirs}).Detect(ctx, shell)
}

// probeTimeout bounds asking an agent what it can do: one written in
// node takes a second or two to load. A variable so a test need not wait
// it out.
var probeTimeout = 20 * time.Second

// Probe starts the agent in dir through the login shell, asks what it can
// do and, when list is set and it lists them, its sessions there.
func (a *acpAgent) Probe(ctx context.Context, shell, dir string, list bool) (acp.InitializeResult, []acp.SessionInfo, error) {
	if dir == "" {
		return acp.InitializeResult{}, nil, fmt.Errorf("%s: no folder to ask in", a.name)
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	env := append(SanitizeEnv(os.Environ()), a.env...)
	// env, so a command that begins NAME=value runs as the shell would
	// run it typed; exec alone takes no assignments.
	argv := []string{shell, "-lc", a.pathSetup() + "exec env " + a.command}
	return acp.Probe(ctx, acp.Options{Argv: argv, Env: env, Dir: dir, InitTimeout: probeTimeout}, list)
}

// ACPAgent is an agent run over the Agent Client Protocol, for the server
// to ask about its sessions and for Settings to show and change.
type ACPAgent interface {
	Adapter
	Probe(ctx context.Context, shell, dir string, list bool) (acp.InitializeResult, []acp.SessionInfo, error)
	Spec() ACPManifest
	Own() bool
}

// NewACP is an ACP agent from a spec that is not in any file yet, for
// checking a command before it is added.
func NewACP(m ACPManifest) (ACPAgent, error) {
	label := m.Label
	if label == "" {
		label = m.Name
	}
	return newACPAgent(m.Name, label, acpSpec{Command: m.Command, Dirs: m.Dirs, Env: m.Env})
}

// AsACP is ad as an ACP agent, if it is one.
func AsACP(ad Adapter) (ACPAgent, bool) {
	a, ok := ad.(*acpAgent)
	return a, ok
}

// Protocol says how an agent is spoken to: ProtocolACP, or "" for its
// own terminal interface.
func Protocol(ad Adapter) string {
	if p, ok := ad.(interface{ Protocol() string }); ok {
		return p.Protocol()
	}
	return ""
}

// CheckACP starts the command in m once, in dir, and says what it offers
// — or what stopped it — for Settings to show before anything is written.
func CheckACP(ctx context.Context, shell, dir string, m ACPManifest) proto.AgentACPCheck {
	if m.Name == "" {
		m.Name = "acp"
	}
	a, err := NewACP(m)
	if err != nil {
		return proto.AgentACPCheck{Problem: proto.ACPFailed, Message: err.Error()}
	}
	aa := a.(*acpAgent)
	av := a.Detect(ctx, shell)
	if !av.Installed {
		where := "on PATH for your login shell"
		if len(aa.dirs) > 0 {
			where += ", nor in " + strings.Join(aa.rawDirs, ", ")
		}
		return proto.AgentACPCheck{Problem: proto.ACPNotFound, Message: aa.binary() + " was not found " + where}
	}
	out := proto.AgentACPCheck{Path: av.Path, Version: av.Version}
	init, _, err := a.Probe(ctx, shell, dir, false)
	if err != nil {
		var exit *acp.ExitError
		var pe *acp.ProbeError
		errors.As(err, &pe)
		switch {
		case errors.As(err, &exit):
			out.Problem, out.Message, out.Output = proto.ACPExited, "it exited before answering initialize", exit.Stderr
		case errors.Is(err, context.DeadlineExceeded):
			out.Problem = proto.ACPNotACP
			out.Message = fmt.Sprintf("it gave no ACP answer in %s; most agents need a flag for it", probeTimeout.Round(time.Second))
		default:
			out.Problem, out.Message = proto.ACPFailed, err.Error()
		}
		if pe != nil && pe.Noise != "" && out.Output == "" {
			out.Output = pe.Noise
			if out.Problem == proto.ACPFailed {
				out.Problem = proto.ACPNotACP
			}
		}
		return out
	}
	out.ProtocolVersion = init.ProtocolVersion
	out.Load, out.List = init.AgentCapabilities.LoadSession, init.CanList()
	if init.AgentInfo != nil {
		out.Agent = init.AgentInfo.Name
		if init.AgentInfo.Version != "" {
			out.Version = init.AgentInfo.Version
		}
	}
	for _, am := range init.AuthMethods {
		name := am.Name
		if name == "" {
			name = am.ID
		}
		out.Auth = append(out.Auth, name)
	}
	return out
}

// SetProbeTimeout is for tests: how long a probe waits for initialize.
func SetProbeTimeout(d time.Duration) func() {
	old := probeTimeout
	probeTimeout = d
	return func() { probeTimeout = old }
}
