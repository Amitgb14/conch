package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// conch integration adapt and publish: conch sandbox adapt and conch
// sandbox provider publish, for every kind. An agent of the person's
// choosing reads a service's API docs in a pane of its own and writes an
// integration file to the kind's contract; conch checks it offline, then
// — once the person has agreed to where the key goes, since the agent
// chose both the variable and the host — reads the service with the key
// itself. The agent never has the key: not in its prompt, its folder or
// its pane's environment. Publishing proposes the file and its index
// entry to the registry as a pull request, through gh, with that check's
// result in its body.

// integrationKinds are the kinds a file may be, in the sheet's order.
var integrationKinds = []string{integration.KindCI, integration.KindHost, integration.KindTickets, integration.KindSandbox, integration.KindDeploy, integration.KindNotify}

// integrationDraftDir is where an integration is adapted: the docs, the
// format, the file the agent writes, and which pane it is in.
func integrationDraftDir(name string) string {
	return filepath.Join(integration.Dir(), "drafts", name)
}

func integrationDraft(name string) string {
	return filepath.Join(integrationDraftDir(name), name+".toml")
}

// liveJSON is what reading the service with the person's key found.
type liveJSON struct {
	OK      bool   `json:"ok"`
	Runs    int    `json:"runs"`
	Project string `json:"project,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Why     string `json:"why,omitempty"`
	KeyEnv  string `json:"key_env"`
	// Goes is which key went where: $VAR to https://host.
	Goes string `json:"goes,omitempty"`
}

func (l liveJSON) String() string {
	if l.OK {
		return fmt.Sprintf("read %d runs of %s on %s", l.Runs, l.Project, firstSetStr(l.Branch, "its default branch"))
	}
	return "not checked: " + l.Why
}

// integrationKeyGoes says which key a live check sends and to which host:
// the address kept for a service run by the person, else the file's.
func integrationKeyGoes(f *integration.File) string {
	key := "$" + f.KeyEnv
	if f.Auth.UserEnv != "" {
		key += " (and $" + f.Auth.UserEnv + ")"
	}
	base := f.API
	if cfg, err := config.Load(); err == nil && cfg.Integrations.Base[f.Name] != "" {
		base = cfg.Integrations.Base[f.Name]
	}
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		base = u.Scheme + "://" + u.Host
	}
	return key + " to " + firstSetStr(base, "the address you give")
}

// checkLive reads the runs of the project checked out at dir with the
// person's key: one read-only call, conch's and never the agent's.
func checkLive(f *integration.File, dir string) liveJSON {
	l := liveJSON{KeyEnv: f.KeyEnv, Goes: integrationKeyGoes(f)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key := keyFor(f.KeyEnv)
	if key == "" {
		l.Why = fmt.Sprintf("put a %s key in $%s", f.DisplayName(), f.KeyEnv)
		if keychainHere() {
			l.Why += fmt.Sprintf(" or the Keychain (service conch-mcp, account %s)", f.KeyEnv)
		}
		return l
	}
	root, rem, branch := projectHere(ctx, dir)
	if root == "" || rem.Host == "" {
		l.Why = fmt.Sprintf("it reads a project's runs: run it in a checkout of a project on %s, or name one with -cwd DIR", f.DisplayName())
		return l
	}
	user := ""
	if f.Auth.UserEnv != "" {
		user = keyFor(f.Auth.UserEnv)
	}
	cfg, _ := config.Load()
	conn, err := f.Connect(key, user, cfg.Integrations.Base[f.Name])
	if err != nil {
		l.Why = err.Error()
		return l
	}
	runs, err := conn.Runs(ctx, integration.Target{Remote: rem, Branch: branch})
	if err != nil {
		l.Why = strings.ReplaceAll(err.Error(), key, "…")
		return l
	}
	l.OK, l.Runs, l.Project, l.Branch = true, len(runs), rem.Path, branch
	return l
}

// integrationKeylessEnv empties, in the agent's pane, every variable an
// API key is likely in — the sandbox providers', this service's likely
// names, every integration conch knows of, the code hosts' — and keeps
// conch run there from the Keychain. The agent works from the docs; it
// has no use for a key.
func integrationKeylessEnv(name string) []string {
	env := keylessEnv(name)
	more := []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "LINEAR_API_KEY", "JIRA_API_TOKEN"}
	files, _ := integration.All()
	for _, f := range files {
		more = append(more, f.KeyEnv)
	}
	for _, v := range more {
		if v != "" && !slicesHas(env, v+"=") {
			env = append(env, v+"=")
		}
	}
	return append(env, "CONCH_SECURITY=false")
}

func integrationAdaptPrompt(name, kind string, c integration.KindContract, urls []string) string {
	return fmt.Sprintf(`conch needs an integration file for the service %q, so it can use that service's API for the %s jobs conch already does.

Read INTEGRATION-FILE.md in this folder: it is the format, with an example. The service's API docs are in ./docs (from %s). Using only those, write ./%s.toml in that format, with format = 3, kind = %q and name = %q.

%s
Fill every operation the docs have a call for, and say in note what the service has no call for. Set docs to the address of the docs, adapted_by to your name and adapted to today's date.

Then run: conch integration check ./%s.toml
and fix what it reports until it reports nothing.

Do not call the service's API and do not look for an API key: conch checks the file with the key itself. Write nothing outside this folder.`,
		name, kind, strings.Join(urls, ", "), name, kind, name, c.Text, name)
}

