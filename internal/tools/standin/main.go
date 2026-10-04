// Command standin is a program conch detects as an agent, for driving the
// paths that need one without running one:
//
//	go build -o /tmp/fake/claude ./internal/tools/standin
//	conch new -name main -- /tmp/fake/claude
//
// conch decides which agent runs in a pane from the name of the pane's
// foreground process (internal/detect/manifests/*.toml, process_names), so
// the **name it is built under is the whole trick**: built as `claude` it is
// a Claude Code pane, as `codex` a Codex one. It is not an agent and says
// nothing to any model; it exists so the tree, the sections, the state
// column and anything else that asks "is this an agent" can be exercised
// without a login, a key, or a plan window spent on a test.
//
// It stays the pane's foreground process and runs what is typed as a child,
// which is the other half of why it is useful: a pane started from inside it
// gets its created_by, the way an agent starting a helper does, so the
// tree's helper rows and ⑂N counts can be driven at all. A program that
// exec'd a shell instead would hand the foreground over and stop being
// detected — which is the mistake this file exists to stop anyone making
// twice.
//
// Nothing here imitates an agent's screen. An agent's states are read from
// what it prints, and a stand-in printing another program's words would
// make the detection rules agree with a fiction rather than with the agent.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	fmt.Printf("stand-in for %s · not an agent; nothing is sent to any model\n", name())
	in := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for in.Scan() {
		if line := in.Text(); line != "" {
			c := exec.Command("/bin/sh", "-c", line)
			c.Stdout, c.Stderr = os.Stdout, os.Stderr
			_ = c.Run()
		}
		fmt.Print("> ")
	}
}

// name is what this was built as, which is what conch detects it by.
func name() string {
	if len(os.Args) == 0 {
		return "an agent"
	}
	for i := len(os.Args[0]) - 1; i >= 0; i-- {
		if os.Args[0][i] == '/' {
			return os.Args[0][i+1:]
		}
	}
	return os.Args[0]
}
