// Command conch is a terminal orchestrator for AI coding agents.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
	"github.com/Amitgb14/conch/internal/tui"
	"github.com/Amitgb14/conch/internal/update"
)

const usage = `conch — terminal orchestrator for AI coding agents

Usage:
  conch                         open the TUI (starts the server if needed)
  conch server                  run the server in the foreground
  conch server stop             stop the server and every pane it owns
  conch server reload           run a new conch build without stopping panes
  conch status                  show server and pane status
  conch new [-cwd DIR] [-name N] -- CMD [ARGS...]
                                start a pane
  conch send ID TEXT            type TEXT into a pane (-keys to send key names)
  conch read ID                 print a pane's visible screen
  conch close ID                close a pane
  conch redraw ID               draw a pane's screen again (after a program left stale text)
  conch new -agent claude [-cwd DIR] [-- CLAUDE ARGS...]
                                start Claude Code with state tracking
  conch agent explain ID        show how a pane's agent state was decided
  conch agent status | install claude
                                check or install Claude Code (use -m for a machine)
  conch project add PATH | create [-no-git] PATH | ls | rm ID
                                manage projects shown in the sidebar
  conch task [-cwd DIR] [-branch B] [-base B] PROMPT
                                new branch + worktree + Claude with PROMPT
  conch branch commit [-file PATH]... -m MESSAGE
                                commit a task branch's changes (-cwd, -branch pick it)
  conch branch push | pr [-title T] [-draft] | merge [-no-squash] | discard [-force]
                                finish a branch: push, open a pull request, merge into
                                the base, or delete it and its worktree
  conch worktree ls | clean [-y] [-force] [PATH...]
                                list leftover worktrees, or remove the finished ones
  conch machine add [-label L] SSH_TARGET
                                add a remote machine (installs conch there)
  conch machine ls | rm ID | rename ID LABEL | upgrade ID | hosts
  conch -m MACHINE COMMAND      run a command against a remote machine
  conch -m MACHINE upload FILE...
                                copy files to a machine's uploads folder and print their paths there
  conch ask [-y | -n] REQUEST   ask the brain; it proposes actions and runs them once you confirm
  conch update [VERSION]        replace this binary with the latest (or given) release
  conch version [--json]        print version
`

func main() {
	buildinfo.Build() // hash the executable now, before a rebuild can replace it
	buildinfo.ResolveVersion()
	log.SetFlags(log.LstdFlags)
	args := os.Args[1:]
	for len(args) >= 2 && (args[0] == "-m" || args[0] == "--machine") {
		machineFlag, args = args[1], args[2:]
	}
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}

	var err error
	switch cmd {
	case "":
		err = runTUI()
	case "server":
		err = runServer(args)
	case "status", "ls":
		err = runStatus()
	case "new":
		err = runNew(args)
	case "send":
		err = runSend(args)
	case "read":
		err = runRead(args)
	case "close":
		err = runClose(args)
	case "redraw":
		err = runRedraw(args)
	case "agent":
		err = runAgent(args)
	case "project":
		err = runProject(args)
	case "task":
		err = runTask(args)
	case "branch":
		err = runBranch(args)
	case "worktree", "worktrees":
		err = runWorktree(args)
	case "report":
		runReport(args) // called by agent hooks; must never fail the agent
	case "machine", "machines":
		err = runMachine(args)
	case "bridge":
		err = runBridge()
	case "update":
		err = runUpdate(args)
	case "upload":
		err = runUpload(args)
	case "ask":
		err = runAsk(args)
	case "version", "--version", "-v":
		runVersion(args)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "conch: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "conch:", err)
		os.Exit(1)
	}
}

// nestedPane is the pane this process runs in when it would connect to the
// server that owns it: a pane inherits both CONCH_PANE_ID and the socket of
// its server. Drawing a TUI inside one of its own panes is confusing — keys
// and the mouse go to the inner one, and closing the pane kills it — so
// conch refuses, like tmux. Clearing either variable (a separate server, or
// CONCH_PANE_ID= to force) opens one anyway.
func nestedPane() string {
	id, sock := os.Getenv("CONCH_PANE_ID"), os.Getenv("CONCH_SOCKET")
	if id == "" || sock == "" || machineFlag != "" && machineFlag != "local" {
		return "" // not in a pane, or aimed at another machine's server
	}
	return id
}