// integrationAdapt is `conch integration adapt`.
func integrationAdapt(args []string) error {
	fs := flag.NewFlagSet("integration adapt", flag.ContinueOnError)
	kind := fs.String("kind", integration.KindCI, "what the service is for: "+strings.Join(integrationKinds, ", "))
	agent := fs.String("agent", "claude", "the agent that writes the file")
	name := fs.String("name", "", "the integration's name (default: from the URL)")
	asJSON := fs.Bool("json", false, "one JSON object a line, for the app")
	revise := fs.String("revise", "", "send this to the agent that wrote NAME's draft, and check again")
	check := fs.Bool("check", false, "check NAME's draft with your key (you agree to where it goes)")
	save := fs.Bool("save", false, "keep NAME's draft as an integration here")
	fresh := fs.Bool("new", false, "adapt it even when conch ships it or a registry lists it")
	dir := fs.String("cwd", "", "the project the live check reads (default: the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	usage := errors.New("usage: conch integration adapt [-kind K] [-agent A] [-name N] [-json] [-cwd DIR] URL... | -revise TEXT NAME | -check NAME | -save NAME")
	k := strings.ToLower(strings.TrimSpace(*kind))
	if k == "code-host" || k == "codehost" {
		k = integration.KindHost
	}
	if k == integration.KindSandbox {
		// Sandbox providers have their own file and command, which does
		// all of this already.
		if *check || *save {
			return errors.New("a sandbox provider's draft: conch sandbox provider check NAME, or save NAME")
		}
		sargs := []string{"-agent", *agent}
		if *name != "" {
			sargs = append(sargs, "-name", *name)
		}
		if *asJSON {
			sargs = append(sargs, "-json")
		}
		if *fresh {
			sargs = append(sargs, "-new")
		}
		if *revise != "" {
			sargs = append(sargs, "-revise", *revise)
		}
		return sandboxAdapt(append(sargs, fs.Args()...))
	}
	if !slicesHas(integrationKinds, k) {
		return fmt.Errorf("%q is not a kind of integration: %s", *kind, strings.Join(integrationKinds, ", "))
	}
	out := adaptOut{json: *asJSON, w: os.Stdout}
	fail := func(err error) error {
		if *asJSON && err != nil {
			out.say(adaptEvent{Event: "error", Detail: err.Error()})
		}
		return err
	}
	modes := 0
	for _, on := range []bool{*revise != "", *check, *save} {
		if on {
			modes++
		}
	}
	switch {
	case modes > 1:
		return usage
	case modes == 1:
		if fs.NArg() != 1 {
			return usage
		}
		n := fs.Arg(0)
		if err := integration.CheckName(n); err != nil {
			return fail(err)
		}
		st, err := readIntegrationDraft(n)
		if err != nil {
			return fail(err)
		}
		switch {
		case *save:
			return integrationSave(n)
		case *check:
			return fail(reportIntegrationDraft(n, st.Kind, st.Agent, *dir, true, out))
		}
		return fail(integrationRevise(st, *revise, *dir, out))
	}
	c, ok := integration.Contract(k)
	if !ok {
		return fail(fmt.Errorf("this conch reads no %s integration files yet: it reads ci, tickets, deploy and notify files (and sandbox providers, with -kind sandbox)", k))
	}
	if fs.NArg() == 0 {
		return usage
	}
	urls := fs.Args()
	n := strings.TrimSpace(*name)
	if n == "" {
		n = adaptName(urls[0])
	}
	if n == "" {
		return fail(fmt.Errorf("cannot tell the service's name from %s: say it with -name", urls[0]))
	}
	if err := integration.CheckName(n); err != nil {
		return fail(err)
	}
	if _, shipped, err := integration.Source(n); err == nil {
		if !shipped {
			return fail(fmt.Errorf("%s is already an integration here (%s): remove it first with conch integration remove %s", n, filepath.Join(integration.Dir(), n+".toml"), n))
		}
		if !*fresh {
			return fail(fmt.Errorf("%s ships with conch (conch integration info %s); -new adapts a file of your own to replace it", n, n))
		}
	}
	if !*fresh {
		if e, ok := integrationListed(urls, n); ok {
			out.say(adaptEvent{Event: "registry", Name: e.Name, Detail: fmt.Sprintf("the registry %s lists %s already (%s, %s): conch integration install %s (or adapt it anyway with -new)",
				e.Registry, firstSetStr(e.Label, e.Name), e.Kind, standing(e, true), e.Name)})
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), adaptWait+5*time.Minute)
	defer cancel()
	d := integrationDraftDir(n)
	_ = os.RemoveAll(d)
	docs, err := gatherDocs(ctx, d, urls, out)
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(d, "INTEGRATION-FILE.md"), []byte(integration.Guide), 0o600); err != nil {
		return fail(err)
	}
	cl, err := connect(true)
	if err != nil {
		return fail(err)
	}
	defer cl.Close()
	params := proto.PaneCreateParams{Name: "adapt " + n, Agent: *agent, Prompt: integrationAdaptPrompt(n, k, c, urls), Cwd: d, NoProject: true,
		Cols: 120, Rows: 40, Env: integrationKeylessEnv(n)}
	var info proto.PaneInfo
	if err := call(cl, proto.MethodPaneCreate, params, &info); err != nil {
		return fail(fmt.Errorf("start %s: %w", *agent, err))
	}
	st := adaptState{Name: n, Kind: k, Agent: *agent, Pane: info.ID, URLs: urls, Docs: docs}
	b, _ := json.Marshal(st)
	_ = os.WriteFile(filepath.Join(d, "adapt.json"), b, 0o600)
	out.say(adaptEvent{Event: "agent", Pane: info.ID, Name: *agent, Detail: fmt.Sprintf("%s is writing the file in pane %s (watch it there)", *agent, info.ID)})
	if err := awaitWriter(cl, info.ID, *agent, out); err != nil {
		return fail(err)
	}
	if err := reportIntegrationDraft(n, k, *agent, *dir, false, out); err != nil {
		return fail(err)
	}
	if !*asJSON && askYes("Keep "+n+" as an integration here?") {
		return integrationSave(n)
	}
	return nil
}

