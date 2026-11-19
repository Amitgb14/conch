package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/Amitgb14/conch/internal/sandbox"
)

type orphanJSON struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	State    string `json:"state"`
	Alias    string `json:"alias"`
}

type orphansJSON struct {
	Schema  int          `json:"schema"`
	Tool    string       `json:"tool"`
	Orphans []orphanJSON `json:"orphans"`
	// Problems are providers that could not be asked: their sandboxes
	// may be orphans too.
	Problems []string `json:"problems,omitempty"`
}

// findOrphans is conch-sandbox's sandboxes made for tool — ones whose
// alias says so — that are still there, running or keeping a disk, and
// that tool no longer has a machine for: made for it, then forgotten by
// it, and still billed. One made for another tool, or by hand, is in use
// somewhere this tool cannot see, so it is never this tool's orphan.
func findOrphans(ctx context.Context, tool string) ([]orphanJSON, []string, error) {
	machines, err := toolMachines(ctx, tool)
	if err != nil {
		return nil, nil, err
	}
	known := func(alias string) bool {
		for _, m := range machines {
			if m.has(alias) {
				return true
			}
		}
		return false
	}
	var out []orphanJSON
	var problems []string
	for _, name := range sandbox.Offered() {
		p, _, err := openProvider(name)
		if err != nil || p.Check() != nil || sshKind(p) == "" {
			continue // no key here: none of its sandboxes are this one's to see
		}
		list, err := p.List(ctx)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		for _, sb := range list {
			alias := aliasName(name, sb.ID)
			if sb.State.Going() || known(alias) {
				continue
			}
			if own, err := aliasOwner(alias); err != nil || own.ID != sb.ID || own.Tool != tool {
				continue // not made by conch-sandbox for tool: somebody else's to keep
			}
			out = append(out, orphanJSON{Provider: name, ID: sb.ID, Name: sb.Name, State: string(sb.State), Alias: alias})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out, problems, nil
}

func (a *app) orphans(ctx context.Context, args []string) error {
	c := a.command("orphans", "conch-sandbox orphans --tool herdr|herdr-app [--json] [--notify]")
	tool := c.String("tool", "", "herdr or herdr-app: whose machines to compare with")
	asJSON := c.Bool("json", false, "print one JSON object on stdout")
	notify := c.Bool("notify", false, "when there are any, say so in a notification (herdr)")
	rest, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return c.wrong("orphans takes no arguments")
	}
	if *tool != "herdr" && *tool != "herdr-app" {
		return c.wrong("give --tool herdr or --tool herdr-app")
	}
	list, problems, err := findOrphans(ctx, *tool)
	if err != nil {
		return err
	}
	for _, p := range problems {
		fmt.Fprintf(a.stderr, "  couldn't list %s\n", p)
	}
	if *notify && len(list) > 0 {
		a.notify(ctx, *tool, list)
	}
	if *asJSON {
		if list == nil {
			list = []orphanJSON{}
		}
		return a.printJSON(orphansJSON{Schema: schema, Tool: *tool, Orphans: list, Problems: problems})
	}
	if len(list) == 0 {
		fmt.Fprintf(a.stderr, "No sandbox of conch-sandbox's is missing from %s.\n", *tool)
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ALIAS\tPROVIDER\tID\tSTATE")
	for _, o := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", o.Alias, o.Provider, o.ID, o.State)
	}
	return tw.Flush()
}

// notify tells the person, in the tool, that sandboxes it forgot still
// cost. herdr shows a notification; the herdr app's own mod shows them
// itself, so it is said on stderr.
func (a *app) notify(ctx context.Context, tool string, list []orphanJSON) {
	what := "1 sandbox is"
	if len(list) != 1 {
		what = strconv.Itoa(len(list)) + " sandboxes are"
	}
	names := make([]string, 0, len(list))
	for _, o := range list {
		names = append(names, o.Alias)
	}
	body := fmt.Sprintf("%s still there with no %s machine: %s. Delete them from New sandbox, or with conch-sandbox rm.",
		what, tool, strings.Join(names, ", "))
	if tool == "herdr" {
		cmd := exec.CommandContext(ctx, herdrBin(), "notification", "show", "conch sandboxes", "--body", body)
		if out, err := cmd.CombinedOutput(); err == nil {
			return
		} else {
			fmt.Fprintf(a.stderr, "  couldn't notify herdr: %v %s\n", err, strings.TrimSpace(string(out)))
		}
	}
	fmt.Fprintln(a.stderr, body)
}

// ---- pick ----

// ask prints a question on stderr and reads the answer; def is what an
// empty answer means.
func (a *app) ask(question, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(a.stderr, "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(a.stderr, "%s: ", question)
	}
	s, err := a.readLine()
	if err != nil {
		return "", errors.New("no answer: the input ended")
	}
	if s = strings.TrimSpace(s); s == "" {
		return def, nil
	}
	return s, nil
}

// choose asks for one of options by number; def (0-based) is Enter.
func (a *app) choose(question string, options []string, def int) (int, error) {
	fmt.Fprintln(a.stderr, question)
	for i, o := range options {
		fmt.Fprintf(a.stderr, "  %d) %s\n", i+1, o)
	}
	for tries := 0; tries < 3; tries++ {
		s, err := a.ask("Number", strconv.Itoa(def+1))
		if err != nil {
			return 0, err
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		fmt.Fprintf(a.stderr, "  give a number from 1 to %d\n", len(options))
	}
	return 0, errors.New("no choice made")
}

// gitDefaults is the repository, branch and commit of the folder pick
// runs in, offered as what to check out. Nothing is said when it is not
// one, or has no https address to clone from.
func gitDefaults(ctx context.Context, dir string) repo {
	git := func(args ...string) string {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, io.Discard
		if cmd.Run() != nil {
			return ""
		}
		return strings.TrimSpace(out.String())
	}
	r := repo{URL: httpsOf(git("remote", "get-url", "origin"))}
	if r.URL == "" {
		return repo{}
	}
	if b := git("rev-parse", "--abbrev-ref", "HEAD"); b != "HEAD" {
		r.Branch = b
	}
	r.Ref = git("rev-parse", "HEAD")
	if r.check() != nil {
		return repo{}
	}
	return r
}

// httpsOf is a remote's https address: as it is, or from git@host:path.
func httpsOf(remote string) string {
	switch {
	case strings.HasPrefix(remote, "https://"):
		return remote
	case strings.HasPrefix(remote, "git@") && strings.Contains(remote, ":"):
		host, path, _ := strings.Cut(strings.TrimPrefix(remote, "git@"), ":")
		if host != "" && path != "" && !strings.Contains(host, "/") {
			return "https://" + host + "/" + strings.TrimPrefix(path, "/")
		}
	case strings.HasPrefix(remote, "ssh://git@"):
		rest := strings.TrimPrefix(remote, "ssh://git@")
		host, path, ok := strings.Cut(rest, "/")
		if ok && !strings.Contains(host, ":") {
			return "https://" + host + "/" + path
		}
	}
	return ""
}

// pick is herdr's New sandbox: a few questions on the terminal, then a
// sandbox with the branch checked out, a herdr machine for it and a
// workspace in the checkout. Sandboxes herdr has forgotten come first.
func (a *app) pick(ctx context.Context, args []string) error {
	c := a.command("pick", "conch-sandbox pick --tool herdr")
	tool := c.String("tool", "", "herdr: the tool to add the sandbox to")
	rest, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *tool != "herdr" {
		return c.wrong("give --tool herdr")
	}
	if !a.terminal {
		return errors.New("pick asks its questions at a terminal; with none, use conch-sandbox create")
	}

	if list, _, err := findOrphans(ctx, "herdr"); err != nil {
		fmt.Fprintf(a.stderr, "Couldn't look for forgotten sandboxes: %v\n", err)
	} else if len(list) > 0 {
		fmt.Fprintln(a.stderr, "These sandboxes are still there, and herdr has no machine for them:")
		for _, o := range list {
			ans, err := a.ask(fmt.Sprintf("  Delete %s (%s, %s)? y/N", o.Alias, sandbox.ProviderLabel(o.Provider), o.State), "")
			if err != nil {
				return err
			}
			if a := strings.ToLower(ans); a != "y" && a != "yes" {
				continue
			}
			p, _, err := ready(o.Provider)
			if err == nil {
				err = a.remove(ctx, p, o.ID, false, false)
			}
			if err != nil {
				fmt.Fprintf(a.stderr, "  couldn't delete %s: %v\n", o.Alias, err)
			}
		}
	}

	type choice struct {
		name string
		p    *sandbox.Custom
	}
	var usable []choice
	var unready []string
	for _, name := range sandbox.Offered() {
		p, _, err := openProvider(name)
		if err != nil || sshKind(p) == "" {
			continue
		}
		cp, ok := p.(*sandbox.Custom)
		if !ok {
			continue
		}
		if p.Check() != nil {
			unready = append(unready, name)
			continue
		}
		usable = append(usable, choice{name, cp})
	}
	if len(usable) == 0 {
		why := "none is installed: conch sandbox provider install NAME"
		if len(unready) > 0 {
			why = "none has a key: conch-sandbox key set " + unready[0]
		}
		return errors.New("no provider to make a sandbox with: " + why)
	}
	labels := make([]string, len(usable))
	for i, u := range usable {
		labels[i] = sandbox.ProviderLabel(u.name)
	}
	i, err := a.choose("Provider:", labels, 0)
	if err != nil {
		return err
	}
	pick := usable[i]

	def := gitDefaults(ctx, firstOf(a.dir, "."))
	name, err := a.ask("Name", firstOf(slug(def.Branch), "sandbox"))
	if err != nil {
		return err
	}
	req := createReq{provider: pick.name, name: name, root: true, tool: "herdr"}
	for _, s := range pick.p.Settings() {
		if s.Key != "region" || len(s.Choices) == 0 {
			continue
		}
		opts := append([]string{"the provider's default"}, s.Choices...)
		n, err := a.choose(firstOf(s.Label, "Region")+":", opts, 0)
		if err != nil {
			return err
		}
		if n > 0 {
			req.region = s.Choices[n-1]
		}
	}
	if sizes := pick.p.Sizes(); len(sizes.Names) > 0 {
		d := 0
		for i, n := range sizes.Names {
			if n == sizes.Default {
				d = i
			}
		}
		n, err := a.choose("Size:", sizes.Names, d)
		if err != nil {
			return err
		}
		req.size = sizes.Names[n]
	}
	url, err := a.ask("Repository to check out (- for none)", firstOf(def.URL, "-"))
	if err != nil {
		return err
	}
	if url != "-" {
		req.repo.URL = url
		branchDef := ""
		if url == def.URL {
			branchDef = def.Branch
		}
		if req.repo.Branch, err = a.ask("Branch", branchDef); err != nil {
			return err
		}
		// The commit here is offered only for the branch it is on.
		if url == def.URL && req.repo.Branch == def.Branch {
			req.repo.Ref = def.Ref
		}
	}

	r, al, err := a.makeSandbox(ctx, req)
	if err != nil {
		return err
	}
	label := sandbox.ProviderLabel(pick.name) + " " + firstOf(name, r.ID)
	add := exec.CommandContext(ctx, herdrBin(), "machine", "add", al.name(), "--label", label)
	add.Stdin, add.Stdout, add.Stderr = a.stdin, a.stderr, a.stderr
	if err := add.Run(); err != nil {
		return fmt.Errorf("herdr machine add %s: %v. The sandbox is kept: add it again, or delete it with conch-sandbox rm --machine %s",
			al.name(), err, al.name())
	}
	id := label
	if ms, err := toolMachines(ctx, "herdr"); err == nil {
		for _, m := range ms {
			if m.has(al.name()) && m.ID != "" {
				id = m.ID
			}
		}
	}
	ws := exec.CommandContext(ctx, herdrBin(), "--machine", id, "workspace", "create", "--cwd", al.Root, "--label", firstOf(name, r.ID), "--focus")
	ws.Stdout, ws.Stderr = io.Discard, a.stderr
	if err := ws.Run(); err != nil {
		fmt.Fprintf(a.stderr, "Couldn't open a workspace there (%v); open one on %s yourself.\n", err, label)
	}
	fmt.Fprintf(a.stdout, "%s: herdr machine %q, the checkout at %s\n", al.name(), label, al.Root)
	return nil
}
