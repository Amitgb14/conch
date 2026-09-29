package main

import (
	"errors"
	"fmt"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// actForHere carries an agent's scope to another machine. That machine's
// server sees a connection from outside its panes and would let it do
// anything, so when this server says the command runs in one of its agent
// panes, the connection is asked to be held to what that agent started
// there. A server there too old to do that is not driven at all.
//
// It is a guard like the scoping it carries, not a lock: it asks this
// machine's server, which knows from the kernel which pane is asking, but
// a program that skips conch and runs ssh itself is not asking.
func actForHere(remote *client.Client, machine string) error {
	who, err := localCaller()
	if err != nil || !who.Scoped {
		return err
	}
	if len(remote.MissingCapabilities([]string{proto.CapScopeRemote})) > 0 {
		return fmt.Errorf("the %s agent in %s may not drive %s: its conch server predates scoping, "+
			"so nothing would keep the agent to its own work there; upgrade it (conch machine upgrade %s) "+
			"or run this from the TUI or a terminal pane", who.Agent, who.Pane, machine, machine)
	}
	return call(remote, proto.MethodActFor, proto.ActForParams{ID: who.ID, Label: who.Label, Agent: who.Agent}, nil)
}

// localCaller asks this machine's server who the command is. No server
// here, or one without scoping, means nobody to scope: the command is not
// in an agent's pane that anything would hold it to.
func localCaller() (proto.CallerInfo, error) {
	c, err := client.Dial(config.SocketPath(), "conch-cli")
	if err != nil {
		return proto.CallerInfo{}, nil
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{proto.CapScopeRemote})) > 0 {
		return proto.CallerInfo{}, nil
	}
	var who proto.CallerInfo
	if err := call(c, proto.MethodPaneCaller, nil, &who); err != nil {
		var perr *proto.Error
		if errors.As(err, &perr) {
			return proto.CallerInfo{}, fmt.Errorf("asking this machine's conch who is running: %v", err)
		}
		return proto.CallerInfo{}, err
	}
	return who, nil
}
