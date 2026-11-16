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
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
	"github.com/Amitgb14/conch/internal/tracker"
)

// conch integration: what conch talks to, besides itself. An integration
// is a file — a description of a service's API, never code — from a
// registry, checked by hash and installed after the person agrees to the
// hosts it sends their key to. This runs here, with no server: the files
// are this computer's, and so are the keys.

const integrationUsage = `Usage:
  conch integration search [-json] [KIND|WORDS]   what the registries list
  conch integration info [-json] NAME            what it does in conch, where your key goes, who wrote it
  conch integration install [-y] [-key-stdin] [-base URL] [-cwd DIR] NAME
                                                 agree to its hosts, give its key (kept in the Keychain
                                                 on a Mac), and check it against this project
  conch integration key [-stdin] [-base URL] [-cwd DIR] NAME
                                                 give an integration its key: without echo, kept in the
                                                 Keychain on a Mac (elsewhere it says the variable)
  conch integration ls [-json] [-cwd DIR]        every integration here, built in or installed, and its state
  conch integration update [-y] [NAME]           take the registry's newest; asks again when hosts change
  conch integration remove NAME                  delete an installed one
  conch integration check [-json] NAME|FILE      what is wrong with a file (offline)
  conch integration guide                        the file format, for writing one
  conch integration adapt [-kind ci] [-agent A] [-name N] [-json] [-cwd DIR] URL...
                                                 an agent in a pane of its own reads the service's API
                                                 docs and writes NAME.toml; conch checks it offline, then
                                                 with your key once you agree where it goes (the agent
                                                 never has it); -revise TEXT NAME, -check NAME, -save NAME
  conch integration publish [-dry-run] [-json] [-y] [-cwd DIR] FILE|NAME
                                                 check it with your key, then propose it and its index
                                                 entry to the registry as a pull request (through gh)

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
	case "key":
		return integrationKey(args)
	case "adapt":
		return integrationAdapt(args)
	case "publish":
		return integrationPublish(args)
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
	if e.BuiltIn {
		return "built in"
	}
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
		where := hostWords(r.Hosts)
		if r.BuiltIn {
			where = "conch's own code"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", firstSetStr(r.Label, r.Name), r.Kind, standing(r.Entry, r.Signed), where)
	}
	return tw.Flush()
}

// infoJSON is an integration's page, for Conch.app's.
type infoJSON struct {
	Name      string                `json:"name"`
	Label     string                `json:"label"`
	Kind      string                `json:"kind"`
	Source    string                `json:"source"`
	Installed bool                  `json:"installed"`
	BuiltIn   bool                  `json:"builtin,omitempty"`
	Registry  string                `json:"registry,omitempty"`
	Version   int                   `json:"version,omitempty"`
	Signed    bool                  `json:"signed,omitempty"`
	Tier      string                `json:"tier,omitempty"`
	Checked   *integration.Checked  `json:"checked,omitempty"`
	Revoked   string                `json:"revoked,omitempty"`
	Does      []integration.Feature `json:"does"`
	Hosts     integration.Hosts     `json:"hosts"`
	KeyEnv    string                `json:"key_env"`
	UserEnv   string                `json:"user_env,omitempty"`
	AuthType  string                `json:"auth_type,omitempty"`
	Scopes    []string              `json:"scopes,omitempty"`
	BaseURL   string                `json:"base_url,omitempty"`
	KeyHelp   string                `json:"key_help,omitempty"`
	KeySet    bool                  `json:"key_set"`
	Note      string                `json:"note,omitempty"`
	Docs      string                `json:"docs,omitempty"`
	AdaptedBy string                `json:"adapted_by,omitempty"`
	Adapted   string                `json:"adapted,omitempty"`
	SHA256    string                `json:"sha256,omitempty"`
	Installs  int                   `json:"installs,omitempty"`
}

func integrationInfo(args []string) error {
	fs := flag.NewFlagSet("integration info", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch integration info [-json] NAME")
	}
	name := fs.Arg(0)
	if err := integration.CheckName(name); err != nil {
		return err
	}
	if b, ok := builtinNamed(name); ok {
		return builtinInfo(b, *asJSON)
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
	installedHere := f != nil
	if kind == integration.KindSandbox && sandbox.Known(name) {
		// A provider conch has — built in, or a file here — is set up,
		// whatever the registry lists of it.
		installedHere, source = true, "built in"
		if !sandbox.Builtin(name) {
			source = "installed"
		}
		keyHelp = firstSetStr(keyHelp, "Its key goes in Settings → Sandboxes, or $"+keyEnv+".")
	}
	if *asJSON {
		j := infoJSON{Name: name, Label: firstSetStr(label, name), Kind: kind, Source: source, Installed: installedHere,
			Does: integration.Does(kind, ops), Hosts: hosts, KeyEnv: keyEnv, KeyHelp: keyHelp, Note: note, Docs: docs}
		if f != nil {
			j.UserEnv, j.AuthType, j.Scopes, j.BaseURL = f.Auth.UserEnv, f.Auth.Type, f.Auth.Scopes, f.BaseURL
			j.AdaptedBy, j.Adapted = f.AdaptedBy, f.Adapted
		}
		j.KeySet = keyEnv != "" && keyFor(keyEnv) != ""
		if listed {
			j.Registry, j.Version, j.Signed, j.Tier, j.Checked = e.Registry, e.Version, ix.Signed, e.Tier, e.Checked
			j.Revoked, j.SHA256, j.Installs = e.RevokedReason(e.Version), e.SHA256, e.Installs
			if j.AdaptedBy == "" {
				j.AdaptedBy, j.Adapted = e.AdaptedBy, e.Adapted
			}
			j.UserEnv = firstSetStr(j.UserEnv, e.UserEnv)
		}
		return json.NewEncoder(os.Stdout).Encode(j)
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
	base := fs.String("base", "", "the API's address, for a service you run yourself (kept in config.toml)")
	dir := fs.String("cwd", "", "the project to check it against (default: the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch integration install [-y] [-key-stdin] [-base URL] [-cwd DIR] NAME")
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
	if e.BuiltIn {
		return fmt.Errorf("%s is built into conch: there is nothing to install (conch integration info %s says how to set it up)", e.DisplayName(), name)
	}
	if e.Kind == integration.KindSandbox {
		// A sandbox provider installs as it always has, with its hosts and
		// ssh said first; its key goes in Settings → Sandboxes.
		pargs := []string{name}
		if *yes {
			pargs = []string{"-y", name}
		}
		return providerInstall(pargs)
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
	if err := keepBase(f, *base); err != nil {
		return err
	}
	key, err := takeKey(f, *keyStdin)
	if err != nil {
		return err
	}
	fmt.Printf("installed %s %d (%s)", name, e.Version, e.Kind)
	if used := usedHere(f, *dir); used != "" {
		fmt.Printf(" · used by %s", used)
	}
	fmt.Println()
	if key != "" {
		fmt.Println(liveCheckHere(ctx, f, key, *dir))
	}
	return nil
}

// takeKey asks for the file's key when it is not set already: without
// echo on a terminal, from stdin with -key-stdin. On a Mac it goes in the
// Keychain, where conch's server reads it; elsewhere the variable is
// said, since conch writes a key to no file.
func takeKey(f *integration.File, fromStdin bool) (string, error) {
	if k := keyFor(f.KeyEnv); k != "" && !fromStdin {
		return k, nil
	}
	return askKey(f, fromStdin)
}

// askKey reads a new key and keeps it, whatever is set already.
func askKey(f *integration.File, fromStdin bool) (string, error) {
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

func usedHere(f *integration.File, dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root, rem, _ := projectHere(ctx, dir)
	if root != "" && f.Uses(root, rem) {
		return filepath.Base(root)
	}
	return ""
}

// liveCheckHere tries the key: a CI's runs of this project's branch and
// a tracker's open tickets — one read-only call each — or, for a webhook,
// a message saying it works, which is the only way to know it does.
func liveCheckHere(ctx context.Context, f *integration.File, key, dir string) string {
	root, rem, branch := projectHere(ctx, dir)
	if f.Kind == integration.KindCI && (root == "" || !f.Uses(root, rem)) {
		return "not checked: run conch integration install from a project that uses it, or conch branch fix-ci there"
	}
	if f.Kind == integration.KindTickets && f.Project.ID != "" && rem.Host == "" {
		return "not checked: run it from a project whose tickets these are"
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
	target := integration.Target{Remote: rem, Branch: branch}
	switch f.Kind {
	case integration.KindTickets:
		list, err := conn.Tracker(target).List(ctx, tracker.ListOptions{Limit: 100})
		if err != nil {
			return "the check failed: " + err.Error()
		}
		return fmt.Sprintf("checked: read %d open tickets on %s", len(list), f.DisplayName())
	case integration.KindNotify:
		err := conn.Send(ctx, integration.Message{Title: "conch", Body: "Notifications through " + f.DisplayName() + " work."})
		if err != nil {
			return "the check failed: " + err.Error()
		}
		return "checked: sent a test message through " + f.DisplayName()
	}
	runs, err := conn.Runs(ctx, target)
	if err != nil {
		return "the check failed: " + err.Error()
	}
	return fmt.Sprintf("checked: read %d runs of %s on %s", len(runs), branch, f.DisplayName())
}

type lsRow struct {
	Name string `json:"name"`
	// Label is how people write it, where that is not the name.
	Label   string `json:"label,omitempty"`
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
	// Sandbox providers too, as conch sandbox providers sees them: built in,
	// installed from a registry, or a file of the person's own.
	installed, _ := sandbox.ReadInstalled()
	for _, name := range sandbox.Names() {
		r := lsRow{Name: name, Label: sandbox.ProviderLabel(name), Kind: integration.KindSandbox, Version: "-", Source: "built in", State: "ok"}
		if !sandbox.Builtin(name) {
			r.Source = "a file of your own"
			if _, ok := installed[name]; ok {
				r.Source = "registry"
			}
		}
		p, err := remote.OpenProvider(name)
		if err == nil {
			err = p.Check()
		}
		if err != nil {
			r.State = strings.TrimPrefix(err.Error(), sandbox.ErrNotConfigured.Error()+": ")
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

// integrationKey asks for an integration's key, shipped or installed.
func integrationKey(args []string) error {
	fs := flag.NewFlagSet("integration key", flag.ContinueOnError)
	fromStdin := fs.Bool("stdin", false, "read the key from standard input")
	base := fs.String("base", "", "the API's address, for a service you run yourself (kept in config.toml)")
	dir := fs.String("cwd", "", "the project to check it against (default: the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch integration key [-stdin] [-base URL] [-cwd DIR] NAME")
	}
	if b, ok := builtinNamed(fs.Arg(0)); ok {
		return builtinKey(b, *fromStdin)
	}
	data, _, err := integration.Source(fs.Arg(0))
	if err != nil {
		return err
	}
	f, problems, err := integration.Parse(data)
	if err != nil || len(problems) > 0 {
		return fmt.Errorf("%s cannot be used: %v %s", fs.Arg(0), err, strings.Join(problems, "; "))
	}
	if err := keepBase(f, *base); err != nil {
		return err
	}
	if keyFor(f.KeyEnv) != "" && !*fromStdin {
		fmt.Printf("%s's key is set already ($%s, or the Keychain)\n", f.DisplayName(), f.KeyEnv)
		return nil
	}
	key, err := askKey(f, *fromStdin)
	if err != nil {
		return err
	}
	if key != "" && keychainHere() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		fmt.Println(liveCheckHere(ctx, f, key, *dir))
	}
	return nil
}

// keepBase records the address of a service the person runs themselves,
// for a file that asks for one; "" changes nothing.
func keepBase(f *integration.File, base string) error {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return nil
	}
	if f.BaseURL != "ask" {
		return fmt.Errorf("%s's API is %s; it takes no other address", f.Name, f.API)
	}
	if !strings.HasPrefix(base, "https://") {
		return fmt.Errorf("%q: an https:// address", base)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Integrations.Base == nil {
		cfg.Integrations.Base = map[string]string{}
	}
	cfg.Integrations.Base[f.Name] = base
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("its address, %s, is kept in config.toml ([integrations.base] %s)\n", base, f.Name)
	return nil
}

// A built-in is conch's own code — GitHub through gh, Linear and Jira
// through their APIs — with no file: info says how it is set up, and key
// gives a tracker its key under the variable the tracker's code reads,
// which is what the project sheet's Connect… for Linear runs.
type builtinIntegration struct {
	name, label, kind string
	// keyEnv is the variable its key is read from, from [trackers] when
	// that names another; "" for one with no key of conch's (gh's login).
	keyEnv func(config.Config) string
	help   string
	docs   string
}

var builtinIntegrations = []builtinIntegration{
	{name: "github", label: "GitHub", kind: integration.KindHost, help: "Read through gh, with its own login: gh auth login.", docs: "https://cli.github.com"},
	{name: "github-actions", label: "GitHub Actions", kind: integration.KindCI, help: "Read through gh, with its own login: gh auth login.", docs: "https://docs.github.com/actions"},
	{name: "github-issues", label: "GitHub Issues", kind: integration.KindTickets, help: "Read through gh, with its own login: gh auth login.", docs: "https://docs.github.com/issues"},
	{name: "linear", label: "Linear", kind: integration.KindTickets, docs: "https://linear.app/developers",
		keyEnv: func(c config.Config) string { return firstSetStr(c.Trackers.Linear.APIKeyEnv, "LINEAR_API_KEY") },
		help:   "A personal API key, from Linear's Settings → Security & access. Its team is chosen in Settings → Trackers."},
	{name: "jira", label: "Jira", kind: integration.KindTickets, docs: "https://developer.atlassian.com/cloud/jira/platform/rest/v3/",
		keyEnv: func(c config.Config) string { return firstSetStr(c.Trackers.Jira.APITokenEnv, "JIRA_API_TOKEN") },
		help:   "An API token, from id.atlassian.com → Security → API tokens; your site's address and your email go in Settings → Trackers."},
}

func builtinNamed(name string) (builtinIntegration, bool) {
	for _, b := range builtinIntegrations {
		if b.name == name {
			return b, true
		}
	}
	return builtinIntegration{}, false
}

func builtinInfo(b builtinIntegration, asJSON bool) error {
	cfg, _ := config.Load()
	env := ""
	if b.keyEnv != nil {
		env = b.keyEnv(cfg)
	}
	set := env != "" && (keyFor(env) != "" || b.name == "linear" && cfg.Trackers.Linear.APIKey != "" || b.name == "jira" && cfg.Trackers.Jira.APIToken != "")
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(infoJSON{Name: b.name, Label: b.label, Kind: b.kind, Source: "built in", Installed: true, BuiltIn: true,
			Does: []integration.Feature{}, KeyEnv: env, KeyHelp: b.help, KeySet: set, Docs: b.docs})
	}
	fmt.Printf("%s · %s · built into conch\n\n%s\n", b.label, b.kind, b.help)
	switch {
	case env == "":
	case set:
		fmt.Printf("Its key ($%s) is set.\n", env)
	default:
		fmt.Printf("Its key is not set: conch integration key %s keeps it in the Keychain as $%s.\n", b.name, env)
	}
	return nil
}

func builtinKey(b builtinIntegration, fromStdin bool) error {
	cfg, _ := config.Load()
	if b.keyEnv == nil {
		fmt.Printf("%s has no key of conch's: %s\n", b.label, b.help)
		return nil
	}
	env := b.keyEnv(cfg)
	if keyFor(env) != "" && !fromStdin {
		fmt.Printf("%s's key is set already ($%s, or the Keychain)\n", b.label, env)
	} else {
		f := &integration.File{Name: b.name, Label: b.label, Kind: b.kind, KeyEnv: env}
		if _, err := askKey(f, fromStdin); err != nil {
			return err
		}
	}
	if b.name == "jira" && (cfg.Trackers.Jira.URL == "" || cfg.Trackers.Jira.Email == "") {
		fmt.Println("Jira also needs your site's address and your email: Settings → Trackers (or [trackers.jira] in config.toml)")
	}
	return nil
}
