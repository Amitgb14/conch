package remote

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// A sandbox is saved as a machine whose target is "PROVIDER:ID". Its ssh
// destination can't be saved: the provider hands out a fresh token for
// each connection. An older conch reading the catalog tries ssh to that
// target, fails to resolve it, and shows the machine offline — harmless,
// where an empty target would have meant this computer.

// SandboxTarget is the catalog target of a provider's sandbox.
func SandboxTarget(provider, id string) string { return provider + ":" + id }

// ParseSandboxTarget splits a sandbox's catalog target; ok is false for an
// ssh target.
func ParseSandboxTarget(target string) (provider, id string, ok bool) {
	provider, id, found := strings.Cut(target, ":")
	if !found || id == "" || strings.ContainsAny(id, "@/: ") || !sandbox.Known(provider) {
		return "", "", false
	}
	return provider, id, true
}

// IsSandbox reports whether the machine is a provider's sandbox.
func (m Machine) IsSandbox() bool {
	_, _, ok := ParseSandboxTarget(m.Target)
	return ok
}

// openProvider returns the named provider, set up from config.toml; tests
// replace it.
var openProvider = func(name string) (sandbox.Provider, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return sandbox.Open(name, cfg.Sandbox)
}

// OpenProvider returns the named sandbox provider, set up from config.toml.
func OpenProvider(name string) (sandbox.Provider, error) { return openProvider(name) }

// SandboxStoppedError is a sandbox that isn't running, so there is nothing
// to connect to. Starting it is the user's call: a running sandbox costs.
type SandboxStoppedError struct {
	Label string
	State sandbox.State
}

func (e *SandboxStoppedError) Error() string {
	return fmt.Sprintf("sandbox %s is %s", e.Label, e.State)
}

// TransportFor reaches the machine saved with target: over ssh, or for a
// sandbox, over ssh with fresh credentials from its provider. label names
// it in messages, since a sandbox's ssh destination is a secret.
func TransportFor(ctx context.Context, label, target string, interactive bool) (Transport, error) {
	provider, id, ok := ParseSandboxTarget(target)
	if !ok {
		return SSH(target, interactive), nil
	}
	p, err := openProvider(provider)
	if err != nil {
		return nil, err
	}
	s, err := p.Get(ctx, id)
	switch {
	case errors.Is(err, sandbox.ErrNotFound):
		return nil, fmt.Errorf("sandbox %s (%s %s) no longer exists", label, provider, id)
	case err != nil:
		return nil, err
	case s.State != sandbox.StateStarted:
		return nil, &SandboxStoppedError{Label: label, State: s.State}
	}
	// A provider that takes a key of your own rather than handing out a
	// secret is given conch's public key, so the way in is one this
	// computer already holds the other half of.
	identity := ""
	if ka, ok := p.(sandbox.KeyAuthorizer); ok {
		line, path, err := publicKey(ctx)
		if err != nil {
			return nil, err
		}
		if err := ka.AuthorizeKey(ctx, id, line); err != nil {
			return nil, err
		}
		// ssh has to offer the key conch just authorized. The generated
		// config names conch's own key, which stops ssh trying the
		// defaults, so the user's key would never be offered and the
		// sandbox would refuse a login it was set up to allow.
		identity = strings.TrimSuffix(path, ".pub")
	}
	a, err := p.SSHAccess(ctx, id)
	if err != nil {
		return nil, err
	}
	return sandboxSSH(label, a, identity), nil
}

// DefaultSandboxLabel names a sandbox nobody named, after its ID.
func DefaultSandboxLabel(id string) string {
	if len(id) > 8 {
		id = id[:8]
	}
	return "sandbox-" + id
}

// SetUpSandbox installs conch in a new sandbox and connects to its server.
// Nothing asks first: the sandbox was made for this.
func SetUpSandbox(ctx context.Context, m Machine, say func(string)) (*client.Client, error) {
	tr, err := TransportFor(ctx, m.Label, m.Target, false)
	if err != nil {
		return nil, err
	}
	// A provider says a sandbox is started before it is answering —
	// Daytona says so within a second of being asked for one — so wait
	// until it does rather than failing the first thing conch tries.
	if say != nil {
		say("waiting for " + m.Label + " to answer")
	}
	if err := WaitReachable(ctx, tr); err != nil {
		return nil, fmt.Errorf("%s never answered: %w", m.Label, err)
	}
	return Connect(ctx, tr, Options{Install: true, Progress: say})
}

