package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// conch status is read two ways. A script or an agent pipes it and parses
// the columns, so piped it is the table it has always been, column for
// column (statusWide). A person reads it in a terminal, where that table —
// every pane's size, full folder and sixty characters of command — ran
// past the edge of an ordinary window and pushed the state, the one thing
// looked for, out of reach. So in a terminal it fits the window: what is
// running and how it stands first, the folder from ~, the command in
// whatever room is left, and a line saying how many agents are working.

const statusUsage = "usage: conch status [-wide] [-json]"

// terminalWidth is the width of the terminal stdout is, or 0 when it is
// not one; tests replace it.
var terminalWidth = func() int {
	if !stdoutIsTerminal() {
		return 0
	}
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		return 0
	}
	return w
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	wide := fs.Bool("wide", false, "every column, as it is printed to a pipe")
	asJSON := fs.Bool("json", false, "the server and its panes as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 || *wide && *asJSON {
		return errors.New(statusUsage)
	}
	c, err := connect(false)
	if err != nil {
		if machineFlag != "" && machineFlag != "local" {
			return err // why the machine can't be reached, not "not running"
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"server": nil, "panes": []proto.PaneInfo{}})
		}
		// A socket still in place after the server stopped serving — one
		// wedged partway through a reload — is not the same as no server.
		if _, serr := os.Stat(config.SocketPath()); serr == nil {
			fmt.Println("server: not answering; its socket is still there but nothing serves it")
			fmt.Println("run `conch` to start a fresh server (the old panes are lost)")
			return nil
		}
		fmt.Println("server: not running")
		return nil
	}
	defer c.Close()
	var list proto.PaneList
	listErr := call(c, proto.MethodPaneList, nil, &list)
	if *asJSON {
		if listErr != nil {
			return listErr
		}
		if list.Panes == nil {
			list.Panes = []proto.PaneInfo{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"server": c.Server, "panes": list.Panes})
	}
	fmt.Printf("server: running  version %s  pid %d  up %s%s\n",
		c.Server.Version, c.Server.PID, time.Since(c.Server.Started).Round(time.Second), reloadedNote(c.Server))
	if listErr != nil {
		return listErr
	}
	if len(list.Panes) == 0 {
		fmt.Println("no panes")
		return nil
	}
	width := terminalWidth()
	if *wide || width == 0 {
		return statusWide(list.Panes)
	}
	fmt.Print(statusFitted(list.Panes, c.Server.Home, width))
	return nil
}

// statusWide is the table a script reads. Its columns are a contract.
func statusWide(panes []proto.PaneInfo) error {
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATE\tAGENT\tSSH\tSIZE\tCWD\tCOMMAND")
	for _, p := range panes {
		state := p.State
		if p.State == proto.PaneExited {
			state = fmt.Sprintf("exited(%d)", p.ExitCode)
		}
		agent := "-"
		if p.Agent != nil {
			agent = p.Agent.Name + ":" + p.Agent.State
		}
		// Where a terminal is logged in with ssh now — what -ssh on upload
		// and download reaches — rather than what it was started with.
		ssh := "-"
		if p.SSH != "" {
			ssh = p.SSH
		}
		command := statusCommand(p)
		if r := []rune(command); len(r) > 60 {
			command = string(r[:59]) + "…"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%dx%d\t%s\t%s\n",
			p.ID, p.Name, state, agent, ssh, p.Cols, p.Rows, p.Cwd, command)
	}
	return tw.Flush()
}

// statusCommand is a pane's command on one line: scripts span lines.
func statusCommand(p proto.PaneInfo) string {
	return strings.Join(strings.Fields(strings.Join(p.Command, " ")), " ")
}

