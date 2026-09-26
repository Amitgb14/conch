package remote

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
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
	a, err := p.SSHAccess(ctx, id)
	if err != nil {
		return nil, err
	}
	return sandboxSSH(label, a), nil
}

// sandboxSSH reaches a sandbox through its provider's ssh gateway. It never
// prompts: the token is the whole login, and the gateway's host key is
// taken the first time, as nobody is there to answer for it.
func sandboxSSH(label string, a sandbox.Access) Transport {
	return &sandboxTransport{
		ssh:    &sshTransport{target: a.Target(), opts: sshOpts{acceptNew: true}},
		name:   label,
		secret: a.User,
	}
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
