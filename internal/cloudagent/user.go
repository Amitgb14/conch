package cloudagent

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
)

// User is who the person's side of the machine runs as: the conch server,
// its panes, and the scripts /exec runs (PROTOCOL §3.4, §4: user conch,
// home /home/conch). The agent itself runs as root under systemd, since
// boot.json and the system's git configuration are root's; everything it
// starts for the person runs as this user instead.
type User struct {
	Name  string
	UID   int
	GID   int
	Home  string
	Shell string
	// Groups are the user's supplementary groups (docker, say), which a
	// process started as it keeps: without them Go clears them all.
	Groups []uint32
}

// passwdPath is where a user's login shell is read from; a variable so a
// test can give one.
var passwdPath = "/etc/passwd"

// LookupUser is name as the system knows it.
func LookupUser(name string) (*User, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return nil, err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, fmt.Errorf("user %s: uid %q", name, u.Uid)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, fmt.Errorf("user %s: gid %q", name, u.Gid)
	}
	var groups []uint32
	if ids, err := u.GroupIds(); err == nil {
		for _, id := range ids {
			if g, err := strconv.ParseUint(id, 10, 32); err == nil {
				groups = append(groups, uint32(g))
			}
		}
	}
	return &User{Name: u.Username, UID: uid, GID: gid, Home: u.HomeDir, Shell: loginShell(u.Username), Groups: groups}, nil
}

// CurrentUser is the user this process runs as.
func CurrentUser() (*User, error) {
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	me, err := LookupUser(u.Username)
	if err != nil {
		return nil, err
	}
	if os.Getenv("SHELL") != "" {
		me.Shell = os.Getenv("SHELL")
	}
	if os.Getenv("HOME") != "" {
		me.Home = os.Getenv("HOME")
	}
	return me, nil
}

// loginShell is name's shell from the passwd file, /bin/sh when it has none.
func loginShell(name string) string {
	f, err := os.Open(passwdPath)
	if err != nil {
		return "/bin/sh"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) >= 7 && fields[0] == name && fields[6] != "" {
			return fields[6]
		}
	}
	return "/bin/sh"
}

// isMe says whether u is this process's own user, so nothing need change
// to run as it.
func (u *User) isMe() bool { return u == nil || u.UID == os.Geteuid() }

// credential is what a process started as u is given, or nil for this
// process's own user.
func (u *User) credential() *syscall.SysProcAttr {
	if u.isMe() {
		return nil
	}
	return &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(u.UID), Gid: uint32(u.GID), Groups: u.Groups}}
}

// env is a login environment for u: what ssh would give it, plus extra.
func (u *User) env(extra ...string) []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	base := []string{"HOME=" + u.Home, "USER=" + u.Name, "LOGNAME=" + u.Name, "SHELL=" + u.Shell, "PATH=" + path,
		"LANG=" + firstSet(os.Getenv("LANG"), "C.UTF-8"), "TERM=" + firstSet(os.Getenv("TERM"), "xterm-256color")}
	if u.isMe() {
		// This process's own environment, less what is the agent's alone.
		return config.MergeEnv(withoutAgentVars(os.Environ()), append(base, extra...)...)
	}
	return config.MergeEnv(base, extra...)
}

// agentOnly are variables of this process that nothing it starts may see.
var agentOnly = []string{"CONCH_SOCKET", "CONCH_PANE_ID", "NOTIFY_SOCKET", "INVOCATION_ID", "JOURNAL_STREAM"}

func withoutAgentVars(env []string) []string {
	out := env[:0:0]
outer:
	for _, kv := range env {
		for _, k := range agentOnly {
			if strings.HasPrefix(kv, k+"=") {
				continue outer
			}
		}
		out = append(out, kv)
	}
	return out
}

func firstSet(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// serverStartWait is how long a server started here has to answer.
var serverStartWait = 15 * time.Second

// ensureServer makes sure a conch server answers on sock, starting one as
// u if not. For this process's own user that is client.EnsureServer, as
// every conch command does; for another it is the same server started
// with that user's credentials and home, its log in that user's conch
// directory.
func ensureServer(exe, sock, logPath string, u *User) error {
	if u.isMe() {
		return client.EnsureServer(sock, logPath)
	}
	if c, err := client.Dial(sock, "conch-cloud-agent-probe"); err == nil {
		c.Close()
		return nil
	}
	cmd := serverCommand(exe, sock, logPath, u)
	cmd.SysProcAttr = u.credential()
	cmd.SysProcAttr.Setsid = true
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start conch server as %s: %w", u.Name, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(serverStartWait)
	for {
		if c, err := client.Dial(sock, "conch-cloud-agent-probe"); err == nil {
			c.Close()
			return nil
		}
		select {
		case err := <-exited:
			return fmt.Errorf("conch server exited as it started (%v); see %s", err, logPath)
		case <-deadline:
			return fmt.Errorf("conch server did not answer within %s; see %s", serverStartWait, logPath)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// serverScript makes the server's directories, opens its log and runs it
// ($1 server, socket $2, log $3) — all as the user it is started as. The
// agent is root and those paths are in the user's home, where any of the
// user's processes can leave a symlink: root opening the log, or chowning
// it or a directory, once followed server.log to /etc/passwd and handed it
// to the user. So root touches nothing there, and the server removes a
// stale socket itself, as it always does.
const serverScript = `umask 077 && mkdir -p -- "$(dirname -- "$2")" "$(dirname -- "$3")" && exec "$1" server >>"$3" 2>&1`

// serverCommand is conch server started through serverScript as u, not
// yet given u's credentials.
func serverCommand(exe, sock, logPath string, u *User) *exec.Cmd {
	cmd := exec.Command("/bin/sh", "-c", serverScript, "conch-server", exe, sock, logPath)
	cmd.Dir = u.Home
	cmd.Env = u.env("CONCH_SOCKET=" + sock)
	return cmd
}

// serverSocket is where u's conch server listens: as config.SocketPath
// says for this process's own user, and in u's home for another.
func serverSocket(u *User) string {
	if u.isMe() {
		return config.SocketPath()
	}
	return filepath.Join(u.Home, ".config", "conch", "conch.sock")
}