func runTUI() error {
	if id := nestedPane(); id != "" {
		return fmt.Errorf("this is already conch, in pane %s: detach first with %s d, "+
			"or run `CONCH_PANE_ID= conch` to open one inside this pane anyway", id, tuiPrefix())
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	c, err := connect(true)
	if err != nil {
		return err
	}
	if c, err = offerUpgrade(c); err != nil {
		return err
	}
	defer c.Close()

	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if cfg.UI.Mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(tui.New(c, cfg), opts...)
	final, err := p.Run()
	if err == nil && tui.RestartRequested(final) {
		// An update replaced this binary: run the new one in its place.
		c.Close()
		exe, xerr := update.Executable()
		if xerr != nil {
			return xerr
		}
		return syscall.Exec(exe, os.Args, tui.RestartEnv(final))
	}
	return err
}

func runServer(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "stop":
			c, err := connect(false)
			if err != nil {
				return errors.New("server is not running")
			}
			return stopServer(c, machineFlag == "" || machineFlag == "local")
		case "reload":
			fs := flag.NewFlagSet("server reload", flag.ContinueOnError)
			bin := fs.String("binary", "", "program to reload into (default: the server's own executable path)")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			c, err := connect(false)
			if err != nil {
				return errors.New("server is not running")
			}
			nc, err := reloadServer(c, *bin)
			if err != nil {
				return err
			}
			defer nc.Close()
			fmt.Printf("server reloaded: build %s, pid %d (panes kept)\n", nc.Server.Build, nc.Server.PID)
			return nil
		default:
			return fmt.Errorf("unknown server subcommand %q", args[0])
		}
	}
	err := server.New(config.SocketPath(), config.Dir()).Run()
	if errors.Is(err, server.ErrAlreadyRunning) {
		return fmt.Errorf("%w on %s", err, config.SocketPath())
	}
	return err
}

func runStatus() error {
	c, err := connect(false)
	if err != nil {
		if machineFlag != "" && machineFlag != "local" {
			return err // why the machine can't be reached, not "not running"
		}
		fmt.Println("server: not running")
		return nil
	}
	defer c.Close()
	fmt.Printf("server: running  version %s  pid %d  up %s\n",
		c.Server.Version, c.Server.PID, time.Since(c.Server.Started).Round(time.Second))
	var list proto.PaneList
	if err := call(c, proto.MethodPaneList, nil, &list); err != nil {
		return err
	}
	if len(list.Panes) == 0 {
		fmt.Println("no panes")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATE\tAGENT\tSIZE\tCWD\tCOMMAND")
	for _, p := range list.Panes {
		state := p.State
		if p.State == proto.PaneExited {
			state = fmt.Sprintf("exited(%d)", p.ExitCode)
		}
		agent := "-"
		if p.Agent != nil {
			agent = p.Agent.Name + ":" + p.Agent.State
		}
		command := strings.Join(strings.Fields(strings.Join(p.Command, " ")), " ") // scripts span lines
		if r := []rune(command); len(r) > 60 {
			command = string(r[:59]) + "…"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%dx%d\t%s\t%s\n",
			p.ID, p.Name, state, agent, p.Cols, p.Rows, p.Cwd, command)
	}
	return tw.Flush()
}

func runNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	cwd := fs.String("cwd", "", "working directory (default: current)")
	name := fs.String("name", "", "pane name")
	agent := fs.String("agent", "", "launch a known agent (claude); remaining args are passed to it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	command := fs.Args()
	agentArgs := ""
	if *agent != "" {
		for _, a := range command {
			agentArgs += adapter.ShellQuote(a) + " "
		}
		command = nil
	} else if len(command) == 0 {
		command = []string{config.DefaultShell(), "-l"}
	}
	dir := *cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	var info proto.PaneInfo
	if err := call(c, proto.MethodPaneCreate, proto.PaneCreateParams{
		Name: *name, Agent: *agent, AgentArgs: agentArgs, Command: command, Cwd: dir, Cols: 120, Rows: 40,
	}, &info); err != nil {
		return err
	}
	fmt.Println(info.ID)
	return nil
}

func runSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	keys := fs.Bool("keys", false, "treat arguments as key names (e.g. enter ctrl+c)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return errors.New("usage: conch send [-keys] ID TEXT...")
	}
	id, rest := fs.Arg(0), fs.Args()[1:]
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	if *keys {
		return call(c, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: id, Keys: rest}, nil)
	}
	return call(c, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: id, Text: strings.Join(rest, " ")}, nil)
}

func runRead(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: conch read ID")
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	var res proto.PaneReadResult
	if err := call(c, proto.MethodPaneRead, proto.PaneRef{ID: args[0]}, &res); err != nil {
		return err
	}
	// Drop trailing blank rows.
	end := len(res.Lines)
	for end > 0 && res.Lines[end-1] == "" {
		end--
	}
	_, err = io.WriteString(os.Stdout, strings.Join(res.Lines[:end], "\n")+"\n")
	return err
}

func runRedraw(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: conch redraw ID")
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{"pane.redraw.v1"})) > 0 {
		return errors.New("the conch server predates redrawing panes; reload it with `conch server reload`")
	}
	return call(c, proto.MethodPaneRedraw, proto.PaneRef{ID: args[0]}, nil)
}

func runClose(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: conch close ID")
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	return call(c, proto.MethodPaneClose, proto.PaneRef{ID: args[0]}, nil)
}