// statusFitted is the table for a person, no line wider than width.
func statusFitted(panes []proto.PaneInfo, home string, width int) string {
	var b strings.Builder
	if line := statusSummary(panes); line != "" {
		b.WriteString(fitLine(line, width) + "\n")
	}
	anySSH := false
	for _, p := range panes {
		anySSH = anySSH || p.SSH != ""
	}
	head := []string{"ID", "NAME", "AGENT", "STATE"}
	if anySSH {
		head = append(head, "SSH")
	}
	head = append(head, "FOLDER", "COMMAND")
	rows := [][]string{head}
	for _, p := range panes {
		agent, state := "", p.State
		switch {
		case p.State == proto.PaneExited:
			state = fmt.Sprintf("exited(%d)", p.ExitCode)
			if p.Agent != nil {
				agent = p.Agent.Name
			}
		case p.Agent != nil:
			agent, state = p.Agent.Name, agentWord(p.Agent.State)
		}
		row := []string{p.ID, p.Name, agent, state}
		if anySSH {
			row = append(row, p.SSH)
		}
		command := ""
		if p.Agent == nil { // an agent's command is the agent, already said
			command = statusCommand(p)
		}
		rows = append(rows, append(row, tildePath(p.Cwd, home), command))
	}
	widths := make([]int, len(head))
	for _, r := range rows {
		for i, cell := range r {
			widths[i] = max(widths[i], len([]rune(cell)))
		}
	}
	folder, command := len(head)-2, len(head)-1
	used := func() int {
		n := 0
		for i := range widths[:command] {
			n += widths[i] + 2
		}
		return n
	}
	// Room goes to what tells panes apart first. The command gets what is
	// left, and none when that is too little to say anything — the folder
	// is worth more than "/bin/sh -lc…". Then the folder gives up its
	// start (the end of a path is the part that names it), and a long name
	// is cut last.
	for _, col := range []struct{ i, least int }{{folder, 12}, {1, 8}} {
		if over := used() - width; over > 0 {
			widths[col.i] = max(widths[col.i]-over, min(widths[col.i], col.least))
		}
	}
	if room := width - used(); room >= 16 || room >= widths[command] {
		widths[command] = max(min(widths[command], room), 0)
	} else {
		widths[command] = 0
	}
	for _, r := range rows {
		var cells []string
		for i, cell := range r {
			if widths[i] == 0 {
				continue
			}
			if i == folder {
				cell = cutStart(cell, widths[i])
			} else {
				cell = cutEnd(cell, widths[i])
			}
			if i < len(r)-1 {
				cell += strings.Repeat(" ", widths[i]-len([]rune(cell)))
			}
			cells = append(cells, cell)
		}
		b.WriteString(fitLine(strings.TrimRight(strings.Join(cells, "  "), " "), width) + "\n")
	}
	return b.String()
}

// statusSummary says how the agents stand, in a line: what somebody
// running status is usually asking.
func statusSummary(panes []proto.PaneInfo) string {
	count := map[string]int{}
	agents := 0
	for _, p := range panes {
		if p.Agent == nil || p.State != proto.PaneRunning {
			continue
		}
		agents++
		count[agentWord(p.Agent.State)]++
	}
	if agents == 0 {
		return ""
	}
	var parts []string
	known := 0
	for _, st := range []string{"working", "waiting", proto.AgentDone, proto.AgentIdle} {
		if n := count[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, st))
			known += n
		}
	}
	if n := agents - known; n > 0 { // states a newer server knows
		parts = append(parts, fmt.Sprintf("%d other", n))
	}
	return counted(agents, "agent") + ": " + strings.Join(parts, ", ")
}

// agentWord is an agent's state as the TUI says it: blocked is an agent
// waiting for its person.
func agentWord(state string) string {
	if state == proto.AgentBlocked {
		return "waiting"
	}
	return state
}

// tildePath is a folder from ~ when it is under home.
func tildePath(path, home string) string {
	switch {
	case home == "" || home == "/":
		return path
	case path == home:
		return "~"
	case strings.HasPrefix(path, home+"/"):
		return "~" + path[len(home):]
	}
	return path
}

// cutEnd keeps the start of s in n columns, cutStart the end.
func cutEnd(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func cutStart(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[len(r)-n:])
	}
	return "…" + string(r[len(r)-n+1:])
}

// fitLine is the last word on width: no line is wider than the terminal,
// however narrow.
func fitLine(s string, width int) string { return cutEnd(s, width) }
