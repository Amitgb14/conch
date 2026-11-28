package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/sandbox"
	"github.com/Amitgb14/conch/internal/sshkey"
)

// struck stands for a secret — a token that is the ssh user name — in
// anything printed.
const struck = "…"

type sizeJSON struct {
	CPU       int `json:"cpu,omitempty"`
	MemoryGiB int `json:"memory_gib,omitempty"`
	DiskGiB   int `json:"disk_gib,omitempty"`
}

type sshJSON struct {
	Alias string `json:"alias,omitempty"`
	// Kind is "key" or "token": see sshKind.
	Kind string `json:"kind"`
	Host string `json:"host"`
	Port int    `json:"port"`
	// User is struck out when it is a token.
	User    string     `json:"user"`
	Expires *time.Time `json:"expires,omitempty"`
	// IdentityFile is the private key ssh offers, for the key kind.
	IdentityFile string `json:"identity_file,omitempty"`
}

// result is what every sandbox verb prints with --json.
type result struct {
	Schema      int       `json:"schema"`
	Provider    string    `json:"provider"`
	ID          string    `json:"id"`
	Name        string    `json:"name,omitempty"`
	State       string    `json:"state"`
	Region      string    `json:"region,omitempty"`
	Size        *sizeJSON `json:"size,omitempty"`
	ProjectRoot string    `json:"project_root,omitempty"`
	SSH         *sshJSON  `json:"ssh,omitempty"`
}

// verbOps is the operation each verb needs, as the registry names it.
var verbOps = map[string]string{"create": "create", "start": "start", "stop": "stop", "rm": "delete"}

// can refuses a verb the provider says it cannot do (its capabilities);
// one that says nothing is left to its file.
func can(provider, verb string) error {
	op := verbOps[verb]
	if op == "" {
		return nil
	}
	if c := sandbox.CapabilitiesOf(provider); !c.Can(op) {
		return fmt.Errorf("%s cannot %s: its file has no %s", sandbox.ProviderLabel(provider), verb, op)
	}
	return nil
}

func describe(provider string, s sandbox.Sandbox, region string) result {
	r := result{Schema: schema, Provider: provider, ID: s.ID, Name: s.Name, State: string(s.State), Region: s.Target}
	if r.Region == "" {
		r.Region = region
	}
	if s.Name == s.ID {
		r.Name = "" // the provider gave none: Custom fills in the ID
	}
	if s.CPU+s.Memory+s.Disk > 0 {
		r.Size = &sizeJSON{CPU: s.CPU, MemoryGiB: s.Memory, DiskGiB: s.Disk}
	}
	return r
}

// access readies the sandbox for an ssh login and says how to make it:
// the key handed over or offered for the key kind, a fresh token for the
// token kind. What is printed has the token struck out; the alias has it.
func access(ctx context.Context, p sandbox.Provider, id string) (*sshJSON, alias, error) {
	kind := sshKind(p)
	if kind == "" {
		return nil, alias{}, errors.New(sandbox.ProviderLabel(p.Name()) + "'s file says no ssh way in")
	}
	identity, err := sshkey.Authorize(ctx, p, id, nil)
	if err != nil {
		return nil, alias{}, fmt.Errorf("let your ssh key in: %w", err)
	}
	// The token goes into an alias, used later and again: as long a life
	// as the file allows.
	ac, err := p.SSHAccess(sandbox.LongLived(ctx), id)
	if err != nil {
		return nil, alias{}, err
	}
	al := alias{Provider: p.Name(), ID: id, Host: ac.Host, Port: ac.Port, User: ac.User, Identity: identity}
	if al.Port == 0 {
		al.Port = 22
	}
	s := &sshJSON{Kind: kind, Host: ac.Host, Port: al.Port, User: ac.User, IdentityFile: identity}
	if !ac.Expires.IsZero() {
		e := ac.Expires.UTC()
		s.Expires, al.Expires = &e, &e
	}
	if !ac.PlainUser {
		s.User, al.Token = struck, true
	}
	return s, al, nil
}