func readIntegrationDraft(name string) (adaptState, error) {
	b, err := os.ReadFile(filepath.Join(integrationDraftDir(name), "adapt.json"))
	if err != nil {
		return adaptState{}, fmt.Errorf("no draft of %s: start one with conch integration adapt URL", name)
	}
	var st adaptState
	if err := json.Unmarshal(b, &st); err != nil {
		return adaptState{}, err
	}
	st.Kind = firstSetStr(st.Kind, integration.KindCI)
	return st, nil
}

// integrationListed is a registry's entry that adapting urls as name
// would make again: by name, or by the API's host. A registry that cannot
// be read is no: adapting works without one.
func integrationListed(urls []string, name string) (integration.Entry, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ixs := indexes(ctx, io.Discard)
	if e, _, ok := findEntry(ixs, name); ok && e.Kind != integration.KindSandbox {
		return e, true
	}
	for _, raw := range urls {
		h := strings.ToLower(hostOfURL(raw))
		for _, ix := range ixs {
			for _, e := range ix.Entries {
				if e.Kind != integration.KindSandbox && h != "" && strings.EqualFold(e.Hosts.API, h) {
					return e, true
				}
			}
		}
	}
	return integration.Entry{}, false
}

// reportIntegrationDraft reads what the agent wrote, checks it offline,
// and — with the person's yes, or when agreed is their -check — reads the
// service with their key.
func reportIntegrationDraft(name, kind, agent, dir string, agreed bool, out adaptOut) error {
	path := integrationDraft(name)
	f, problems, err := integration.Read(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s finished without writing %s", agent, path)
	}
	if err != nil {
		return err
	}
	text, _ := os.ReadFile(path)
	out.say(adaptEvent{Event: "file", Name: agent, Path: path, Text: string(text), Kind: f.Kind, Operations: f.Operations(), Detail: f.Note})
	if f.Name != name {
		problems = append(problems, fmt.Sprintf("name is %q, not %q", f.Name, name))
	}
	if f.Kind != kind {
		problems = append(problems, fmt.Sprintf("kind is %q, not %q", f.Kind, kind))
	}
	if len(problems) > 0 {
		out.say(adaptEvent{Event: "problems", Problems: problems})
		return fmt.Errorf("the file is not right yet: conch integration adapt -revise \"…\" %s asks %s to change it", name, agent)
	}
	out.say(adaptEvent{Event: "offline", Detail: "0 problems", OK: true})
	goes := integrationKeyGoes(f)
	if !agreed && out.json {
		// The app asks the person, then runs -check.
		out.say(adaptEvent{Event: "consent", Name: name, Detail: goes})
	} else {
		var l liveJSON
		if agreed || adaptConfirm(fmt.Sprintf("Check it against the service? That sends %s.", goes)) {
			l = checkLive(f, dir)
		} else {
			l = liveJSON{KeyEnv: f.KeyEnv, Goes: goes, Why: fmt.Sprintf("it would send %s, which the file chose: read the file, then run conch integration adapt -check %s", goes, name)}
		}
		out.say(adaptEvent{Event: "live", Live: &l})
	}
	out.say(adaptEvent{Event: "done", Name: name, Path: path, OK: true,
		Detail: fmt.Sprintf("keep it here with: conch integration adapt -save %s · propose it to the registry with: conch integration publish %s", name, name)})
	return nil
}

