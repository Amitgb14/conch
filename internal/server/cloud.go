package server

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// On a Conch Cloud machine, `conch cloud agent` runs beside this server and
// registers its own socket here (cloud.agent.register). An agent this
// server starts is then given the account's env-mode secrets, which the
// cloud agent fetches from the control plane for that start (PROTOCOL §7):
// they reach that agent's environment and nothing else — not the server's
// own environment, not a file, not a pane that is only a shell. The
// registration is the connection's: when the cloud agent goes, so does
// cloud.agent.v1.
//
// Whoever registers decides what every agent's environment holds, so the
// registration is guarded: never from inside a pane, never over a live
// one — except by root, which is what the real cloud agent runs as and
// nothing in a pane can be. And the registration carries a key the cloud
// agent made, which the server sends with each ask, so that its socket
// gives the secrets to this server and not to any process of the same
// user that finds it. That keeps them out of a shell's reach on demand;
// it cannot hide them from that user once an agent holds them, since a
// process's environment is readable by its user (/proc/PID/environ).

type cloudAgent struct {
	mu     sync.Mutex
	owner  *client
	socket string
	key    string
}

// cloudAskWait is how long an agent's start waits for its secrets: a
// control plane that does not answer must not hold up a pane for long,
// and the agent starts without them.
var cloudAskWait = 10 * time.Second

func (ca *cloudAgent) register(c *client, p proto.CloudAgentRegisterParams) *proto.Error {
	if p.Socket == "" || !filepath.IsAbs(p.Socket) {
		return proto.Errorf(proto.ErrBadRequest, "cloud agent socket %q is not an absolute path", p.Socket)
	}
	c.mu.Lock()
	actFor := c.actFor.ID
	c.mu.Unlock()
	if c.pane != "" || actFor != "" {
		// Even a shell's: an agent types into shells it started.
		return proto.Errorf(proto.ErrOutOfScope, "a cloud agent is not registered from inside a pane: it would decide what every agent's environment holds")
	}
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if ca.owner != nil && ca.owner != c && !(c.root && !ca.owner.root) {
		return proto.Errorf(proto.ErrCloudAgentHeld, "another connection is registered as this machine's cloud agent")
	}
	ca.owner, ca.socket, ca.key = c, p.Socket, p.Key
	log.Printf("cloud agent registered at %s", p.Socket)
	return nil
}

// forget drops the registration when its connection ends.
func (ca *cloudAgent) forget(c *client) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if ca.owner == c {
		ca.owner, ca.socket, ca.key = nil, "", ""
		log.Printf("cloud agent gone")
	}
}

func (ca *cloudAgent) registered() bool {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	return ca.owner != nil
}

// envName is a variable a shell can carry; anything else is not passed.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// envReserved are names a secret may not take: conch's own variables,
// which come before the secrets and would lose to them (CONCH_PANE_ID is
// how hooks say which pane they are), and those that change what a
// program loads or where it looks rather than what it is told.
var envReserved = regexp.MustCompile(`^(CONCH_.*|LD_.*|DYLD_.*|PATH|HOME|USER|LOGNAME|SHELL|TERM|BASH_ENV|ENV|NODE_OPTIONS|PYTHONSTARTUP|PYTHONPATH|PERL5OPT|RUBYOPT)$`)

// env is NAME=value for each env-mode secret, asked of the cloud agent now,
// or nothing — with the reason logged, never the values — when there is no
// cloud agent or it cannot say.
func (ca *cloudAgent) env() []string {
	ca.mu.Lock()
	socket, key := ca.socket, ca.key
	ca.mu.Unlock()
	if socket == "" {
		return nil
	}
	ans, err := askCloudAgent(socket, proto.CloudAgentAsk{Op: proto.CloudAskEnv, Key: key}, cloudAskWait)
	if err != nil {
		log.Printf("cloud agent: no secrets for this agent: %v", err)
		return nil
	}
	if ans.Error != "" {
		log.Printf("cloud agent: no secrets for this agent: %s", ans.Error)
		return nil
	}
	names := make([]string, 0, len(ans.Env))
	for k := range ans.Env {
		if envName.MatchString(k) && !envReserved.MatchString(k) {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, k := range names {
		out = append(out, k+"="+ans.Env[k])
	}
	return out
}

// askCloudAgent asks the cloud agent's socket one question.
func askCloudAgent(socket string, ask proto.CloudAgentAsk, wait time.Duration) (proto.CloudAgentAnswer, error) {
	var ans proto.CloudAgentAnswer
	nc, err := net.DialTimeout("unix", socket, wait)
	if err != nil {
		return ans, err
	}
	defer nc.Close()
	_ = nc.SetDeadline(time.Now().Add(wait))
	b, _ := json.Marshal(ask)
	if _, err := nc.Write(append(b, '\n')); err != nil {
		return ans, err
	}
	line, err := bufio.NewReader(nc).ReadBytes('\n')
	if err != nil {
		return ans, err
	}
	return ans, json.Unmarshal(line, &ans)
}
