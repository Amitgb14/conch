package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// Access tokens for agents outside conch (#350): an MCP client elsewhere,
// a script, another tool. Each is a level — read, prompt, drive, full —
// for some projects, until a date, and is checked by the server that made
// it; `conch -m MACHINE token create` makes one for that machine. The
// secret is printed once and kept only as a hash.

const tokenUsage = "usage: conch token create [-level read|prompt|drive|full] [-project P]... [-expires 30d|never] [-json] NAME | ls [-json] | revoke NAME"

func runToken(args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	switch args[0] {
	case "create", "new", "add":
		return tokenCreate(args[1:])
	case "ls", "list":
		return tokenList(args[1:])
	case "revoke", "rm":
		if len(args) != 2 {
			return errors.New(tokenUsage)
		}
		c, err := tokenConnect()
		if err != nil {
			return err
		}
		defer c.Close()
		var info proto.TokenInfo
		if err := call(c, proto.MethodTokenRevoke, proto.TokenRevokeParams{Name: args[1]}, &info); err != nil {
			return err
		}
		fmt.Printf("revoked %s (%s): it is refused from now on, also where it is in use\n", info.Name, info.ID)
		return nil
	}
	return unknownSubcommand("token", args[0])
}

func tokenConnect() (*client.Client, error) {
	c, err := connect(true)
	if err != nil {
		return nil, err
	}
	if len(c.MissingCapabilities([]string{proto.CapToken})) > 0 {
		c.Close()
		return nil, errors.New("the conch server there predates access tokens; reload it with `conch server reload`")
	}
	return c, nil
}

func tokenCreate(args []string) error {
	fs := flag.NewFlagSet("token create", flag.ContinueOnError)
	level := fs.String("level", proto.TokenRead, "read, prompt, drive or full")
	var projects stringList
	fs.Var(&projects, "project", "a project it is limited to, by name, id or folder (repeat for more; none is every project)")
	expires := fs.String("expires", "30d", "how long it lasts: 30d, 12h, 90m, or never")
	asJSON := fs.Bool("json", false, "print the token and what it may do as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New(tokenUsage)
	}
	p := proto.TokenCreateParams{Name: fs.Arg(0), Level: *level, Projects: projects}
	life, never, err := parseTokenLife(*expires)
	if err != nil {
		return err
	}
	p.ExpiresIn, p.Never = int64(life/time.Second), never
	c, err := tokenConnect()
	if err != nil {
		return err
	}
	defer c.Close()
	var res proto.TokenCreateResult
	if err := call(c, proto.MethodTokenCreate, p, &res); err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(res)
	}
	fmt.Println(res.Token)
	fmt.Fprintf(os.Stderr, "%s: a %s token, %s, %s.\n", res.Info.Name, res.Info.Level, tokenWhere(res.Info), tokenUntil(res.Info))
	fmt.Fprintf(os.Stderr, "It is shown once: give it to the agent as %s=… (or Authorization: Bearer … to conch mcp -http).\n", proto.TokenEnv)
	return nil
}

func tokenList(args []string) error {
	fs := flag.NewFlagSet("token ls", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print them as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(tokenUsage)
	}
	c, err := tokenConnect()
	if err != nil {
		return err
	}
	defer c.Close()
	var list proto.TokenListResult
	if err := call(c, proto.MethodTokenList, nil, &list); err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(list)
	}
	if len(list.Tokens) == 0 {
		fmt.Println("no access tokens (conch token create NAME makes one)")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tID\tLEVEL\tPROJECTS\tEXPIRES\tUSED")
	for _, tk := range list.Tokens {
		used := "never"
		if !tk.Used.IsZero() {
			used = tk.Used.Local().Format("2 Jan 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", tk.Name, tk.ID, tk.Level, tokenProjects(tk), tokenExpiry(tk, time.Now()), used)
	}
	return tw.Flush()
}

// parseTokenLife reads 30d, 12h, 90m, a Go duration, or never.
func parseTokenLife(s string) (time.Duration, bool, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "never" {
		return 0, true, nil
	}
	bad := fmt.Errorf("-expires %q: say 30d, 12h, 90m or never", s)
	var d time.Duration
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil {
			return 0, false, bad
		}
		if days > 36500 {
			return 0, false, errors.New("-expires: at most a hundred years; or never")
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, false, bad
		}
	}
	if d < time.Minute {
		return 0, false, errors.New("-expires: at least a minute")
	}
	return d, false, nil
}

func tokenProjects(tk proto.TokenInfo) string {
	if len(tk.Projects) == 0 {
		return "all"
	}
	var names []string
	for _, p := range tk.Projects {
		names = append(names, p.Name)
	}
	return strings.Join(names, ",")
}

func tokenWhere(tk proto.TokenInfo) string {
	if len(tk.Projects) == 0 {
		return "in every project"
	}
	return "in " + tokenProjects(tk)
}

func tokenUntil(tk proto.TokenInfo) string {
	if tk.Expires.IsZero() {
		return "never expiring"
	}
	return "until " + tk.Expires.Local().Format("2 Jan 2006 15:04")
}

func tokenExpiry(tk proto.TokenInfo, now time.Time) string {
	switch {
	case tk.Expires.IsZero():
		return "never"
	case !now.Before(tk.Expires):
		return "expired"
	}
	return tk.Expires.Local().Format("2 Jan 2006")
}
