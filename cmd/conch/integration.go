package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/integration"
)

// conch integration: what conch talks to, besides itself. An integration
// is a file — a description of a service's API, never code — from a
// registry, checked by hash and installed after the person agrees to the
// hosts it sends their key to. This runs here, with no server: the files
// are this computer's, and so are the keys.

const integrationUsage = `Usage:
  conch integration search [-json] [KIND|WORDS]   what the registries list
  conch integration info NAME                    what it does in conch, where your key goes, who wrote it
  conch integration install [-y] [-key-stdin] NAME
                                                 agree to its hosts, give its key (kept in the Keychain
                                                 on a Mac), and check it against this project
  conch integration ls [-json] [-cwd DIR]        every integration here, built in or installed, and its state
  conch integration update [-y] [NAME]           take the registry's newest; asks again when hosts change
  conch integration remove NAME                  delete an installed one
  conch integration check [-json] NAME|FILE      what is wrong with a file (offline)
  conch integration guide                        the file format, for writing one

A file of your own goes in ~/.config/conch/integrations/NAME.toml and
replaces one conch ships of the same name.`

func runIntegration(args []string) error {
	if len(args) == 0 {
		fmt.Println(integrationUsage)
		return nil
	}
	sub, args := args[0], args[1:]
	switch sub {
	case "search":
		return integrationSearch(args)
	case "info", "show":
		return integrationInfo(args)
	case "install":
		return integrationInstall(args)
	case "ls", "list":
		return integrationList(args)
	case "update":
		return integrationUpdate(args)
	case "remove", "rm":
		return integrationRemove(args)
	case "check":
		return integrationCheck(args)
	case "guide":
		fmt.Print(integration.Guide)
		return nil
	case "help", "-h", "--help":
		fmt.Println(integrationUsage)
		return nil
	}
	return unknownSubcommand("integration", sub)
}

func integrationRegistries() []integration.Registry {
	cfg, _ := config.Load()
	return integration.Registries(cfg.Integrations)
}

// indexes reads every registry; one that cannot be read is said and
// skipped, so a company's registry being down does not hide conch's.
func indexes(ctx context.Context, w io.Writer) []*integration.Index {
	var out []*integration.Index
	for _, r := range integrationRegistries() {
		ix, err := integration.FetchIndex(ctx, r)
		if err != nil {
			fmt.Fprintln(w, err)
			continue
		}
		if _, err := integration.MarkRevoked(ix); err != nil {
			fmt.Fprintln(w, err)
		}
		out = append(out, ix)
	}
	return out
}

func findEntry(ixs []*integration.Index, name string) (integration.Entry, *integration.Index, bool) {
	for _, ix := range ixs {
		if e, ok := ix.Find(name); ok {
			return e, ix, true
		}
	}
	return integration.Entry{}, nil, false
}

func registryNamed(name string) (integration.Registry, bool) {
	for _, r := range integrationRegistries() {
		if r.Name == name {
			return r, true
		}
	}
	return integration.Registry{}, false
}

// standing is an entry's tier as people read it.
func standing(e integration.Entry, signed bool) string {
	s := "not checked"
	if e.Checked != nil {
		s = "verified " + e.Checked.Date
	}
	if e.RevokedReason(e.Version) != "" {
		s = "revoked"
	}
	if !signed {
		s += " (unsigned index)"
	}
	return s
}

func hostWords(h integration.Hosts) string {
	s := h.API
	if s == "" {
		s = "your own server"
	}
	if len(h.Follow) > 0 {
		s += ", " + strings.Join(h.Follow, ", ") + " (without your key)"
	}
	return s
}