// say prints r: JSON on stdout, or a line or two for a person.
func (a *app) say(r result, asJSON bool) error {
	if asJSON {
		return a.printJSON(r)
	}
	what := r.ID
	if r.Name != "" {
		what += " (" + r.Name + ")"
	}
	fmt.Fprintf(a.stdout, "%s sandbox %s: %s\n", sandbox.ProviderLabel(r.Provider), what, r.State)
	if r.Size != nil {
		// What the provider did not say is left out, not called 0.
		var parts []string
		for _, p := range []struct {
			n    int
			unit string
		}{{r.Size.CPU, "vCPU"}, {r.Size.MemoryGiB, "GiB memory"}, {r.Size.DiskGiB, "GiB disk"}} {
			if p.n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", p.n, p.unit))
			}
		}
		fmt.Fprintf(a.stdout, "  size    %s\n", strings.Join(parts, ", "))
	}
	if r.Region != "" {
		fmt.Fprintf(a.stdout, "  region  %s\n", r.Region)
	}
	if s := r.SSH; s != nil {
		line := fmt.Sprintf("  ssh     %s@%s -p %d", s.User, s.Host, s.Port)
		if s.Alias != "" {
			line = "  ssh     " + s.Alias
		}
		if s.Expires != nil {
			line += " (until " + s.Expires.Format(time.RFC3339) + ")"
		}
		fmt.Fprintln(a.stdout, line)
	}
	return nil
}

// output is how a verb prints its result.
type output int

const (
	textOut output = iota
	jsonOut
	orcaOut // Orca's own result, with the real user name
)

// opts are the flags of a verb on one sandbox.
type opts struct {
	*command
	provider, machine, format   *string
	asJSON, fromOrca, fromHerdr *bool
}

// sandboxFlags declares them; orca says the verb can print Orca's result.
func (a *app) sandboxFlags(verb, usage string, orca bool) *opts {
	c := a.command(verb, usage)
	o := &opts{command: c, format: new(string), machine: new(string), fromHerdr: new(bool)}
	o.provider = c.String("provider", "", "the provider (conch-sandbox providers lists them)")
	o.asJSON = c.Bool("json", false, "print one JSON object on stdout")
	o.fromOrca = c.Bool("from-orca", false, "read the sandbox (or, for create, the workspace) from Orca's ORCA_* variables")
	if verb != "create" {
		o.machine = c.String("machine", "", "the sandbox behind this ssh alias, one of conch-sandbox's")
		o.fromHerdr = c.Bool("from-herdr", false, "the sandbox of the herdr workspace a plugin action runs in")
	}
	if orca {
		o.format = c.String("format", "", `"orca" prints Orca's result instead`)
	}
	return o
}

func (o *opts) output() (output, error) {
	switch {
	case *o.format == "":
	case *o.format != "orca":
		return 0, o.wrong(fmt.Sprintf("--format %q: the only one is orca", *o.format))
	case *o.asJSON:
		return 0, o.wrong("give --json or --format orca, not both")
	default:
		return orcaOut, nil
	}
	if *o.asJSON {
		return jsonOut, nil
	}
	return textOut, nil
}

// orcaInstance reads ORCA_VM_INSTANCE_ID: the userData create printed
// ({"provider": …, "id": …}) as Orca hands it back, or the id alone.
func orcaInstance(provider string) (string, string, error) {
	raw := strings.TrimSpace(os.Getenv("ORCA_VM_INSTANCE_ID"))
	if raw == "" {
		return "", "", errors.New("--from-orca: ORCA_VM_INSTANCE_ID is not set")
	}
	if !strings.HasPrefix(raw, "{") {
		return provider, raw, nil
	}
	var ud orcaUserData
	if err := json.Unmarshal([]byte(raw), &ud); err != nil || ud.ID == "" {
		return "", "", fmt.Errorf("--from-orca: ORCA_VM_INSTANCE_ID is neither an id nor create's userData: %q", raw)
	}
	if provider != "" && ud.Provider != "" && ud.Provider != provider {
		return "", "", fmt.Errorf("--from-orca: that sandbox is %s's, not %s's", ud.Provider, provider)
	}
	return firstOf(provider, ud.Provider), ud.ID, nil
}

