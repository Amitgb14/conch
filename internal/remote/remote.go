package remote

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/amitghadge/conch/internal/buildinfo"
	"github.com/amitghadge/conch/internal/client"
	"github.com/amitghadge/conch/internal/proto"
)

// Probe is what a machine reported about itself.
type Probe struct {
	Platform string          // e.g. linux/amd64
	Bin      string          // path of an installed conch, "" if none
	Info     *buildinfo.Info // that binary's `conch version --json`
}

// probeScript prints the platform and the first conch binary it finds,
// preferring the one conch installs. Non-interactive ssh sessions often lack
// the user's PATH, hence the explicit location.
const probeScript = `uname -s; uname -m
for c in "$HOME/.local/bin/conch" "$(command -v conch 2>/dev/null)"; do
  if [ -n "$c" ] && [ -x "$c" ]; then echo "bin=$c"; "$c" version --json 2>/dev/null; break; fi
done`

// ProbeMachine inspects target.
func ProbeMachine(ctx context.Context, target string, interactive bool) (Probe, error) {
	out, err := run(ctx, target, probeScript, nil, interactive)
	if err != nil {
		return Probe{}, err
	}
	return parseProbe(string(out))
}

func parseProbe(out string) (Probe, error) {
	var p Probe
	sc := bufio.NewScanner(strings.NewReader(out))
	var lines []string
	for sc.Scan() {
		lines = append(lines, strings.TrimSpace(sc.Text()))
	}
	if len(lines) < 2 {
		return p, fmt.Errorf("unexpected probe output %q", out)
	}
	osName, arch := strings.ToLower(lines[0]), lines[1]
	switch arch {
	case "x86_64", "amd64":
		arch = "amd64"
	case "aarch64", "arm64":
		arch = "arm64"
	default:
		return p, fmt.Errorf("unsupported CPU %q", arch)
	}
	if osName != "linux" && osName != "darwin" {
		return p, fmt.Errorf("unsupported OS %q", lines[0])
	}
	p.Platform = osName + "/" + arch
	for _, l := range lines[2:] {
		switch {
		case strings.HasPrefix(l, "bin="):
			p.Bin = strings.TrimPrefix(l, "bin=")
		case strings.HasPrefix(l, "{"):
			var info buildinfo.Info
			if json.Unmarshal([]byte(l), &info) == nil {
				p.Info = &info
			}
		}
	}
	return p, nil
}

// Missing lists capabilities this client needs that the probed binary lacks.
// A binary too old to print its version lacks everything.
func (p Probe) Missing() []string {
	if p.Info == nil {
		return proto.Capabilities
	}
	have := map[string]bool{}
	for _, c := range p.Info.Capabilities {
		have[c] = true
	}
	var missing []string
	for _, c := range proto.Capabilities {
		if !have[c] {
			missing = append(missing, c)
		}
	}
	return missing
}

// InstallError means the machine needs conch installed or upgraded before
// it can be used. Background connections never install on their own.
type InstallError struct {
	Platform string
	Reason   string
}

func (e *InstallError) Error() string { return e.Reason }

// OutdatedServerError means the machine's running server predates this
// client. Restarting it stops its panes, so that is the user's call.
type OutdatedServerError struct {
	Missing []string
}

func (e *OutdatedServerError) Error() string {
	return "the server there is from an older build (lacks " + strings.Join(e.Missing, ", ") + "); restart it"
}

// Install copies a conch binary for the machine's platform to
// ~/.local/bin/conch there, replacing any older one atomically. A server
// already running keeps its old binary until it restarts. For a platform
// other than this computer's, conch is built from source when needed.
func Install(ctx context.Context, target, platform string, interactive bool, say func(string)) (string, error) {
	if say == nil {
		say = func(string) {}
	}
	bin, err := binaryFor(ctx, platform, say)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		return "", err
	}
	say(fmt.Sprintf("copying conch to %s (%d MB)", target, len(data)>>20))
	const script = `set -e; d="$HOME/.local/bin"; mkdir -p "$d"; cat > "$d/conch.new"; chmod 755 "$d/conch.new"; mv -f "$d/conch.new" "$d/conch"; echo "$d/conch"`
	out, err := run(ctx, target, script, data, false)
	if err != nil {
		return "", fmt.Errorf("install conch: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Options control Connect.
type Options struct {
	// Install lets Connect install or upgrade conch on the machine.
	Install bool
	// Interactive lets ssh prompt on the terminal.
	Interactive bool
	// Progress receives human-readable steps.
	Progress func(string)
}

// Connect reaches the conch server on target, starting it if needed. When
// the server is older than this client, the connected client is returned
// together with an *OutdatedServerError.
func Connect(ctx context.Context, target string, opts Options) (*client.Client, error) {
	say := opts.Progress
	if say == nil {
		say = func(string) {}
	}
	say("probing " + target)
	probe, err := ProbeMachine(ctx, target, opts.Interactive)
	if err != nil {
		return nil, err
	}
	bin := probe.Bin
	if missing := probe.Missing(); bin == "" || len(missing) > 0 {
		reason := fmt.Sprintf("conch is not installed on %s (%s)", target, probe.Platform)
		if bin != "" {
			reason = fmt.Sprintf("conch on %s is from an older build (lacks %s)", target, strings.Join(missing, ", "))
		}
		if !opts.Install {
			return nil, &InstallError{Platform: probe.Platform, Reason: reason}
		}
		say(reason + "; installing")
		if bin, err = Install(ctx, target, probe.Platform, opts.Interactive, say); err != nil {
			return nil, err
		}
	}

	say("connecting")
	c, err := Bridge(target, bin)
	if err != nil {
		return nil, err
	}
	if missing := c.MissingCapabilities(proto.Capabilities); len(missing) > 0 {
		return c, &OutdatedServerError{Missing: missing}
	}
	return c, nil
}

// Bridge runs `conch bridge` on target and speaks the protocol through it.
func Bridge(target, bin string) (*client.Client, error) {
	cmd, err := sshCmd(context.Background(), target, shellQuote(bin)+" bridge", false)
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &tail{max: 4096}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	conn := &bridgeConn{cmd: cmd, r: stdout, w: stdin, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(conn.done)
	}()
	c, err := client.New(conn, "conch-remote")
	if err != nil {
		conn.Close()
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, sshError(errors.New(msg), msg)
		}
		return nil, err
	}
	return c, nil
}

// bridgeConn is the protocol stream through a running ssh process.
type bridgeConn struct {
	cmd  *exec.Cmd
	r    io.Reader
	w    io.WriteCloser
	done chan struct{}
	once sync.Once
}

func (b *bridgeConn) Read(p []byte) (int, error)  { return b.r.Read(p) }
func (b *bridgeConn) Write(p []byte) (int, error) { return b.w.Write(p) }

func (b *bridgeConn) Close() error {
	b.once.Do(func() {
		_ = b.w.Close() // EOF on the bridge's stdin ends it
		select {
		case <-b.done:
		case <-time.After(2 * time.Second):
			_ = b.cmd.Process.Kill()
		}
	})
	return nil
}

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