func integrationSearch(args []string) error {
	fs := flag.NewFlagSet("integration search", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	type row struct {
		integration.Entry
		Signed bool `json:"signed"`
	}
	var rows []row
	for _, ix := range indexes(ctx, os.Stderr) {
		for _, e := range ix.Search(strings.Join(fs.Args(), " ")) {
			if e.Kind == integration.KindSandbox {
				continue // conch sandbox provider search lists those
			}
			rows = append(rows, row{e, ix.Signed})
		}
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(rows)
	}
	if len(rows) == 0 {
		fmt.Println("nothing listed matches")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", firstSetStr(r.Label, r.Name), r.Kind, standing(r.Entry, r.Signed), hostWords(r.Hosts))
	}
	return tw.Flush()
}

func integrationInfo(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: conch integration info NAME")
	}
	name := args[0]
	if err := integration.CheckName(name); err != nil {
		return err
	}
	var f *integration.File
	files, _ := integration.All()
	for _, x := range files {
		if x.Name == name {
			f = x
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	e, ix, listed := findEntry(indexes(ctx, io.Discard), name)
	if f == nil && !listed {
		return fmt.Errorf("no integration %s here or in a registry: conch integration search lists them", name)
	}
	kind, label, ops, hosts := e.Kind, e.Label, e.Operations, e.Hosts
	keyEnv, note, docs := e.KeyEnv, e.Note, e.Docs
	source := "registry " + e.Registry
	keyHelp := ""
	if f != nil {
		kind, label, ops, keyEnv, note, docs, keyHelp = f.Kind, f.DisplayName(), f.Operations(), f.KeyEnv, f.Note, f.Docs, f.KeyHelp
		hosts = integration.Hosts{API: hostOfURL(f.API), Follow: f.FollowHosts}
		source = "installed"
		if f.Shipped() {
			source = "ships with conch"
		}
	}
	fmt.Printf("%s · %s · %s\n", firstSetStr(label, name), kind, source)
	if listed {
		fmt.Printf("registry %s, version %d · %s\n", e.Registry, e.Version, standing(e, ix.Signed))
		if why := e.RevokedReason(e.Version); why != "" {
			fmt.Printf("REVOKED: %s\n", why)
		}
	}
	fmt.Println("\nWhat it does in conch")
	for _, d := range integration.Does(kind, ops) {
		mark := map[string]string{"yes": "✓", "partly": "◐", "no": "✗", "built-in": "—"}[d.Level]
		fmt.Printf("  %s %s\n      %s\n", mark, d.What, d.Where)
	}
	fmt.Printf("\nYour key ($%s) goes to %s only", keyEnv, firstSetStr(hosts.API, "the address you give"))
	if len(hosts.Follow) > 0 {
		fmt.Printf("; it also reads %s, without your key", strings.Join(hosts.Follow, ", "))
	}
	fmt.Println(". No programs run: HTTP only.")
	if keyHelp != "" {
		fmt.Println("\nSetting it up: " + keyHelp)
	}
	if note != "" {
		fmt.Println("\nNote: " + note)
	}
	if docs != "" {
		fmt.Println("Docs: " + docs)
	}
	return nil
}

func hostOfURL(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	h, _, _ := strings.Cut(raw, "/")
	return h
}

func integrationInstall(args []string) error {
	fs := flag.NewFlagSet("integration install", flag.ContinueOnError)
	yes := fs.Bool("y", false, "install without asking")
	keyStdin := fs.Bool("key-stdin", false, "read the key from standard input, for scripts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch integration install [-y] [-key-stdin] NAME")
	}
	name := fs.Arg(0)
	if err := integration.CheckName(name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, ix, ok := findEntry(indexes(ctx, os.Stderr), name)
	if !ok {
		return fmt.Errorf("no registry lists %s: conch integration search lists what they have", name)
	}
	if why := e.RevokedReason(e.Version); why != "" {
		return fmt.Errorf("%s version %d is revoked: %s", name, e.Version, why)
	}
	if recs, _ := integration.Records(); recs[name].Version != 0 {
		return fmt.Errorf("%s is installed already: conch integration update %s takes the newest", name, name)
	}
	reg, _ := registryNamed(e.Registry)
	fmt.Printf("%s sends your key ($%s) to %s only.", name, e.KeyEnv, firstSetStr(e.Hosts.API, "the address you give"))
	if len(e.Hosts.Follow) > 0 {
		fmt.Printf(" It reads %s without it.", strings.Join(e.Hosts.Follow, ", "))
	}
	if !ix.Signed {
		fmt.Printf(" (The registry's index is unsigned.)")
	}
	if !*yes {
		if !askYes(" Go on?") {
			return errors.New("nothing installed (-y installs without asking)")
		}
	} else {
		fmt.Println()
	}
	data, f, err := integration.FetchFile(ctx, reg, e)
	if err != nil {
		return err
	}
	if err := integration.Install(e, data); err != nil {
		return err
	}
	key, err := takeKey(f, *keyStdin)
	if err != nil {
		return err
	}
	fmt.Printf("installed %s %d (%s)", name, e.Version, e.Kind)
	if used := usedHere(f); used != "" {
		fmt.Printf(" · used by %s", used)
	}
	fmt.Println()
	if key != "" {
		fmt.Println(liveCheckHere(ctx, f, key))
	}
	return nil
}

// takeKey asks for the file's key when it is not set already: without
// echo on a terminal, from stdin with -key-stdin. On a Mac it goes in the
// Keychain, where conch's server reads it; elsewhere the variable is
// said, since conch writes a key to no file.
func takeKey(f *integration.File, fromStdin bool) (string, error) {
	if k := keyFor(f.KeyEnv); k != "" {
		return k, nil
	}
	var key string
	switch {
	case fromStdin:
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 8<<10))
		if err != nil {
			return "", err
		}
		key = strings.TrimSpace(string(b))
	case term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()):
		fmt.Printf("%s key, for $%s (not shown; empty to skip): ", f.DisplayName(), f.KeyEnv)
		b, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Println()
		if err != nil {
			return "", err
		}
		key = strings.TrimSpace(string(b))
	}
	if key == "" {
		fmt.Printf("put the key in $%s where conch's server runs", f.KeyEnv)
		if keychainHere() {
			fmt.Printf(", or the Keychain (service conch-mcp, account %s)", f.KeyEnv)
		}
		fmt.Println()
		return "", nil
	}
	if !keychainHere() {
		fmt.Printf("conch keeps no key in a file: put it in $%s where conch's server runs\n", f.KeyEnv)
		return key, nil
	}
	if err := keychainSet(f.KeyEnv, key); err != nil {
		return "", fmt.Errorf("the Keychain: %w", err)
	}
	fmt.Printf("kept in the Keychain (conch-mcp, %s)\n", f.KeyEnv)
	return key, nil
}