// locate is the sandbox a verb is about, from exactly one of: an ID with
// --provider, --machine ALIAS, --from-orca or --from-herdr. An alias names
// its provider; one given beside it must agree.
func (a *app) locate(ctx context.Context, o *opts, rest []string) (provider, id string, err error) {
	given := 0
	for _, b := range []bool{len(rest) > 0, *o.machine != "", *o.fromOrca, *o.fromHerdr} {
		if b {
			given++
		}
	}
	if given != 1 || len(rest) > 1 {
		return "", "", o.wrong("give one sandbox: an ID with --provider, --machine ALIAS, --from-orca or --from-herdr")
	}
	provider = *o.provider
	alias := *o.machine
	switch {
	case len(rest) == 1:
		id = rest[0]
	case *o.fromOrca:
		if provider, id, err = orcaInstance(provider); err != nil {
			return "", "", err
		}
	case *o.fromHerdr:
		if alias, err = herdrAlias(ctx); err != nil {
			return "", "", err
		}
	}
	if alias != "" {
		own, err := aliasOwner(alias)
		if err != nil {
			return "", "", err
		}
		if provider != "" && provider != own.Provider {
			return "", "", fmt.Errorf("%s is a %s sandbox, not %s", alias, own.Provider, provider)
		}
		provider, id = own.Provider, own.ID
	}
	if provider == "" {
		return "", "", o.wrong("give --provider NAME (" + installedWords() + ")")
	}
	if err := sandbox.CheckName(provider); err != nil {
		return "", "", o.wrong(err.Error())
	}
	if err := checkID(id); err != nil {
		return "", "", o.wrong(err.Error())
	}
	return provider, id, nil
}

// target is a verb's sandbox, its provider ready to use.
type target struct {
	p   sandbox.Provider
	id  string
	out output
	o   *opts
}

func (a *app) one(ctx context.Context, verb, usage string, args []string, orca bool) (*target, error) {
	o := a.sandboxFlags(verb, usage, orca)
	rest, err := o.parse(args)
	if err != nil {
		return nil, err
	}
	out, err := o.output()
	if err != nil {
		return nil, err
	}
	provider, id, err := a.locate(ctx, o, rest)
	if err != nil {
		return nil, err
	}
	if err := can(provider, verb); err != nil {
		return nil, err
	}
	p, _, err := ready(provider)
	if err != nil {
		return nil, err
	}
	return &target{p: p, id: id, out: out, o: o}, nil
}

// gone says a provider no longer has the sandbox, in words naming it.
func gone(p sandbox.Provider, id string, err error) error {
	if errors.Is(err, sandbox.ErrNotFound) {
		return fmt.Errorf("%s has no sandbox %s", sandbox.ProviderLabel(p.Name()), id)
	}
	return err
}

// createReq is a sandbox to make.
type createReq struct {
	provider, name, region, size, image string
	repo                                repo
	// yes adds the Include line without asking; orca makes it not needed,
	// since Orca connects by the address it is given.
	yes, orca bool
	// root asks for the sandbox's home as the root when there is no
	// checkout: Orca and herdr open a workspace there.
	root bool
	// tool is who it is made for (tools), kept in its alias.
	tool string
}

