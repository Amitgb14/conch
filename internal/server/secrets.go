package server

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/agentsetup"
)

// The tokens an MCP server of the library needs, kept in this Mac's
// Keychain (CapLibraryKeychain). conch never writes a secret into an
// agent's files — a server there names a variable, Bearer ${TOKEN} — so
// the value has to be in the agent's environment, and a variable exported
// in a shell's rc file is not in the environment of a server Conch.app
// started, nor in an agent it starts. So the app puts the token in the
// Keychain under the service conch-mcp, named by its variable, and every
// pane this server starts is given each variable the library refers to
// that the Keychain has and the server's own environment does not.
//
// The Keychain is reached through /usr/bin/security, never a library of
// conch's own: an item made by security is one security may read with no
// prompt, whichever build of conch asks — a rebuilt conch is a different
// program to the Keychain, and an item it made would ask the person again
// after every build. CONCH_SECURITY replaces the binary in tests, so no
// test reaches the developer's Keychain.

// KeychainService is the Keychain service the tokens are kept under; the
// account is the variable's name.
const KeychainService = "conch-mcp"

// secretTTL is how long a token read from the Keychain is used before it
// is read again, and secretMissTTL how long a variable it lacks is not
// asked about again: a token added or changed in the app reaches the next
// agent after at most that long, and a pane started in a burst does not
// run security once per variable each.
const (
	secretTTL     = 5 * time.Minute
	secretMissTTL = 20 * time.Second
)

type secretCache struct {
	mu   sync.Mutex
	vals map[string]secretEntry
}

type secretEntry struct {
	value string
	at    time.Time
}

func securityBinary() string {
	if b := os.Getenv("CONCH_SECURITY"); b != "" {
		return b
	}
	return "/usr/bin/security"
}

// keychainHas says whether a server here can read the Keychain at all.
func keychainHas() bool { return runtime.GOOS == "darwin" || os.Getenv("CONCH_SECURITY") != "" }

// lookup is the variable's token from the Keychain, "" when it has none.
func (sc *secretCache) lookup(name string) string {
	sc.mu.Lock()
	if e, ok := sc.vals[name]; ok {
		ttl := secretTTL
		if e.value == "" {
			ttl = secretMissTTL
		}
		if time.Since(e.at) < ttl {
			sc.mu.Unlock()
			return e.value
		}
	}
	sc.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, securityBinary(), "find-generic-password", "-s", KeychainService, "-a", name, "-w").Output()
	v := ""
	if err == nil {
		v = strings.TrimRight(string(out), "\r\n")
	}
	sc.mu.Lock()
	if sc.vals == nil {
		sc.vals = map[string]secretEntry{}
	}
	sc.vals[name] = secretEntry{value: v, at: time.Now()}
	sc.mu.Unlock()
	return v
}

// forget drops what was read, so a token just saved or removed is read
// afresh by the next pane.
func (sc *secretCache) forget() {
	sc.mu.Lock()
	sc.vals = nil
	sc.mu.Unlock()
}

// secretEnv is NAME=token for each variable the library refers to that the
// Keychain holds and this server's environment does not: a variable
// somebody exported themselves is theirs and wins.
func (s *Server) secretEnv() []string {
	if !keychainHas() {
		return nil
	}
	lib, err := agentsetup.LoadLibrary()
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range agentsetup.LibraryVars(lib) {
		if os.Getenv(name) != "" {
			continue
		}
		if v := s.secrets.lookup(name); v != "" {
			out = append(out, name+"="+v)
		}
	}
	return out
}