// integrationRevise sends a change to the agent that wrote the draft, and
// checks the draft again.
func integrationRevise(st adaptState, text, dir string, out adaptOut) error {
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	var res proto.AgentPromptResult
	if err := call(c, proto.MethodAgentPrompt, proto.AgentPromptParams{ID: st.Pane, Text: text + "\n\nThen run conch integration check ./" + st.Name + ".toml again."}, &res); err != nil {
		return fmt.Errorf("pane %s: %w", st.Pane, err)
	}
	out.say(adaptEvent{Event: "agent", Pane: st.Pane, Name: st.Agent, Detail: fmt.Sprintf("%s is changing the file in pane %s", st.Agent, st.Pane)})
	if _, _, err := awaitTurn(c, res, map[string]bool{proto.AgentDone: true, proto.AgentIdle: true, proto.AgentBlocked: true}, "done", adaptWait); err != nil {
		return err
	}
	return reportIntegrationDraft(st.Name, st.Kind, st.Agent, dir, false, out)
}

// integrationSave keeps a clean draft as a file of the person's own.
func integrationSave(name string) error {
	f, problems, err := integration.Read(integrationDraft(name))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no draft of %s: start one with conch integration adapt URL", name)
	}
	if err != nil {
		return err
	}
	if len(problems) > 0 || f.Name != name {
		return fmt.Errorf("the draft is not right yet: conch integration check %s", integrationDraft(name))
	}
	path := filepath.Join(integration.Dir(), name+".toml")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s is already an integration here: remove it first with conch integration remove %s", name, name)
	}
	data, err := os.ReadFile(integrationDraft(name))
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	integration.Forget()
	_ = os.RemoveAll(integrationDraftDir(name))
	fmt.Printf("kept %s as %s: conch integration key %s gives it your key\n", name, path, name)
	return nil
}