// makeSandbox makes the sandbox, lets this computer's key in or asks for
// a token, writes its alias, and checks the repository out. Anything that
// fails once the sandbox exists deletes it.
func (a *app) makeSandbox(ctx context.Context, req createReq) (result, alias, error) {
	if err := req.repo.check(); err != nil {
		return result{}, alias{}, err
	}
	if err := can(req.provider, "create"); err != nil {
		return result{}, alias{}, err
	}
	p, cfg, err := ready(req.provider)
	if err != nil {
		return result{}, alias{}, err
	}
	// Asked before anything is made: a sandbox with an alias nothing
	// reads is one a tool cannot reach.
	if !req.orca || req.yes {
		if err := a.ensureInclude(req.yes); err != nil {
			return result{}, alias{}, err
		}
	}
	pc := cfg.Sandbox.Of(req.provider)
	env, err := sandbox.EnvFrom(pc.Env)
	if err != nil {
		return result{}, alias{}, err
	}
	spec := sandbox.Spec{Name: strings.TrimSpace(req.name), Region: req.region, Size: req.size, Snapshot: req.image, Env: env, AutoStop: pc.AutoStop}
	// conch's own sandboxes are named by the file: its prefix (conch-),
	// the name, a few letters. A file that sends the name as it is given
	// is handed one made that way, so every sandbox reads as conch's.
	if cp, ok := p.(*sandbox.Custom); ok && cp.NamedByLabel() {
		spec.Name = cp.NameFor(spec.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, a.wait)
	defer cancel()
	fmt.Fprintf(a.stderr, "Creating a %s sandbox…\n", sandbox.ProviderLabel(req.provider))
	sb, err := p.Create(ctx, spec)
	if err == nil {
		// The ID is the provider's reply, and it names an ssh alias in a
		// file ~/.ssh/config includes: one with a line break or a * would
		// write ssh settings of its own for every host. Held to what an
		// ID typed here may be; the sandbox is deleted (its ID goes into
		// that request escaped) rather than kept unreachable.
		if cerr := checkID(sb.ID); cerr != nil {
			err = fmt.Errorf("%s answered with a sandbox ID conch-sandbox will not use: %w", sandbox.ProviderLabel(req.provider), cerr)
		}
	}
	if err != nil {
		if sb.ID != "" {
			a.abandon(p, sb.ID)
		}
		return result{}, alias{}, err
	}
	fmt.Fprintf(a.stderr, "  sandbox %s started\n", sb.ID)
	r := describe(req.provider, sb, req.region)
	var al alias
	if r.SSH, al, err = access(ctx, p, sb.ID); err == nil {
		al.Tool = firstOf(req.tool, "cli")
		err = setAlias(al)
	}
	if err == nil {
		switch {
		case req.repo.URL != "":
			fmt.Fprintf(a.stderr, "  cloning %s\n", req.repo.URL)
			al.Root, err = a.remoteRoot(ctx, al, req.repo.cloneScript(req.repo.dirName(req.name)))
		case req.orca || req.root:
			al.Root, err = a.remoteRoot(ctx, al, homeScript)
		}
	}
	if err == nil && al.Root != "" {
		err = setAlias(al)
	}
	if err != nil {
		a.abandon(p, sb.ID)
		return result{}, alias{}, err
	}
	r.SSH.Alias, r.ProjectRoot = al.name(), al.Root
	fmt.Fprintf(a.stderr, "  ssh alias %s\n", al.name())
	return r, al, nil
}

func (a *app) create(ctx context.Context, args []string) error {
	o := a.sandboxFlags("create",
		"conch-sandbox create --provider P [--name N] [--repo URL [--branch B] [--ref SHA]] [--region R] [--size S] [--image I] [--tool T] [--yes] [--json | --format orca] [--from-orca]", true)
	tool := o.String("tool", "", "who it is for: herdr, herdr-app, orca or cli (default cli; orca with --from-orca or --format orca). A tool's sandbox gets its home as project_root, and is only ever that tool's orphan")
	name := o.String("name", "", "what it is for; the sandbox is named conch-NAME-…, the checkout ~/NAME")
	region := o.String("region", "", "where it runs: one the provider's file offers (default: config.toml's)")
	size := o.String("size", "", "a named size, for a provider whose machines come in them")
	image := o.String("image", "", "the image or snapshot to start from (default: config.toml's, or the provider's)")
	yes := o.Bool("yes", false, "add the Include line for the ssh aliases to ~/.ssh/config without asking")
	var rp repo
	o.StringVar(&rp.URL, "repo", "", "an https:// repository to clone into the sandbox's home")
	o.StringVar(&rp.Branch, "branch", "", "the branch to check out")
	o.StringVar(&rp.Ref, "ref", "", "the commit to check out (pushed)")
	rest, err := o.parse(args)
	if err != nil {
		return err
	}
	out, err := o.output()
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return o.wrong("create takes no arguments: " + strings.Join(rest, " "))
	}
	if *tool != "" && !slices.Contains(tools, *tool) {
		return o.wrong(fmt.Sprintf("--tool %q: one of %s", *tool, strings.Join(tools, ", ")))
	}
	if *o.fromOrca || out == orcaOut {
		if *tool != "" && *tool != "orca" {
			return o.wrong("--tool " + *tool + " with Orca's --from-orca or --format orca")
		}
		*tool = "orca"
	}
	*tool = firstOf(*tool, "cli")
	if *o.fromOrca {
		// Orca's workspace says what is wanted; a flag given as well wins.
		// Its repository's address is usually git@…, cloned over https.
		*name = firstOf(*name, os.Getenv("ORCA_WORKSPACE_NAME"))
		if rp.URL == "" {
			u := os.Getenv("ORCA_REPO_URL")
			rp.URL = firstOf(httpsOf(u), u)
		}
		rp.Branch = firstOf(rp.Branch, os.Getenv("ORCA_REPO_BRANCH"))
		rp.Ref = firstOf(rp.Ref, os.Getenv("ORCA_REPO_REF"))
	}
	if *o.provider == "" {
		return o.wrong("give --provider NAME (" + installedWords() + ")")
	}
	r, al, err := a.makeSandbox(ctx, createReq{provider: *o.provider, name: *name, region: *region, size: *size, image: *image,
		repo: rp, yes: *yes, orca: out == orcaOut, tool: *tool, root: *tool != "cli"})
	if err != nil {
		return err
	}
	return a.emit(r, al, out, firstOf(*name, r.Name, r.ID))
}

