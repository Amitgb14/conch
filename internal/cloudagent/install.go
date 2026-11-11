package cloudagent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// UnitName is the systemd unit the control plane restarts after every
// inject (conch-cloud's sshx boot script: systemctl restart
// conch-cloud-agent).
const UnitName = "conch-cloud-agent.service"

// Unit is the systemd unit that runs exe as the cloud agent.
//
// KillMode=process because the agent starts the machine's conch server,
// which is the person's — its panes are their agents — and every inject
// restarts the agent: with the default, systemd would end the server and
// every pane with it. Exit statuses 3 (the token was refused) and 4 (the
// machine was) are not restarted, since restarting cannot change them and
// the next inject restarts the unit anyway; a missing boot.json is waited
// for, so the unit can be enabled at boot, before the host has injected one.
// Anything else (a boot.json it refuses, say) is restarted, 5 s apart at
// first and up to 5 minutes apart (systemd 254 and later; an older one
// warns about the two keys and keeps 5 s).
func Unit(exe string) string {
	return `[Unit]
Description=Conch Cloud agent: this machine's side of Conch Cloud
Documentation=https://amitgb14.github.io/conch/docs/cloud-machine
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=` + exe + ` cloud agent
Restart=on-failure
RestartSec=5
RestartSteps=6
RestartMaxDelaySec=300
RestartPreventExitStatus=3 4
KillMode=process

[Install]
WantedBy=multi-user.target
`
}

// InstallOptions are where Install writes; the zero value is the machine's.
type InstallOptions struct {
	Executable string // conch; default this one
	UnitDir    string // default /etc/systemd/system
	StateDir   string // default DefaultStateDir
	User       string // the machine's user, which must exist; default conch
	// Systemctl runs systemctl; nil runs the real one, and is skipped on a
	// machine without systemd.
	Systemctl func(ctx context.Context, args ...string) error
}

// Install makes this machine one the control plane can start an agent on:
// the unit, enabled, and the state directory. It is what a machine image
// runs once (scripts/cloud-image.sh); it does not start the agent, which
// waits for boot.json anyway.
func Install(ctx context.Context, o InstallOptions) (string, error) {
	if o.Executable == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		o.Executable = exe
	}
	if o.UnitDir == "" {
		o.UnitDir = "/etc/systemd/system"
	}
	if o.StateDir == "" {
		o.StateDir = DefaultStateDir
	}
	if o.User == "" {
		o.User = "conch"
	}
	if _, err := LookupUser(o.User); err != nil {
		return "", fmt.Errorf("the machine's user %s: %w (make it first: useradd --create-home --shell /bin/bash %s)", o.User, err, o.User)
	}
	if strings.ContainsAny(o.Executable, " \t\n\"'\\") {
		return "", fmt.Errorf("%q: put conch somewhere without spaces or quotes in its path (/usr/local/bin/conch)", o.Executable)
	}
	if err := os.MkdirAll(o.StateDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(o.StateDir, 0o700); err != nil {
		return "", err
	}
	unit := Unit(o.Executable)
	if o.User != "conch" {
		unit = strings.Replace(unit, " cloud agent\n", " cloud agent -user "+o.User+"\n", 1)
	}
	path := filepath.Join(o.UnitDir, UnitName)
	if err := os.MkdirAll(o.UnitDir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return "", err
	}
	sc := o.Systemctl
	if sc == nil {
		if _, err := os.Stat("/run/systemd/system"); err != nil {
			return path, nil // no systemd: the inject script starts the agent itself
		}
		sc = systemctl
	}
	if err := sc(ctx, "daemon-reload"); err != nil {
		return path, err
	}
	return path, sc(ctx, "enable", UnitName)
}

func systemctl(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}
