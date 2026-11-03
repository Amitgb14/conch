package phone

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Amitgb14/conch/internal/config"
)

// GatewayArgs is how conch web is started again: with no flags when the
// settings say where (it reads them), else as it was last started. ok is
// false when neither says anything — it has never run here, and nothing
// says where it should listen.
func GatewayArgs(settingsURL string, s *Store) (args []string, ok bool) {
	if strings.TrimSpace(settingsURL) != "" {
		return nil, true
	}
	run, _ := s.Gateway()
	if run.Args == nil && run.Started.IsZero() {
		return nil, false
	}
	if run.Args == nil {
		return []string{}, true // started with no flags at all
	}
	return run.Args, true
}

// StartGateway starts conch web (exe) with args in the background: its
// own session, so it outlives whatever started it — the TUI, Conch.app,
// a shell — and its output in web.log beside the server's. start runs the
// command; the TUI and tests pass their own. `conch web stop` ends it.
func StartGateway(exe string, args []string, start func(*exec.Cmd) error) error {
	logf, err := os.OpenFile(filepath.Join(config.Dir(), "web.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, append([]string{"web"}, args...)...)
	cmd.Env = config.MergeEnv(os.Environ(), "CONCH_PANE_ID=")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := start(cmd); err != nil {
		return err
	}
	// This copy of the log closes here; the gateway keeps its.
	if cmd.Process != nil {
		go func() { _ = cmd.Wait() }() // reaped when it ends, not left a zombie
	}
	return nil
}
