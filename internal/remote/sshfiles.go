package remote

import (
	"fmt"
	"path/filepath"
	"strings"
)

// A terminal where somebody typed `ssh host` is a way onto that host, and
// its Files view reaches the host's files through a session of conch's own
// made with what was typed: the same host, user, port, key, jump host and
// config. Nothing on the host is needed but its sftp server, which OpenSSH
// has by default.

// Login is an ssh command line read back: the host it logs in to, and the
// options a second session to that host needs.
type Login struct {
	Host string   // as typed, user@ included
	Opts []string // the options kept, each with its value
}

// sshArgOpts are ssh's options that take a value (ssh(1)).
const sshArgOpts = "BbcDEeFIiJLlmOoPpQRSWw"

// keptArgOpts are those a session for files keeps: where and how to log in.
// Forwarding (-D -L -R -W), a log file (-E), an escape character (-e), a
// tun device (-w), a control command (-O) and a query (-Q) are for the
// terminal's own session, and some — a second -L on the same port — would
// make this one fail.
const keptArgOpts = "BbcFIiJlmoPpS"

// keptFlags are the flags without a value that are kept: the address family
// and compression. -t, -N, -f, -A, -X and the rest belong to the terminal.
const keptFlags = "46C"

// droppedSettings are -o settings that would give the files session a
// terminal, a command, forwards or a command run here.
var droppedSettings = map[string]bool{
	"requesttty": true, "remotecommand": true, "localcommand": true, "permitlocalcommand": true,
	"localforward": true, "remoteforward": true, "dynamicforward": true, "forwardagent": true,
	"forwardx11": true, "forwardx11trusted": true, "sessiontype": true, "stdinnull": true,
	"forkafterauthentication": true, "batchmode": true, "tunnel": true, "tunneldevice": true,
}

// ParseLogin reads an ssh command line — the program and its arguments, as
// the process table has them — and says which host it logs in to. It is
// false for anything that is not ssh logging in somewhere: another
// program, ssh -V, -G, -Q or -O, or no host.
func ParseLogin(argv []string) (Login, bool) {
	if len(argv) < 2 || filepath.Base(argv[0]) != "ssh" {
		return Login{}, false
	}
	var l Login
	args := argv[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 < len(args) {
				l.Host = args[i+1]
			}
			break
		}
		if len(a) < 2 || a[0] != '-' {
			l.Host = a
			break
		}
		for j := 1; j < len(a); j++ {
			c := a[j]
			if strings.IndexByte(sshArgOpts, c) < 0 {
				switch {
				case c == 'G' || c == 'V':
					return Login{}, false // prints and exits
				case strings.IndexByte(keptFlags, c) >= 0:
					l.Opts = append(l.Opts, "-"+string(c))
				}
				continue
			}
			v := a[j+1:]
			if v == "" {
				if i+1 >= len(args) {
					return Login{}, false
				}
				i++
				v = args[i]
			}
			switch {
			case c == 'O' || c == 'Q':
				return Login{}, false // a control command or a query
			case c == 'o' && droppedSettings[settingName(v)]:
			case strings.IndexByte(keptArgOpts, c) >= 0:
				l.Opts = append(l.Opts, "-"+string(c), v)
			}
			break
		}
	}
	if CheckLoginTarget(l.Host) != nil {
		return Login{}, false
	}
	return l, true
}

// settingName is the name of an -o setting, lower case: "Port=22" and
// "Port 22" both name port.
func settingName(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.IndexAny(v, "= \t"); i >= 0 {
		v = v[:i]
	}
	return strings.ToLower(v)
}

// RunCommand is the ssh command that runs argv on the host the login
// reached, in a terminal: the same options, and here a person is there to
// answer a password. Each word is single-quoted for the host's login
// shell, which reads it as one word in sh, bash, zsh and fish alike.
func RunCommand(l Login, argv []string) []string {
	cmd := []string{sshBinary()}
	cmd = append(cmd, l.Opts...)
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return append(cmd, "-t", "--", l.Host, strings.Join(quoted, " "))
}

// FilesCommand is the ssh command that opens an sftp session to the host
// the login reached. It never asks anything — a host that wants a password
// says so and fails, since nobody is there to type it — and asks for no
// forwards, whatever the config says. A config that shares connections
// (ControlMaster) shares the terminal's, so no second login happens at all.
func FilesCommand(l Login) []string {
	cmd := []string{sshBinary()}
	cmd = append(cmd, l.Opts...)
	return append(cmd, "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", "-o", "RequestTTY=no",
		"-s", "--", l.Host, "sftp")
}

// MachineOpts is the extra ssh options a machine may be reached with. The
// list is first checked as any login's is (NormalizeLoginArgs), so a word
// that is not an option, or an option with its value missing, is refused
// where it can still be said. What is left is then read by ParseLogin
// itself, against a host of its own, and so keeps exactly the set a second
// session to a host keeps: where and how to log in. A machine's
// connections are not a terminal's session — conch runs a probe, an
// install and a bridge on them — so a forward, a tty, an escape character
// and agent forwarding are dropped rather than refused: each is a legal
// ssh option that means nothing here, and a second -L on a port in use
// would make the connection fail outright.
func MachineOpts(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	args, err := NormalizeLoginArgs(args)
	if err != nil {
		return nil, err
	}
	argv := append([]string{"ssh"}, args...)
	l, ok := ParseLogin(append(argv, "--", optsHost))
	if !ok {
		return nil, fmt.Errorf("%s are not ssh options a machine can be reached with", strings.Join(args, " "))
	}
	return l.Opts, nil
}

// optsHost stands in for the host while MachineOpts reads options, so
// ParseLogin has the login it insists on. It is never connected to.
const optsHost = "host"