// stopServer asks the server to stop and waits until it has, so a following
// command doesn't reach the dying server. It closes c. A local server is
// gone when its socket refuses connections; a remote one (local false) when
// the connection to it closes.
func stopServer(c *client.Client, local bool) error {
	defer c.Close()
	if err := call(c, proto.MethodServerStop, nil, nil); err != nil {
		return err
	}
	if !local {
		// The bridge ends with the server. Don't wait long for it: connecting
		// again goes through a new bridge anyway.
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && c.Err() == nil; {
			time.Sleep(50 * time.Millisecond)
		}
		return nil
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		nc, err := net.Dial("unix", config.SocketPath())
		if err != nil {
			return nil
		}
		nc.Close()
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("server did not stop within 10s")
}

// offerUpgrade checks that the running server supports everything this
// binary uses. A server started by an older build keeps running across
// rebuilds (that is the point of it), so ask before replacing it: restarting
// stops its panes. Declining keeps the old server; the TUI then warns.
func offerUpgrade(c *client.Client) (*client.Client, error) {
	missing := c.MissingCapabilities(proto.Capabilities)
	if len(missing) == 0 {
		return c, nil
	}
	var list proto.PaneList
	_ = call(c, proto.MethodPaneList, nil, &list)
	running := 0
	for _, p := range list.Panes {
		if p.State == proto.PaneRunning {
			running++
		}
	}
	fmt.Fprintf(os.Stderr, "The conch server (pid %d, running since %s) is from an older build and lacks: %s.\n",
		c.Server.PID, c.Server.Started.Format("Jan 2 15:04"), strings.Join(missing, ", "))
	if len(c.MissingCapabilities([]string{"server.reload.v1"})) == 0 {
		fmt.Fprint(os.Stderr, "Reload it onto this build? Panes keep running. [Y/n] ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a == "n" || a == "no" {
			return c, nil
		}
		exe, _ := os.Executable()
		return reloadServer(c, exe)
	}
	if running > 0 {
		fmt.Fprintf(os.Stderr, "Restarting it stops its %d running pane(s).\n", running)
	}
	fmt.Fprint(os.Stderr, "Restart the server now? [y/N] ")
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return c, nil
	}
	if err := stopServer(c, true); err != nil {
		return nil, err
	}
	return connect(true)
}

// reloadServer asks the server to exec bin (default: its own executable) and
// returns a connection to the reloaded server. The old connection is closed.
func reloadServer(c *client.Client, bin string) (*client.Client, error) {
	pid, loaded := c.Server.PID, loadedAt(c.Server)
	var res proto.ServerReloadResult
	err := call(c, proto.MethodServerReload, proto.ServerReloadParams{Binary: bin}, &res)
	c.Close()
	if err != nil {
		return nil, err
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		nc, err := connect(false)
		if err != nil {
			continue
		}
		if nc.Server.PID == pid && loadedAt(nc.Server).After(loaded) {
			return nc, nil // the same process, started again
		}
		if nc.Server.PID != pid {
			nc.Close()
			return nil, errors.New("a different server answered; the reload may have failed: see " + config.ServerLogPath())
		}
		nc.Close()
	}
	return nil, errors.New("the server did not come back within 15s; see " + config.ServerLogPath())
}

// loadedAt is when a server's program started. Servers from before
// loaded_at reset Started at every reload instead.
func loadedAt(h proto.HelloResult) time.Time {
	if !h.LoadedAt.IsZero() {
		return h.LoadedAt
	}
	return h.Started
}

// connect dials the local server (or the -m machine's), optionally starting
// it first.
// tuiPrefix is the configured tmux-style prefix, for messages.
func tuiPrefix() string {
	if cfg, err := config.Load(); err == nil && cfg.Keys.Prefix != "" {
		return cfg.Keys.Prefix
	}
	return "ctrl+b"
}

func connect(start bool) (*client.Client, error) {
	if machineFlag != "" && machineFlag != "local" {
		return connectMachine(machineFlag)
	}
	sock := config.SocketPath()
	if start {
		if err := client.EnsureServer(sock, config.ServerLogPath()); err != nil {
			return nil, err
		}
	}
	return client.Dial(sock, "conch-cli")
}

func call(c *client.Client, method string, params, out any) error {
	return callFor(c, method, params, out, 10*time.Second)
}

// callFor is call with a longer wait, for work that reaches the network or
// touches many files.
func callFor(c *client.Client, method string, params, out any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := c.Call(ctx, method, params, out)
	var perr *proto.Error
	if errors.As(err, &perr) && perr.Code == proto.ErrUnknown {
		return fmt.Errorf("the conch server (pid %d) is from an older build without %s; restart it with `conch server stop` (its panes stop) or run `conch` to be asked", c.Server.PID, method)
	}
	return err
}
