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

type cloudAgent struct {
	mu     sync.Mutex
	owner  *client
	socket string
}

// cloudAskWait is how long an agent's start waits for its secrets: a
// control plane that does not answer must not hold up a pane for long,
// and the agent starts without them.
var cloudAskWait = 10 * time.Second

func (ca *cloudAgent) register(c *client, socket string) *proto.Error {
	if socket == "" || !filepath.IsAbs(socket) {
		return proto.Errorf(proto.ErrBadRequest, "cloud agent socket %q is not an absolute path", socket)
	}
	ca.mu.Lock()
	ca.owner, ca.socket = c, socket
	ca.mu.Unlock()
	log.Printf("cloud agent registered at %s", socket)
	return nil
}

// forget drops the registration when its connection ends.
func (ca *cloudAgent) forget(c *client) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if ca.owner == c {
		ca.owner, ca.socket = nil, ""
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

// env is NAME=value for each env-mode secret, asked of the cloud agent now,
// or nothing — with the reason logged, never the values — when there is no
// cloud agent or it cannot say.
func (ca *cloudAgent) env() []string {
	ca.mu.Lock()
	socket := ca.socket
	ca.mu.Unlock()
	if socket == "" {
		return nil
	}
	ans, err := askCloudAgent(socket, proto.CloudAgentAsk{Op: proto.CloudAskEnv}, cloudAskWait)
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
		if envName.MatchString(k) {
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
