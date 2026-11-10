package cloudagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// The agent's own socket: how the things beside it on the machine ask for
// what only it holds, in memory. The conch server asks for an agent's
// env secrets as it starts one (cloud.agent.v1), and git's credential
// helper asks for the machine token to hand the git proxy (PROTOCOL §6).
// Neither is ever written to the machine's disk or put in an environment
// that outlives the one process it is for. The socket is the person's
// user's, 0600: that user's processes may ask, nobody else's.

// localAskWait bounds one ask, the control plane's answer included.
var localAskWait = 15 * time.Second

// listenLocal opens the agent's socket at path, for u.
func listenLocal(path string, u *User) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, errors.New(path + " is there and is not a socket")
		}
		_ = os.Remove(path) // a socket an earlier agent left
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	if !u.isMe() {
		if err := os.Chown(path, u.UID, u.GID); err != nil {
			ln.Close()
			return nil, err
		}
	}
	return ln, nil
}

// serveLocal answers on ln until it is closed.
func (a *Agent) serveLocal(ctx context.Context, ln net.Listener) {
	for {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		go a.answerLocal(ctx, nc)
	}
}

func (a *Agent) answerLocal(ctx context.Context, nc net.Conn) {
	defer nc.Close()
	_ = nc.SetDeadline(time.Now().Add(localAskWait + time.Second))
	r := bufio.NewReader(nc)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var ask proto.CloudAgentAsk
		var ans proto.CloudAgentAnswer
		if err := json.Unmarshal(line, &ask); err != nil {
			ans.Error = "not a question: " + err.Error()
		} else {
			ans = a.answer(ctx, ask)
		}
		b, _ := json.Marshal(ans)
		if _, err := nc.Write(append(b, '\n')); err != nil {
			return
		}
	}
}

// gitUser is the user name git sends the proxy with the token; the proxy
// reads only the password.
const gitUser = "conch"

func (a *Agent) answer(ctx context.Context, ask proto.CloudAgentAsk) proto.CloudAgentAnswer {
	switch ask.Op {
	case proto.CloudAskEnv:
		ctx, cancel := context.WithTimeout(ctx, localAskWait)
		defer cancel()
		env, err := a.api.Env(ctx)
		if err != nil {
			a.logf("cloud: env secrets for an agent: %v", err)
			return proto.CloudAgentAnswer{Error: "the control plane did not give this machine's secrets: " + err.Error()}
		}
		if env == nil {
			env = map[string]string{}
		}
		return proto.CloudAgentAnswer{Env: env}
	case proto.CloudAskGitCredential:
		return proto.CloudAgentAnswer{Username: gitUser, Password: a.api.Token()}
	}
	return proto.CloudAgentAnswer{Error: "unknown question " + ask.Op}
}

// Ask asks the agent's socket one question: what `conch cloud
// git-credential` does for git.
func Ask(socket string, ask proto.CloudAgentAsk) (proto.CloudAgentAnswer, error) {
	var ans proto.CloudAgentAnswer
	nc, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return ans, err
	}
	defer nc.Close()
	_ = nc.SetDeadline(time.Now().Add(localAskWait + 5*time.Second))
	b, _ := json.Marshal(ask)
	if _, err := nc.Write(append(b, '\n')); err != nil {
		return ans, err
	}
	line, err := bufio.NewReader(nc).ReadBytes('\n')
	if err != nil {
		return ans, err
	}
	if err := json.Unmarshal(line, &ans); err != nil {
		return ans, err
	}
	if ans.Error != "" {
		return ans, errors.New(ans.Error)
	}
	return ans, nil
}
