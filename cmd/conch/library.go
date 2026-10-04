package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/Amitgb14/conch/internal/agentsetup"
	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

const libraryUsage = `usage: conch agent library [list]
       conch agent library add [-to NAMES] [-env K=${V}]... [-header K=V]... [-sse] NAME (URL | -- COMMAND [ARGS...])
       conch agent library mcp [-to NAMES]
       conch agent library skill [-to NAMES] NAME FOLDER
       conch agent library on|off NAME NAMES
       conch agent library rm NAME
       conch agent library import AGENT
       conch agent library plan | apply | undo [-stamp S]`

// agentLibrary manages the MCP servers and skills conch keeps for the
// agents, and gives each agent what the library says. Editing the library
// writes to no agent; plan says what apply would write.
func agentLibrary(args []string) error {
	cmd := "list"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	if missing := c.MissingCapabilities([]string{proto.CapAgentLibrary}); len(missing) > 0 {
		return fmt.Errorf("the server on this machine is too old for the library (needs %s); reload it with conch update", missing[0])
	}
	return runLibrary(c, os.Stdout, cmd, args)
}

func runLibrary(c *client.Client, w io.Writer, cmd string, args []string) error {
	get := func(p proto.AgentLibraryParams) (proto.AgentLibraryResult, error) {
		var res proto.AgentLibraryResult
		err := call(c, proto.MethodAgentLibrary, p, &res)
		return res, err
	}
	save := func(lib proto.Library) error {
		res, err := get(proto.AgentLibraryParams{Set: &lib})
		if err != nil {
			return err
		}
		printLibrary(w, res)
		fmt.Fprintln(w, "\nsaved; conch agent library plan says what applying writes")
		return nil
	}
	switch cmd {
	case "list", "ls":
		if len(args) > 0 {
			return errors.New(libraryUsage)
		}
		res, err := get(proto.AgentLibraryParams{})
		if err != nil {
			return err
		}
		printLibrary(w, res)
		return nil

	case "add":
		fs := flag.NewFlagSet("agent library add", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		to := fs.String("to", "all", "the agents to give it to, comma separated, or all")
		sse := fs.Bool("sse", false, "the server speaks SSE rather than streamable HTTP")
		var env, headers pairs
		fs.Var(&env, "env", "an environment variable, K=${VAR} (repeatable)")
		fs.Var(&headers, "header", "an HTTP header, e.g. Authorization=Bearer ${TOKEN} (repeatable)")
		if err := fs.Parse(args); err != nil || fs.NArg() < 2 {
			return errors.New(libraryUsage)
		}
		res, err := get(proto.AgentLibraryParams{})
		if err != nil {
			return err
		}
		s := proto.LibraryServer{Name: fs.Arg(0), Env: env.m, Headers: headers.m, Agents: agentList(*to, res.Agents)}
		rest := fs.Args()[1:]
		if len(rest) > 0 && rest[0] == "--" {
			rest = rest[1:]
		}
		switch {
		case len(rest) == 0:
			return errors.New(libraryUsage)
		case len(rest) == 1 && (strings.HasPrefix(rest[0], "http://") || strings.HasPrefix(rest[0], "https://")):
			s.URL = rest[0]
			if *sse {
				s.Transport = "sse"
			}
		default:
			s.Command, s.Args = rest[0], rest[1:]
		}
		lib := res.Library
		replaced := false
		for i := range lib.Servers {
			if lib.Servers[i].Name == s.Name {
				lib.Servers[i], replaced = s, true
			}
		}
		if !replaced {
			lib.Servers = append(lib.Servers, s)
		}
		return save(lib)

	case "mcp":
		// conch's own tools, in one action: the entry is the same for
		// every agent, so there is nothing to type and nothing to get
		// wrong (agentsetup.ConchServer).
		fs := flag.NewFlagSet("agent library mcp", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		to := fs.String("to", "all", "the agents to give it to, comma separated, or all")
		if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
			return errors.New(libraryUsage)
		}
		res, err := get(proto.AgentLibraryParams{})
		if err != nil {
			return err
		}
		return save(agentsetup.WithConch(res.Library, agentList(*to, res.Agents)))

	case "skill":
		fs := flag.NewFlagSet("agent library skill", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		to := fs.String("to", "all", "the agents to give it to, comma separated, or all")
		if err := fs.Parse(args); err != nil || fs.NArg() != 2 {
			return errors.New(libraryUsage)
		}
		res, err := get(proto.AgentLibraryParams{})
		if err != nil {
			return err
		}
		sk := proto.LibrarySkill{Name: fs.Arg(0), Path: fs.Arg(1), Agents: agentList(*to, res.Agents)}
		lib := res.Library
		replaced := false
		for i := range lib.Skills {
			if lib.Skills[i].Name == sk.Name {
				lib.Skills[i], replaced = sk, true
			}
		}
		if !replaced {
			lib.Skills = append(lib.Skills, sk)
		}
		return save(lib)

	case "on", "off", "rm":
		if (cmd == "rm" && len(args) != 1) || (cmd != "rm" && len(args) != 2) {
			return errors.New(libraryUsage)
		}
		res, err := get(proto.AgentLibraryParams{})
		if err != nil {
			return err
		}
		lib, name := res.Library, args[0]
		var agents []string
		if cmd != "rm" {
			agents = agentList(args[1], res.Agents)
		}
		change := func(have []string) []string {
			if cmd == "on" {
				return append(have, agents...)
			}
			var out []string
			for _, a := range have {
				if !contains(agents, a) {
					out = append(out, a)
				}
			}
			return out
		}
		found := false
		var servers []proto.LibraryServer
		for _, s := range lib.Servers {
			if s.Name == name {
				found = true
				if cmd == "rm" {
					continue
				}
				s.Agents = change(s.Agents)
			}
			servers = append(servers, s)
		}
		var skills []proto.LibrarySkill
		for _, sk := range lib.Skills {
			if sk.Name == name {
				found = true
				if cmd == "rm" {
					continue
				}
				sk.Agents = change(sk.Agents)
			}
			skills = append(skills, sk)
		}
		if !found {
			return fmt.Errorf("the library has no server or skill called %s", name)
		}
		lib.Servers, lib.Skills = servers, skills
		return save(lib)

	case "import":
		if len(args) != 1 {
			return errors.New(libraryUsage)
		}
		res, err := get(proto.AgentLibraryParams{Import: args[0]})
		if err != nil {
			return err
		}
		printLibrary(w, res)
		fmt.Fprintf(w, "\nimported %s\n", strings.Join(res.Imported, ", "))
		for _, s := range res.Skipped {
			fmt.Fprintf(w, "left out %s\n", s)
		}
		return nil

	case "plan", "apply", "undo":
		fs := flag.NewFlagSet("agent library "+cmd, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		stamp := fs.String("stamp", "", "which apply to undo")
		if err := fs.Parse(args); err != nil || fs.NArg() > 0 || (*stamp != "" && cmd != "undo") {
			return errors.New(libraryUsage)
		}
		p := proto.LibraryApplyParams{Apply: cmd == "apply", Undo: cmd == "undo", Stamp: *stamp}
		var res proto.AgentSyncResult
		if err := call(c, proto.MethodLibraryApply, p, &res); err != nil {
			return err
		}
		printSyncWith(w, res, cmd != "plan", "conch agent library apply", "conch agent library undo")
		return nil
	}
	return errors.New(libraryUsage)
}

// pairs is a repeatable K=V flag.
type pairs struct{ m map[string]string }

func (p *pairs) String() string { return "" }
func (p *pairs) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(k) == "" {
		return fmt.Errorf("%q is not K=V", v)
	}
	if p.m == nil {
		p.m = map[string]string{}
	}
	p.m[strings.TrimSpace(k)] = val
	return nil
}

// agentList reads "all" or "claude,codex".
func agentList(s string, all []string) []string {
	if strings.TrimSpace(s) == "all" {
		return append([]string(nil), all...)
	}
	var out []string
	for _, n := range strings.Split(s, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// libraryMarks are how a cell's state reads in a table.
var libraryMarks = map[string]string{
	"on": "✓", "pending": "+", "remove": "−", "off": "·", "theirs": "○", "differs": "!", "cannot": "✗",
}

func printLibrary(w io.Writer, res proto.AgentLibraryResult) {
	if len(res.Library.Servers)+len(res.Library.Skills) == 0 {
		fmt.Fprintln(w, "the library is empty: conch agent library add, skill, or import AGENT")
		return
	}
	state := map[string]proto.LibraryCell{}
	for _, c := range res.Cells {
		state[c.Kind+"\x00"+c.Name+"\x00"+c.Agent] = c
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "\t\t%s\n", strings.Join(res.Agents, "\t"))
	var notes []string
	row := func(kind, name, what string) {
		cols := []string{name, what}
		for _, a := range res.Agents {
			c, ok := state[kind+"\x00"+name+"\x00"+a]
			mark := "·"
			if ok {
				mark = libraryMarks[c.State]
				if c.State == "differs" || c.State == "cannot" || c.State == "remove" {
					notes = append(notes, fmt.Sprintf("%s %s · %s: %s", libraryMarks[c.State], name, a, c.Detail))
				}
			}
			cols = append(cols, mark)
		}
		fmt.Fprintln(tw, strings.Join(cols, "\t"))
	}
	servers := append([]proto.LibraryServer(nil), res.Library.Servers...)
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	for _, s := range servers {
		what := "stdio · " + s.Command
		if s.URL != "" {
			what = "http"
			if s.Transport == "sse" {
				what = "sse"
			}
		}
		row(proto.SyncMCP, s.Name, what)
	}
	skills := append([]proto.LibrarySkill(nil), res.Library.Skills...)
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	for _, sk := range skills {
		row(proto.SyncSkill, sk.Name, "skill · "+sk.Path)
	}
	tw.Flush()
	fmt.Fprintln(w, "\n✓ has it  + apply writes it  − apply takes it out  · off  ○ set by hand  ! differs  ✗ cannot")
	for _, n := range notes {
		fmt.Fprintln(w, n)
	}
}