func securityBin() string {
	if b := os.Getenv("CONCH_SECURITY"); b != "" {
		return b
	}
	return "/usr/bin/security"
}

func keychainHere() bool {
	return os.Getenv("CONCH_SECURITY") != "" || runtime.GOOS == "darwin"
}

// keychainSet stores the key through security's interactive mode, so it
// is on no command line another process could read.
func keychainSet(account, key string) error {
	if strings.ContainsAny(key, "\"\n\\") || strings.ContainsAny(account, "\" \n") {
		return errors.New("that key has characters the Keychain's command line cannot take; put it in the variable instead")
	}
	cmd := exec.Command(securityBin(), "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s conch-mcp -a %s -w \"%s\"\n", account, key))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.ReplaceAll(strings.TrimSpace(string(out)), key, "…"))
	}
	return nil
}

// keyFor is a key's variable here, then the Keychain.
func keyFor(name string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	if !keychainHere() {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, securityBin(), "find-generic-password", "-s", "conch-mcp", "-a", name, "-w").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// projectHere is the project in the current directory: its checkout, remote and
// branch, when it is one.
func projectHere(ctx context.Context, dir string) (root string, rem integration.Remote, branch string) {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", integration.Remote{}, ""
	}
	// symbolic-ref, not rev-parse HEAD: a repository with no commit yet
	// has a branch but no HEAD to name.
	top := strings.TrimSpace(string(out))
	if b, err := exec.CommandContext(ctx, "git", "-C", top, "symbolic-ref", "--short", "HEAD").Output(); err == nil {
		branch = strings.TrimSpace(string(b))
	}
	parts := []string{top}
	rem, _ = integration.ParseRemote(gitx.RemoteURL(ctx, parts[0]))
	return parts[0], rem, branch
}

