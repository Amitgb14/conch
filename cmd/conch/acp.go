package main

import "github.com/Amitgb14/conch/internal/acp"

// `conch acp` is what runs in the pane of an agent that speaks the Agent
// Client Protocol (internal/acp): the protocol's client, drawing the
// conversation and taking typed prompts. The server starts it from a
// manifest's [acp] table; it needs no server itself, which is why a
// person can run one in any terminal to try an agent.
func runACP(args []string) error { return acp.Main(args) }
