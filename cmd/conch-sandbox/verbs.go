package main

import (
	"context"
	"errors"
	"fmt"
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
// token kind. A provider whose file has no way in gives nil.
func access(ctx context.Context, p sandbox.Provider, id string) (*sshJSON, string, error) {
	kind := sshKind(p)
	if kind == "" {
		return nil, "", nil
	}
	identity, err := sshkey.Authorize(ctx, p, id, nil)
	if err != nil {
		return nil, "", fmt.Errorf("let your ssh key in: %w", err)
	}
	ac, err := p.SSHAccess(ctx, id)
	if err != nil {
		return nil, "", err
	}
	s := &sshJSON{Kind: kind, Host: ac.Host, Port: ac.Port, User: ac.User, IdentityFile: identity}
	if s.Port == 0 {
		s.Port = 22
	}
	if !ac.Expires.IsZero() {
		e := ac.Expires.UTC()
		s.Expires = &e
	}
	secret := ""
	if !ac.PlainUser {
		secret, s.User = ac.User, struck
	}
	return s, secret, nil
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
		fmt.Fprintf(a.stdout, "  size    %d vCPU, %d GiB memory, %d GiB disk\n", r.Size.CPU, r.Size.MemoryGiB, r.Size.DiskGiB)
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

// sandboxFlags are the flags every verb on one sandbox takes.
func (a *app) sandboxCommand(verb, usage string) (*command, *string, *bool) {
	c := a.command(verb, usage)
	provider := c.String("provider", "", "the provider (conch-sandbox providers lists them)")
	asJSON := c.Bool("json", false, "print one JSON object on stdout")
	return c, provider, asJSON
}

// one parses a verb on one sandbox: its provider, ready to use, and its ID.
func (a *app) one(verb, usage string, args []string) (p sandbox.Provider, id string, asJSON bool, err error) {
	c, provider, j := a.sandboxCommand(verb, usage)
	rest, err := c.parse(args)
	if err != nil {
		return nil, "", false, err
	}
	if len(rest) != 1 {
		return nil, "", false, c.wrong("give one sandbox ID")
	}
	if err := checkID(rest[0]); err != nil {
		return nil, "", false, c.wrong(err.Error())
	}
	if *provider == "" {
		return nil, "", false, c.wrong("give --provider NAME (" + installedWords() + ")")
	}
	if err := can(*provider, verb); err != nil {
		return nil, "", false, err
	}
	p, _, err = ready(*provider)
	return p, rest[0], *j, err
}

// gone says a provider no longer has the sandbox, in words naming it.
func gone(p sandbox.Provider, id string, err error) error {
	if errors.Is(err, sandbox.ErrNotFound) {
		return fmt.Errorf("%s has no sandbox %s", sandbox.ProviderLabel(p.Name()), id)
	}
	return err
}

func (a *app) create(ctx context.Context, args []string) error {
	c, provider, asJSON := a.sandboxCommand("create",
		"conch-sandbox create --provider P [--name N] [--region R] [--size S] [--image I] [--json]")
	name := c.String("name", "", "what it is for; the sandbox is named conch-NAME-…")
	region := c.String("region", "", "where it runs: one the provider's file offers (default: config.toml's)")
	size := c.String("size", "", "a named size, for a provider whose machines come in them")
	image := c.String("image", "", "the image or snapshot to start from (default: config.toml's, or the provider's)")
	rest, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return c.wrong("create takes no arguments: " + strings.Join(rest, " "))
	}
	if *provider == "" {
		return c.wrong("give --provider NAME (" + installedWords() + ")")
	}
	if err := can(*provider, "create"); err != nil {
		return err
	}
	p, cfg, err := ready(*provider)
	if err != nil {
		return err
	}
	pc := cfg.Sandbox.Of(*provider)
	env, err := sandbox.EnvFrom(pc.Env)
	if err != nil {
		return err
	}
	spec := sandbox.Spec{Name: strings.TrimSpace(*name), Region: *region, Size: *size, Snapshot: *image, Env: env, AutoStop: pc.AutoStop}
	// conch's own sandboxes are named by the file: its prefix (conch-),
	// the name, a few letters. A file that sends the name as it is given
	// is handed one made that way, so every sandbox reads as conch's.
	if cp, ok := p.(*sandbox.Custom); ok && cp.NamedByLabel() {
		spec.Name = cp.NameFor(spec.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, a.wait)
	defer cancel()
	fmt.Fprintf(a.stderr, "Creating a %s sandbox…\n", sandbox.ProviderLabel(*provider))
	sb, err := p.Create(ctx, spec)
	if err != nil {
		if sb.ID != "" {
			a.abandon(p, sb.ID)
		}
		return err
	}
	fmt.Fprintf(a.stderr, "  sandbox %s started\n", sb.ID)
	r := describe(*provider, sb, *region)
	if r.SSH, _, err = access(ctx, p, sb.ID); err != nil {
		a.abandon(p, sb.ID)
		return err
	}
	return a.say(r, *asJSON)
}

// abandon deletes a sandbox whose making failed: it costs while it runs,
// and nobody has it to delete. It is given time of its own, since the
// failure may have been the deadline or an interrupt.
func (a *app) abandon(p sandbox.Provider, id string) {
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
	p, id, asJSON, err := a.one("get", "conch-sandbox get --provider P ID [--json]", args)
	if err != nil {
		return err
	}
	sb, err := p.Get(ctx, id)
	if err != nil {
		return gone(p, id, err)
	}
	return a.say(describe(p.Name(), sb, ""), asJSON)
}

// start wakes a sandbox and says how to reach it, which for a token
// provider is a fresh token.
func (a *app) start(ctx context.Context, args []string) error {
	p, id, asJSON, err := a.one("start", "conch-sandbox start --provider P ID [--json]", args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, a.wait)
	defer cancel()
	fmt.Fprintf(a.stderr, "Starting %s…\n", id)
	sb, err := p.Start(ctx, id)
	if err != nil {
		return gone(p, id, err)
	}
	if sb.ID == "" {
		sb.ID = id
	}
	r := describe(p.Name(), sb, "")
	if r.SSH, _, err = access(ctx, p, id); err != nil {
		return err
	}
	return a.say(r, asJSON)
}

func (a *app) stop(ctx context.Context, args []string) error {
	p, id, asJSON, err := a.one("stop", "conch-sandbox stop --provider P ID [--json]", args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, a.wait)
	defer cancel()
	fmt.Fprintf(a.stderr, "Stopping %s…\n", id)
	if err := p.Stop(ctx, id); err != nil {
		return gone(p, id, err)
	}
	return a.say(result{Schema: schema, Provider: p.Name(), ID: id, State: string(sandbox.StateStopped)}, asJSON)
}

// rm deletes a sandbox. One already gone is not an error: a tool deleting
// what it made wants it gone, and it is.
func (a *app) rm(ctx context.Context, args []string) error {
	p, id, asJSON, err := a.one("rm", "conch-sandbox rm --provider P ID [--json]", args)
	if err != nil {
		return err
	}
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
	return a.say(result{Schema: schema, Provider: p.Name(), ID: id, State: "deleted"}, asJSON)
}