// integrationPublishSource finds what publish proposes: a path, a file
// here, a draft, or one conch ships.
func integrationPublishSource(ref string) (string, []byte, error) {
	if strings.Contains(ref, "/") || strings.HasSuffix(ref, ".toml") {
		b, err := os.ReadFile(ref)
		return ref, b, err
	}
	if err := integration.CheckName(ref); err != nil {
		return "", nil, err
	}
	for _, p := range []string{filepath.Join(integration.Dir(), ref+".toml"), integrationDraft(ref)} {
		if b, err := os.ReadFile(p); err == nil {
			return p, b, nil
		}
	}
	if b, shipped, err := integration.Source(ref); err == nil && shipped {
		return ref + " (ships with conch)", b, nil
	}
	return "", nil, fmt.Errorf("no integration %s here: adapt one with conch integration adapt URL", ref)
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// integrationPublish is `conch integration publish`.
func integrationPublish(args []string) error {
	fs := flag.NewFlagSet("integration publish", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "show what would be proposed, and propose nothing")
	asJSON := fs.Bool("json", false, "the pull request as JSON")
	yes := fs.Bool("y", false, "check it with your key without asking where it goes")
	dir := fs.String("cwd", "", "the project the live check reads (default: the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch integration publish [-dry-run] [-json] [-y] [-cwd DIR] FILE|NAME")
	}
	where, data, err := integrationPublishSource(fs.Arg(0))
	if err != nil {
		return err
	}
	f, problems, err := integration.Parse(data)
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if f.Kind == integration.KindSandbox {
		return fmt.Errorf("%s is a sandbox provider: conch sandbox provider publish %s", f.Name, f.Name)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s has problems to fix first: conch integration check %s", where, fs.Arg(0))
	}
	name := f.Name
	if recs, _ := integration.Records(); recs[name].Version != 0 {
		if edited, _ := integration.Edited(name); !edited {
			return fmt.Errorf("%s is the registry's own, unchanged: nothing to publish", name)
		}
	}
	if u, err := url.Parse(f.Docs); err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%s says no docs = \"https://…\": say where it was adapted from, so a reviewer can check it", where)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Versions are never reused, so the next is numbered from the index:
	// without it there is no number to give.
	reg := integrationRegistries()[0]
	ix, err := integration.FetchIndex(ctx, reg)
	if err != nil {
		return fmt.Errorf("publish numbers the version from the registry's index, and could not read it: %w", err)
	}
	sum := sha256Hex(data)
	version := 1
	prev, listed := ix.Find(name)
	if listed {
		switch {
		case prev.SHA256 == sum:
			return fmt.Errorf("the registry has this file already, as %s version %d", name, prev.Version)
		case prev.Kind != f.Kind:
			return fmt.Errorf("the registry's %s is kind %s, not %s: give this one another name", name, prev.Kind, f.Kind)
		}
		version = prev.Version + 1
		for _, r := range prev.Revoked {
			version = max(version, r.Version+1)
		}
	}
	// Published files are ones that have worked: conch reads the service
	// with the key now, or nothing is proposed. Where the key goes is the
	// file's choice, so it is said first.
	goes := integrationKeyGoes(f)
	if !*yes && !adaptConfirm(fmt.Sprintf("Publishing checks %s with your key first: that sends %s. Go on?", name, goes)) {
		return fmt.Errorf("nothing published: it is checked with your key first, which sends %s (-y agrees)", goes)
	}
	live := checkLive(f, *dir)
	if !live.OK {
		return fmt.Errorf("publish checks %s with your key first, and could not: %s", name, live.Why)
	}
	today := publishNow().UTC().Format("2006-01-02")
	// The registry keeps a folder per kind (conch-integrations: ci/NAME/,
	// tickets/NAME/, …) holding the file as it is and a meta.json; its CI
	// builds the index entry from both.
	path := f.Kind + "/" + name + "/integration.toml"
	e := integration.Entry{Kind: f.Kind, Name: name, Label: f.DisplayName(), Version: version, Format: f.Format, File: path, SHA256: sum,
		Hosts: integration.Hosts{API: hostOfURL(f.API), Follow: f.FollowHosts}, KeyEnv: f.KeyEnv, UserEnv: f.Auth.UserEnv,
		Operations: f.Operations(), Tier: "community", Note: f.Note, Docs: f.Docs,
		AdaptedBy: firstSetStr(f.AdaptedBy, "a person"), Adapted: firstSetStr(f.Adapted, today)}
	// Checked stays empty: verified is a maintainer's word, given after
	// their own run; the submitter's check is in the pull request's body.
	metaJSON, _ := json.MarshalIndent(integrationMeta{Version: version, AdaptedBy: e.AdaptedBy, Adapted: e.Adapted,
		Docs: e.Docs, Conch: releaseVersion()}, "", "  ")
	metaJSON = append(metaJSON, '\n')
	title := fmt.Sprintf("Add %s (%s)", e.Label, e.Kind)
	if listed {
		title = fmt.Sprintf("Update %s to version %d", e.Label, version)
	}
	var body strings.Builder
	fmt.Fprintf(&body, "%s, a %s integration, version %d. Adapted by %s from %s.\n\n", e.Label, e.Kind, version, e.AdaptedBy, e.Docs)
	fmt.Fprintf(&body, "Live check with conch %s on %s: %s.\n\n", releaseVersion(), today, live)
	fmt.Fprintf(&body, "- your key ($%s) goes to `%s`\n", e.KeyEnv, firstSetStr(e.Hosts.API, "the address the person gives"))
	if len(e.Hosts.Follow) > 0 {
		fmt.Fprintf(&body, "- read without it: `%s`\n", strings.Join(e.Hosts.Follow, "`, `"))
	}
	fmt.Fprintf(&body, "- operations: %s\n", strings.Join(e.Operations, ", "))
	if listed && integration.HostsChanged(integration.Record{Hosts: prev.Hosts}, e) {
		fmt.Fprintf(&body, "\n**Hosts changed**: version %d reached %s.\n", prev.Version, hostWords(prev.Hosts))
	}
	body.WriteString("\nProposed with `conch integration publish`.\n")
	files := []registryFile{{path, data}, {f.Kind + "/" + name + "/meta.json", metaJSON}}
	if *dry {
		fmt.Printf("would propose to %s: %s\n\n%s", sandbox.RegistryRepo, title, body.String())
		for _, fl := range files {
			fmt.Printf("\n--- %s\n%s", fl.path, fl.data)
		}
		return nil
	}
	branch := fmt.Sprintf("integration-%s-%s", name, publishNow().UTC().Format("20060102-150405"))
	pr, err := proposeToRegistry(ctx, branch, title, body.String(), files)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"url": pr, "version": version})
	}
	fmt.Printf("proposed %s %d to the registry: %s\nit is listed once a maintainer has reviewed and merged it\n", name, version, pr)
	return nil
}

// integrationMeta is an integration's meta.json in the registry.
type integrationMeta struct {
	Version   int                  `json:"version"`
	AdaptedBy string               `json:"adapted_by"`
	Adapted   string               `json:"adapted"`
	Docs      string               `json:"docs"`
	Conch     string               `json:"conch"`
	Checked   *integration.Checked `json:"checked"`
}