func usedHere(f *integration.File) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root, rem, _ := projectHere(ctx, "")
	if root != "" && f.Uses(root, rem) {
		return filepath.Base(root)
	}
	return ""
}

// liveCheckHere reads the runs of this project's branch with the key: one
// read-only call that says the key and the file work.
func liveCheckHere(ctx context.Context, f *integration.File, key string) string {
	root, rem, branch := projectHere(ctx, "")
	if root == "" || !f.Uses(root, rem) {
		return "not checked: run conch integration install from a project that uses it, or conch branch fix-ci there"
	}
	cfg, _ := config.Load()
	user := ""
	if f.Auth.UserEnv != "" {
		user = keyFor(f.Auth.UserEnv)
	}
	conn, err := f.Connect(key, user, cfg.Integrations.Base[f.Name])
	if err != nil {
		return "not checked: " + err.Error()
	}
	runs, err := conn.Runs(ctx, integration.Target{Remote: rem, Branch: branch})
	if err != nil {
		return "the check failed: " + err.Error()
	}
	return fmt.Sprintf("checked: read %d runs of %s on %s", len(runs), branch, f.DisplayName())
}

type lsRow struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Source  string `json:"source"`
	State   string `json:"state"`
	Uses    bool   `json:"uses,omitempty"`
}

