package remote

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// Transport runs commands on a machine. Everything this package does to a
// machine — probing it, installing conch, bridging to its server — is a
// script run through one of these, so a machine can be reached by ssh
// today and by other means (a sandbox VM) later.
type Transport interface {
	// Command returns a command that runs script on the machine, ready for
	// its stdin, stdout and stderr to be wired up.
	Command(ctx context.Context, script string) (*exec.Cmd, error)
	// Describe names the machine in messages, e.g. "dev@gpu-box".
	Describe() string
	// interactive reports whether the command may talk to the terminal:
	// ssh asking for a password or about a host key.
	interactive() bool
	// failed turns a failed run into the error a person should read.
	failed(err error, stderr string) error
	// forBridge is the transport to run the bridge with: its stdin and
	// stdout carry the protocol, so nothing may prompt on them.
	forBridge() Transport
}

// SSH reaches a machine over ssh. An interactive transport lets ssh prompt
// on the terminal; otherwise a run that needs a password fails instead of
// hanging.
func SSH(target string, interactive bool) Transport {
	return &sshTransport{target: target, opts: sshOpts{interactive: interactive}}
}

// sshKeyOnly logs in by key alone: no password, and no shared connection
// another command has already authenticated.
func sshKeyOnly(target string) Transport {
	return &sshTransport{target: target, opts: sshOpts{keyOnly: true}}
}

type sshTransport struct {
	target string
	opts   sshOpts
}

func (t *sshTransport) Command(ctx context.Context, script string) (*exec.Cmd, error) {
	return sshCmdWith(ctx, t.target, script, t.opts)
}

func (t *sshTransport) Describe() string { return t.target }

// An askpass answers ssh's prompt, so the run must not also wait for the
// terminal.
func (t *sshTransport) interactive() bool { return t.opts.interactive && t.opts.askpass == nil }

func (t *sshTransport) failed(err error, stderr string) error { return sshError(err, stderr) }

func (t *sshTransport) forBridge() Transport {
	next := &sshTransport{target: t.target, opts: t.opts}
	next.opts.interactive = false // an askpass, if any, still answers
	return next
}

// withPassword returns the same ssh transport with a short-lived askpass
// helper answering the password prompt, and the func that removes it.
func (t *sshTransport) withPassword(password string) (Transport, func(), error) {
	ap, err := newAskpass(password)
	if err != nil {
		return nil, nil, err
	}
	next := &sshTransport{target: t.target, opts: t.opts}
	next.opts.askpass = ap
	return next, ap.close, nil
}

// Exec reaches a machine by running a local command, such as
// `container exec -i NAME sh -c SCRIPT` for a sandbox. The script is
// appended to argv as one argument. name is what messages call the machine.
func Exec(name string, argv ...string) Transport {
	return &execTransport{name: name, argv: argv}
}

type execTransport struct {
	name string
	argv []string
}

func (t *execTransport) Command(ctx context.Context, script string) (*exec.Cmd, error) {
	if len(t.argv) == 0 {
		return nil, errors.New("this machine has no command to run things with")
	}
	args := append(append([]string{}, t.argv[1:]...), script)
	return exec.CommandContext(ctx, t.argv[0], args...), nil
}

func (t *execTransport) Describe() string {
	if t.name != "" {
		return t.name
	}
	return strings.Join(t.argv, " ")
}

func (t *execTransport) interactive() bool { return false }

func (t *execTransport) forBridge() Transport { return t }

// failed keeps the command's own message: there is no ssh advice to add.
func (t *execTransport) failed(err error, stderr string) error {
	if msg := strings.TrimSpace(stderr); msg != "" {
		if lines := strings.Split(msg, "\n"); len(lines) > 3 {
			msg = strings.Join(lines[len(lines)-3:], "\n")
		}
		return errors.New(msg)
	}
	return err
}

// runScript runs script on the machine and returns its stdout.
func runScript(ctx context.Context, tr Transport, script string, stdin []byte) ([]byte, error) {
	cmd, err := tr.Command(ctx, script)
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	interactive := tr.interactive()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	} else if interactive {
		cmd.Stdin = os.Stdin
	}
	if interactive {
		cmd.Stderr = os.Stderr // let ssh talk to the user
	}
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), tr.failed(err, stderr.String())
	}
	return stdout.Bytes(), nil
}