// emit prints a result as the verb was asked to.
func (a *app) emit(r result, al alias, out output, label string) error {
	switch out {
	case orcaOut:
		return a.sayOrca(r, al, label)
	case jsonOut:
		return a.printJSON(r)
	}
	return a.say(r, false)
}

// abandon deletes a sandbox whose making failed, and its alias: it costs
// while it runs, and nobody has it to delete. It is given time of its own,
// since the failure may have been the deadline or an interrupt.
func (a *app) abandon(p sandbox.Provider, id string) {
	_, _ = removeAlias(p.Name(), id)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := p.Delete(ctx, id); err != nil && !errors.Is(err, sandbox.ErrNotFound) {
		fmt.Fprintf(a.stderr, "  couldn't delete sandbox %s (%v); delete it with: conch-sandbox rm --provider %s %s\n", id, err, p.Name(), id)
		return
	}
	sandbox.NoteDeleted(p.Name(), id, time.Now())
	fmt.Fprintf(a.stderr, "  deleted sandbox %s\n", id)
}

func (a *app) get(ctx context.Context, args []string) error {
	t, err := a.one(ctx, "get", "conch-sandbox get (--provider P ID | --machine ALIAS | --from-orca | --from-herdr) [--json]", args, false)
	if err != nil {
		return err
	}
	sb, err := t.p.Get(ctx, t.id)
	if err != nil {
		return gone(t.p, t.id, err)
	}
	r := describe(t.p.Name(), sb, "")
	// What its alias holds, as create said it: get asks the provider for
	// no new token and lets no key in, so a sandbox with no alias has no
	// ssh here (conch-sandbox ssh-alias writes one).
	if al, ok := readAlias(aliasName(t.p.Name(), t.id)); ok {
		r.ProjectRoot, r.SSH = al.Root, sshOf(al)
	}
	return a.say(r, t.out == jsonOut)
}

// start wakes a sandbox and says how to reach it, which for a token
// provider is a fresh token.
func (a *app) start(ctx context.Context, args []string) error {
	t, err := a.one(ctx, "start", "conch-sandbox start (--provider P ID | --machine ALIAS | --from-orca | --from-herdr) [--json | --format orca]", args, true)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, a.wait)
	defer cancel()
	fmt.Fprintf(a.stderr, "Starting %s…\n", t.id)
	sb, err := t.p.Start(ctx, t.id)
	if err != nil {
		return gone(t.p, t.id, err)
	}
	if sb.ID == "" {
		sb.ID = t.id
	}
	r := describe(t.p.Name(), sb, "")
	var al alias
	if r.SSH, al, err = access(ctx, t.p, t.id); err != nil {
		return err
	}
	// An alias made for it gets the new token, or the new address of a
	// sandbox that came back on another machine. One never made is left
	// to ssh-alias, which asks about ~/.ssh/config — but Orca, which
	// connects by the address, is given one, and its checkout's root.
	if hasAlias(al.name()) || t.out == orcaOut {
		if !hasAlias(al.name()) {
			al.Tool = "orca"
		}
		if err := setAlias(al); err != nil {
			return err
		}
		own, _ := aliasOwner(al.name())
		al.Root = own.Root
		if al.Root == "" && t.out == orcaOut {
			if al.Root, err = a.remoteRoot(ctx, al, homeScript); err != nil {
				return err
			}
			if err := setAlias(al); err != nil {
				return err
			}
		}
		r.SSH.Alias, r.ProjectRoot = al.name(), al.Root
	}
	label := firstOf(r.Name, r.ID)
	if *t.o.fromOrca {
		label = firstOf(os.Getenv("ORCA_WORKSPACE_NAME"), label) // as create named it
	}
	return a.emit(r, al, t.out, label)
}