func integrationList(args []string) error {
	fs := flag.NewFlagSet("integration ls", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	dir := fs.String("cwd", "", "the project to say which it uses (default: the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root, rem, _ := projectHere(ctx, *dir)
	rows := []lsRow{
		{Name: "github", Kind: "host", Version: "-", Source: "built in", State: "through gh"},
		{Name: "github-actions", Kind: integration.KindCI, Version: "-", Source: "built in", State: "through gh"},
		{Name: "github-issues", Kind: "tickets", Version: "-", Source: "built in", State: "through gh"},
	}
	cfg, _ := config.Load()
	for _, t := range []struct{ name, env, set string }{
		{"linear", firstSetStr(cfg.Trackers.Linear.APIKeyEnv, "LINEAR_API_KEY"), cfg.Trackers.Linear.APIKey},
		{"jira", firstSetStr(cfg.Trackers.Jira.APITokenEnv, "JIRA_API_TOKEN"), cfg.Trackers.Jira.APIToken},
	} {
		state := "key: $" + t.env + " not set"
		if t.set != "" || os.Getenv(t.env) != "" {
			state = "ok"
		}
		rows = append(rows, lsRow{Name: t.name, Kind: "tickets", Version: "-", Source: "built in", State: state})
	}
	files, bad := integration.All()
	recs, _ := integration.Records()
	for _, f := range files {
		r := lsRow{Name: f.Name, Kind: f.Kind, Version: "-", Source: "ships with conch", State: "ok", Uses: root != "" && f.Uses(root, rem)}
		if rec, ok := recs[f.Name]; ok && !f.Shipped() {
			r.Version, r.Source = fmt.Sprint(rec.Version), rec.Registry
			if edited, _ := integration.Edited(f.Name); edited {
				r.State = "edited here"
			}
		} else if !f.Shipped() {
			r.Source = "a file of your own"
		}
		if keyFor(f.KeyEnv) == "" {
			r.State = "key: $" + f.KeyEnv + " not set"
			if keychainHere() {
				r.State += ", nor in the Keychain"
			}
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows[5:], func(i, j int) bool { return rows[5+i].Name < rows[5+j].Name })
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"integrations": rows, "problems": bad})
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tVERSION\tSOURCE\tSTATE")
	for _, r := range rows {
		state := r.State
		if r.Uses {
			state += " · used here"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Kind, r.Version, r.Source, state)
	}
	tw.Flush()
	for _, b := range bad {
		fmt.Println("not used: " + b)
	}
	return nil
}

func integrationUpdate(args []string) error {
	fs := flag.NewFlagSet("integration update", flag.ContinueOnError)
	yes := fs.Bool("y", false, "update without asking, unless the hosts change")
	if err := fs.Parse(args); err != nil {
		return err
	}
	recs, err := integration.Records()
	if err != nil {
		return err
	}
	var names []string
	for n := range recs {
		if fs.NArg() == 0 || n == fs.Arg(0) {
			names = append(names, n)
		}
	}
	if fs.NArg() == 1 && len(names) == 0 {
		return fmt.Errorf("%s was not installed from a registry", fs.Arg(0))
	}
	sort.Strings(names)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ixs := indexes(ctx, os.Stderr)
	for _, n := range names {
		rec := recs[n]
		e, ix, ok := findEntry(ixs, n)
		switch {
		case !ok || ix.Registry != rec.Registry:
			fmt.Printf("%s: its registry %s no longer lists it\n", n, rec.Registry)
			continue
		case e.Version <= rec.Version:
			fmt.Printf("%s: up to date (%d)\n", n, rec.Version)
			continue
		case e.RevokedReason(e.Version) != "":
			fmt.Printf("%s: version %d is revoked: %s\n", n, e.Version, e.RevokedReason(e.Version))
			continue
		}
		if edited, _ := integration.Edited(n); edited {
			fmt.Printf("%s: edited here, so left alone; remove it to take version %d\n", n, e.Version)
			continue
		}
		// New hosts are asked about even with -y: a key is not sent
		// somewhere new without the person saying so.
		if integration.HostsChanged(rec, e) {
			fmt.Printf("%s %d sends your key to %s, where %d sent it to %s.", n, e.Version, hostWords(e.Hosts), rec.Version, hostWords(rec.Hosts))
			if !askYes(" Update?") {
				fmt.Printf("%s: not updated\n", n)
				continue
			}
		} else if !*yes && !askYes(fmt.Sprintf("Update %s %d → %d?", n, rec.Version, e.Version)) {
			fmt.Printf("%s: not updated\n", n)
			continue
		}
		reg, _ := registryNamed(e.Registry)
		data, _, err := integration.FetchFile(ctx, reg, e)
		if err != nil {
			fmt.Printf("%s: %v\n", n, err)
			continue
		}
		if err := integration.Remove(n); err != nil {
			return err
		}
		if err := integration.Install(e, data); err != nil {
			return err
		}
		fmt.Printf("%s: updated to %d\n", n, e.Version)
	}
	return nil
}

func integrationRemove(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: conch integration remove NAME")
	}
	if err := integration.Remove(args[0]); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", args[0])
	return nil
}

func integrationCheck(args []string) error {
	fs := flag.NewFlagSet("integration check", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Bool("offline", true, "check the file alone (the only check there is here)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch integration check [-json] NAME|FILE")
	}
	arg := fs.Arg(0)
	var problems []string
	var label string
	if strings.Contains(arg, "/") || strings.HasSuffix(arg, ".toml") {
		f, p, err := integration.Read(arg)
		if err != nil {
			return err
		}
		problems, label = p, firstSetStr(f.Name, arg)
	} else {
		data, _, err := integration.Source(arg)
		if err != nil {
			return err
		}
		f, p, err := integration.Parse(data)
		if err != nil {
			return err
		}
		problems, label = p, firstSetStr(f.Name, arg)
	}
	if *asJSON {
		if problems == nil {
			problems = []string{}
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"name": label, "problems": problems}); err != nil {
			return err
		}
	} else if len(problems) == 0 {
		fmt.Printf("%s is fine\n", label)
	} else {
		for _, p := range problems {
			fmt.Println(p)
		}
	}
	if len(problems) > 0 {
		return exitStatus(1)
	}
	return nil
}