// UnsavedWork lists what deleting a machine would lose: branches with
// commits no remote has, and uncommitted changes.
func UnsavedWork(projects []proto.ProjectInfo) []string {
	count := func(n int, what string) string {
		if n == 1 {
			return "1 " + what
		}
		return fmt.Sprintf("%d %ss", n, what)
	}
	var lines []string
	for _, p := range projects {
		status := map[string]*proto.GitStatus{}
		for _, w := range p.Worktrees {
			status[w.Path] = w.Status
		}
		told := map[string]bool{}
		for _, b := range p.Branches {
			var what []string
			switch {
			case b.Upstream != "" && !b.Gone && b.Ahead > 0:
				what = append(what, count(b.Ahead, "commit")+" not pushed")
			case (b.Upstream == "" || b.Gone) && b.BaseAhead > 0:
				what = append(what, count(b.BaseAhead, "commit")+" on no remote")
			}
			if s := status[b.Worktree]; b.Worktree != "" && !s.Clean() {
				what = append(what, count(s.Files, "file")+" uncommitted")
				told[b.Worktree] = true
			}
			if len(what) > 0 {
				lines = append(lines, fmt.Sprintf("%s %s: %s", p.Name, b.Name, strings.Join(what, ", ")))
			}
		}
		// A worktree no branch accounts for — a repository with no commits
		// yet, or a detached HEAD — can still hold uncommitted work.
		for _, w := range p.Worktrees {
			if told[w.Path] || w.Status.Clean() {
				continue
			}
			name := w.Branch
			if name == "" {
				name = w.Path
			}
			lines = append(lines, fmt.Sprintf("%s %s: %s uncommitted", p.Name, name, count(w.Status.Files, "file")))
		}
	}
	return lines
}

// sandboxSSH reaches a sandbox through its provider's ssh gateway. It never
// prompts: the token is the whole login, and the gateway's host key is
// taken the first time, as nobody is there to answer for it.
func sandboxSSH(label string, a sandbox.Access, identity string) Transport {
	t := &sandboxTransport{
		ssh:  &sshTransport{target: a.Target(), opts: sshOpts{acceptNew: true, identity: identity}},
		name: label,
	}
	if !a.PlainUser {
		t.secret = a.User
	}
	return t
}

type sandboxTransport struct {
	ssh    *sshTransport
	name   string
	secret string
}

func (t *sandboxTransport) Command(ctx context.Context, script string) (*exec.Cmd, error) {
	return t.ssh.Command(ctx, script)
}

func (t *sandboxTransport) Describe() string { return t.name }

func (t *sandboxTransport) interactive() bool { return false }

func (t *sandboxTransport) forBridge() Transport { return t }

// failed keeps the token out of the message: ssh names its destination.
func (t *sandboxTransport) failed(err error, stderr string) error {
	err = t.ssh.failed(err, stderr)
	if t.secret == "" || !strings.Contains(err.Error(), t.secret) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), t.secret, "…"))
}

// SandboxShell is ssh into a running sandbox for a person at a terminal: a
// login shell when command is empty, otherwise command run there. tty asks
// for a terminal for the command too, for one that draws a screen; a shell
// gets one whenever this side has one. The command is not tied to ctx,
// which only bounds asking the provider for access: a shell lasts as long
// as the person keeps it.
func SandboxShell(ctx context.Context, label, target, command string, tty bool) (*exec.Cmd, error) {
	if _, _, ok := ParseSandboxTarget(target); !ok {
		return nil, fmt.Errorf("%s is not a sandbox", label)
	}
	tr, err := TransportFor(ctx, label, target, false)
	if err != nil {
		return nil, err
	}
	st, ok := tr.(*sandboxTransport)
	if !ok {
		return nil, fmt.Errorf("%s is not a sandbox", label)
	}
	cfg, err := loginSSHConfig()
	if err != nil {
		return nil, err
	}
	// Its own connection, as LoginCommand makes, and the gateway's host
	// key taken the first time as every other conch connection to it does.
	args := []string{"-F", cfg, "-o", "ControlPath=none", "-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=15"}
	if tty {
		args = append(args, "-t")
	}
	if o := st.ssh.opts; o.identity != "" {
		args = append(args, "-i", o.identity, "-o", "IdentitiesOnly=yes")
	}
	args = append(args, "--", st.ssh.target)
	if command != "" {
		args = append(args, command)
	}
	return exec.Command(sshBinary(), args...), nil
}