func (a *app) stop(ctx context.Context, args []string) error {
	t, err := a.one(ctx, "stop", "conch-sandbox stop (--provider P ID | --machine ALIAS | --from-orca | --from-herdr) [--json]", args, false)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, a.wait)
	defer cancel()
	fmt.Fprintf(a.stderr, "Stopping %s…\n", t.id)
	if err := t.p.Stop(ctx, t.id); err != nil {
		return gone(t.p, t.id, err)
	}
	return a.say(result{Schema: schema, Provider: t.p.Name(), ID: t.id, State: string(sandbox.StateStopped)}, t.out == jsonOut)
}

// rm deletes a sandbox and its alias. One already gone is not an error: a
// tool deleting what it made wants it gone, and it is. Run from herdr,
// herdr's machine for it goes too.
func (a *app) rm(ctx context.Context, args []string) error {
	t, err := a.one(ctx, "rm", "conch-sandbox rm (--provider P ID | --machine ALIAS | --from-orca | --from-herdr) [--json]", args, false)
	if err != nil {
		return err
	}
	return a.remove(ctx, t.p, t.id, t.out == jsonOut, *t.o.fromHerdr)
}

func (a *app) remove(ctx context.Context, p sandbox.Provider, id string, asJSON, herdr bool) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	switch err := p.Delete(ctx, id); {
	case errors.Is(err, sandbox.ErrNotFound):
		fmt.Fprintf(a.stderr, "  %s was already gone from %s\n", id, sandbox.ProviderLabel(p.Name()))
	case err != nil:
		return err
	default:
		sandbox.NoteDeleted(p.Name(), id, time.Now())
	}
	name := aliasName(p.Name(), id)
	if herdr {
		a.forgetInHerdr(ctx, name)
	}
	if removed, err := removeAlias(p.Name(), id); err != nil {
		return err
	} else if removed {
		fmt.Fprintf(a.stderr, "  removed ssh alias %s\n", name)
	}
	return a.say(result{Schema: schema, Provider: p.Name(), ID: id, State: "deleted"}, asJSON)
}

// sshAlias writes, or with --remove takes out, a sandbox's ssh alias.
func (a *app) sshAlias(ctx context.Context, args []string) error {
	o := a.sandboxFlags("ssh-alias", "conch-sandbox ssh-alias (--provider P ID | --machine ALIAS) [--remove] [--yes] [--json]", false)
	remove := o.Bool("remove", false, "take the alias out rather than write it")
	yes := o.Bool("yes", false, "add the Include line to ~/.ssh/config without asking")
	rest, err := o.parse(args)
	if err != nil {
		return err
	}
	if *remove {
		// No provider is asked: its file may be gone, and the alias with it.
		provider, id, err := a.locate(ctx, o, rest)
		if err != nil {
			return err
		}
		name := aliasName(provider, id)
		removed, err := removeAlias(provider, id)
		if err != nil {
			return err
		}
		if !removed {
			fmt.Fprintf(a.stderr, "no ssh alias %s\n", name)
		}
		if *o.asJSON {
			return a.printJSON(aliasJSON{Schema: schema, Provider: provider, ID: id, Alias: name, Removed: removed})
		}
		if removed {
			fmt.Fprintf(a.stdout, "removed %s\n", name)
		}
		return nil
	}
	provider, id, err := a.locate(ctx, o, rest)
	if err != nil {
		return err
	}
	if err := a.ensureInclude(*yes); err != nil {
		return err
	}
	p, _, err := ready(provider)
	if err != nil {
		return err
	}
	sb, err := p.Get(ctx, id)
	if err != nil {
		return gone(p, id, err)
	}
	if sb.State != sandbox.StateStarted {
		return fmt.Errorf("sandbox %s is %s: start it first (conch-sandbox start --provider %s %s)", id, sb.State, provider, id)
	}
	r := describe(p.Name(), sb, "")
	var al alias
	if r.SSH, al, err = access(ctx, p, id); err != nil {
		return err
	}
	if err := setAlias(al); err != nil {
		return err
	}
	r.SSH.Alias = al.name()
	if *o.asJSON {
		return a.printJSON(r)
	}
	fmt.Fprintln(a.stdout, al.name())
	return nil
}

type aliasJSON struct {
	Schema   int    `json:"schema"`
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Alias    string `json:"alias"`
	Removed  bool   `json:"removed"`
}
