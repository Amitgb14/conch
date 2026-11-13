package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// Adapting a provider conch does not know: conch downloads its API docs,
// an agent of the person's choosing reads them in a pane of its own and
// writes a provider file (internal/sandbox/custom.go), and conch checks
// that file — its shape, then the list call with the person's key — and
// keeps it as a draft until it is saved. The key is conch's alone: it is
// never put in the agent's prompt or folder, and the variables the
// providers conch knows read their keys from are emptied in its pane.

// adaptDocsMax bounds one downloaded document.
const adaptDocsMax = 4 << 20

// adaptWait is how long an agent is given to write the file.
var adaptWait = 20 * time.Minute

// docsClient fetches the docs; tests point it elsewhere.
var docsClient = &http.Client{Timeout: 30 * time.Second}

// draftDir is where a provider is adapted: the docs, the guide, the file
// the agent writes, and which pane it is in.
func draftDir(name string) string {
	return filepath.Join(sandbox.ProvidersDir(), "drafts", name)
}

type adaptState struct {
	Name  string   `json:"name"`
	Agent string   `json:"agent"`
	Pane  string   `json:"pane"`
	URLs  []string `json:"urls"`
	Docs  []string `json:"docs"`
}

// adaptEvent is a line of `conch sandbox adapt -json`, for the app's sheet.
type adaptEvent struct {
	Event    string             `json:"event"` // registry, docs, agent, waiting, blocked, file, problems, check, done, error
	Detail   string             `json:"detail,omitempty"`
	Name     string             `json:"name,omitempty"`
	Pane     string             `json:"pane,omitempty"`
	Path     string             `json:"path,omitempty"`
	Text     string             `json:"text,omitempty"`
	Mapping  []sandbox.Mapping  `json:"mapping,omitempty"`
	Problems []string           `json:"problems,omitempty"`
	Check    *providerCheckJSON `json:"check,omitempty"`
	OK       bool               `json:"ok,omitempty"`
}

type adaptOut struct {
	json bool
	w    io.Writer
}

func (o adaptOut) say(e adaptEvent) {
	if o.json {
		b, _ := json.Marshal(e)
		fmt.Fprintln(o.w, string(b))
		return
	}
	switch e.Event {
	case "docs":
		fmt.Fprintln(o.w, "docs:", e.Detail)
	case "registry", "agent", "waiting", "blocked":
		fmt.Fprintln(o.w, e.Detail)
	case "file":
		fmt.Fprintf(o.w, "\n%s wrote %s:\n", e.Name, e.Path)
		for _, m := range e.Mapping {
			fmt.Fprintf(o.w, "  %-13s %s", m.Does, m.Call)
			if m.Reads != "" {
				fmt.Fprintf(o.w, "  → %s", m.Reads)
			}
			fmt.Fprintln(o.w)
		}
	case "problems":
		fmt.Fprintln(o.w, "\nwhat is wrong with it:")
		for _, p := range e.Problems {
			fmt.Fprintln(o.w, "  -", p)
		}
	case "check":
		printCheck(o.w, e.Check)
	case "error":
		fmt.Fprintln(o.w, "error:", e.Detail)
	case "done":
		fmt.Fprintln(o.w, "\n"+e.Detail)
	}
}

// adaptName is a provider's name from where its API lives:
// api.tensorlake.ai is tensorlake.
func adaptName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	parts := strings.Split(strings.ToLower(u.Hostname()), ".")
	if len(parts) < 2 {
		return ""
	}
	name := parts[len(parts)-2]
	if len(parts) >= 3 && len(name) <= 3 && (name == "co" || name == "com") { // acme.co.uk
		name = parts[len(parts)-3]
	}
	name = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(name, "-")
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return ""
	}
	return name
}

// docsCandidates are where a provider's API description usually is, when
// what was pasted is the API itself (which answers with a 401, not docs).
func docsCandidates(raw string) []string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	host := u.Hostname()
	// An address has no docs site beside it to guess at.
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return nil
	}
	parts := strings.Split(host, ".")
	root := host
	if len(parts) > 2 {
		root = strings.Join(parts[len(parts)-2:], ".")
	}
	var out []string
	for _, h := range []string{"docs." + root, host, root} {
		for _, p := range []string{"/llms.txt", "/openapi.json", "/openapi.yaml", "/api-reference/openapi.yaml", "/api-reference/openapi.json", "/docs/openapi.json"} {
			c := "https://" + h + p
			if !slicesHas(out, c) {
				out = append(out, c)
			}
		}
	}
	return out
}

func slicesHas(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// fetchDoc downloads one document, refusing what is not docs: an error
// answer, something that is not text, or more than adaptDocsMax.
func fetchDoc(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("%s: not a web address", raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "conch-adapt")
	resp, err := docsClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s answered %d", raw, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, adaptDocsMax+1))
	if err != nil {
		return nil, err
	}
	if len(data) > adaptDocsMax {
		return nil, fmt.Errorf("%s is over %d MB", raw, adaptDocsMax>>20)
	}
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "image/") || strings.Contains(ct, "octet-stream") || strings.Contains(ct, "zip") {
		return nil, fmt.Errorf("%s is %s, not docs", raw, ct)
	}
	if len(strings.TrimSpace(string(data))) < 200 {
		return nil, fmt.Errorf("%s is too short to be docs", raw)
	}
	return data, nil
}

// docName is a file name for a downloaded document.
func docName(i int, raw string) string {
	u, _ := url.Parse(raw)
	base := "doc"
	if u != nil {
		base = strings.Trim(regexp.MustCompile(`[^A-Za-z0-9.-]+`).ReplaceAllString(u.Hostname()+u.Path, "-"), "-.")
	}
	if len(base) > 80 {
		base = base[len(base)-80:]
	}
	return fmt.Sprintf("%02d-%s", i+1, base)
}

// gatherDocs downloads what was given and, when none of it is docs, looks
// where docs usually are.
func gatherDocs(ctx context.Context, dir string, urls []string, out adaptOut) ([]string, error) {
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o700); err != nil {
		return nil, err
	}
	var got []string
	try := func(raw string) bool {
		data, err := fetchDoc(ctx, raw)
		if err != nil {
			out.say(adaptEvent{Event: "docs", Detail: err.Error()})
			return false
		}
		name := docName(len(got), raw)
		if err := os.WriteFile(filepath.Join(dir, "docs", name), data, 0o600); err != nil {
			return false
		}
		got = append(got, raw)
		out.say(adaptEvent{Event: "docs", Detail: fmt.Sprintf("%s (%d KB)", raw, (len(data)+1023)/1024), OK: true})
		return true
	}
	for _, u := range urls {
		try(u)
	}
	if len(got) == 0 {
		for _, c := range docsCandidates(urls[0]) {
			if ctx.Err() != nil {
				break
			}
			if try(c) && (strings.HasSuffix(c, ".json") || strings.HasSuffix(c, ".yaml")) {
				break // an API description is enough
			}
		}
	}
	if len(got) == 0 {
		return nil, errors.New("found no docs there: give the URL of the API's docs or its OpenAPI file")
	}
	return got, nil
}

func adaptPrompt(name string) string {
	return fmt.Sprintf(`conch needs a provider file for the sandbox provider %q, so it can make and reach sandboxes there.

Read PROVIDER-FILE.md in this folder: it is the format, with an example. The provider's API docs are in ./docs. Using only those, write ./provider.toml describing %s's API in that format, with name = %q. Map every state the docs list. If the provider has no ssh way into a sandbox, write NO_SSH.txt saying so and why, instead.

Then run: conch sandbox provider check -offline ./provider.toml
and fix what it reports until it reports nothing.

Do not call the provider's API and do not look for an API key: conch tests the file with the key itself. Write nothing outside this folder.`, name, name, name)
}

// sandboxAdapt is `conch sandbox adapt`.
func sandboxAdapt(args []string) error {
	fs := flag.NewFlagSet("sandbox adapt", flag.ContinueOnError)
	agent := fs.String("agent", "claude", "the agent that writes the file")
	name := fs.String("name", "", "the provider's name (default: from the URL)")
	asJSON := fs.Bool("json", false, "one JSON object a line, for the app")
	revise := fs.String("revise", "", "send this to the agent that wrote NAME's draft, and check again")
	fresh := fs.Bool("new", false, "adapt it even when the registry has the provider already")
	if err := fs.Parse(args); err != nil {
		return err
	}
	out := adaptOut{json: *asJSON, w: os.Stdout}
	fail := func(err error) error {
		// -revise passes its outcome through here, success and all.
		if *asJSON && err != nil {
			out.say(adaptEvent{Event: "error", Detail: err.Error()})
		}
		return err
	}
	if *revise != "" {
		if fs.NArg() != 1 {
			return errors.New("usage: conch sandbox adapt -revise TEXT NAME")
		}
		return fail(adaptRevise(fs.Arg(0), *revise, out))
	}
	if fs.NArg() == 0 {
		return errors.New("usage: conch sandbox adapt [-agent A] [-name N] [-json] URL...")
	}
	urls := fs.Args()
	n := *name
	if n == "" {
		n = adaptName(urls[0])
	}
	if n == "" {
		return fail(fmt.Errorf("cannot tell the provider's name from %s: say it with -name", urls[0]))
	}
	if err := sandbox.CheckName(n); err != nil {
		return fail(err)
	}
	if sandbox.Builtin(n) {
		return fail(fmt.Errorf("conch has %s built in: set its key in Settings → Sandboxes, or run conch sandbox -provider %s create", sandbox.ProviderLabel(n), n))
	}
	if _, err := os.Stat(sandbox.ProviderFilePath(n)); err == nil {
		return fail(fmt.Errorf("%s is already a provider (%s): remove it first with conch sandbox provider rm %s", n, sandbox.ProviderFilePath(n), n))
	}
	// No need to spend an agent's time on what somebody has adapted and
	// published already.
	if !*fresh {
		if e, ok := registryHas(urls, n); ok {
			out.say(adaptEvent{Event: "registry", Name: e.Name, Detail: fmt.Sprintf("%s is in the provider registry already, %s: conch sandbox provider install %s (or adapt it anyway with -new)", e.Label, liveCheck(e), e.Name)})
			if *asJSON {
				return nil
			}
			fmt.Println()
			describeEntry(os.Stdout, e)
			fmt.Println()
			if askYes("Install it rather than adapt it?") {
				ctx, cancel := registryCtx()
				defer cancel()
				return installEntry(ctx, e, true)
			}
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), adaptWait+5*time.Minute)
	defer cancel()
	dir := draftDir(n)
	_ = os.RemoveAll(dir)
	docs, err := gatherDocs(ctx, dir, urls, out)
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PROVIDER-FILE.md"), []byte(sandbox.ProviderFileGuide), 0o600); err != nil {
		return fail(err)
	}
	c, err := connect(true)
	if err != nil {
		return fail(err)
	}
	defer c.Close()
	params := proto.PaneCreateParams{Name: "adapt " + n, Agent: *agent, Prompt: adaptPrompt(n), Cwd: dir, NoProject: true, Cols: 120, Rows: 40, Env: keylessEnv(n)}
	var info proto.PaneInfo
	if err := call(c, proto.MethodPaneCreate, params, &info); err != nil {
		return fail(fmt.Errorf("start %s: %w", *agent, err))
	}
	st := adaptState{Name: n, Agent: *agent, Pane: info.ID, URLs: urls, Docs: docs}
	b, _ := json.Marshal(st)
	_ = os.WriteFile(filepath.Join(dir, "adapt.json"), b, 0o600)
	out.say(adaptEvent{Event: "agent", Pane: info.ID, Name: *agent, Detail: fmt.Sprintf("%s is writing the file in pane %s (watch it there)", *agent, info.ID)})
	if err := awaitWriter(c, info.ID, *agent, out); err != nil {
		return fail(err)
	}
	if err := reportDraft(n, *agent, out); err != nil {
		return fail(err)
	}
	if !*asJSON && askYes("Save "+n+" as a provider?") {
		return providerSave(n)
	}
	return nil
}

// keylessEnv empties, in the agent's pane, the variables API keys are
// usually in: the built-in providers', and the one this provider's would
// likely be called. The agent works from the docs; it has no use for one.
func keylessEnv(name string) []string {
	env := []string{"DAYTONA_API_KEY=", "BOAT_API_KEY=", "DEVIN_API_KEY=", "TENSORLAKE_API_KEY=", "E2B_API_KEY="}
	v := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	return append(env, v+"_API_KEY=", v+"_KEY=", v+"_TOKEN=")
}

// awaitWriter waits for the agent to start and then to finish, saying when
// it is asking something.
func awaitWriter(c *client.Client, pane, agent string, out adaptOut) error {
	busy := map[string]bool{proto.AgentWorking: true, proto.AgentBlocked: true, proto.AgentDone: true}
	_, state, err := awaitState(c, pane, busy, "working", 3*time.Minute, nil)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(adaptWait)
	for {
		switch state {
		case proto.AgentDone, proto.AgentIdle:
			return nil
		case proto.AgentBlocked:
			out.say(adaptEvent{Event: "blocked", Pane: pane, Detail: fmt.Sprintf("%s is asking something in pane %s: answer it there", agent, pane)})
			if _, state, err = awaitState(c, pane, map[string]bool{proto.AgentWorking: true, proto.AgentDone: true, proto.AgentIdle: true}, "answered", time.Until(deadline), nil); err != nil {
				return err
			}
			continue
		}
		left := time.Until(deadline)
		if left <= 0 {
			return fmt.Errorf("%s did not finish in %s; it is still in pane %s", agent, adaptWait, pane)
		}
		out.say(adaptEvent{Event: "waiting", Pane: pane, Detail: agent + " is reading the docs…"})
		if _, state, err = awaitState(c, pane, map[string]bool{proto.AgentDone: true, proto.AgentIdle: true, proto.AgentBlocked: true}, "done", left, nil); err != nil {
			return err
		}
	}
}

// reportDraft reads what the agent wrote and says what it makes of it.
func reportDraft(name, agent string, out adaptOut) error {
	dir := draftDir(name)
	if b, err := os.ReadFile(filepath.Join(dir, "NO_SSH.txt")); err == nil {
		return fmt.Errorf("%s says the provider has no ssh way in, so conch cannot run there: %s", agent, strings.TrimSpace(string(b)))
	}
	path := filepath.Join(dir, "provider.toml")
	f, problems, err := sandbox.ReadProviderFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s finished without writing %s", agent, path)
	}
	if err != nil {
		return err
	}
	text, _ := os.ReadFile(path)
	out.say(adaptEvent{Event: "file", Name: agent, Path: path, Text: string(text), Mapping: f.Describe()})
	if f.Name != name {
		problems = append(problems, fmt.Sprintf("name is %q, not %q", f.Name, name))
	}
	if len(problems) > 0 {
		out.say(adaptEvent{Event: "problems", Problems: problems})
		return fmt.Errorf("the file is not right yet: conch sandbox adapt -revise \"…\" %s asks %s to change it", name, agent)
	}
	// Only with the person's yes, at a terminal: the agent wrote both the
	// variable and the host. The app (-json) shows the file and leaves the
	// check to conch sandbox provider check, run once they have read it.
	ck := checkProviderAsking(f, func(goes string) bool {
		return !out.json && adaptConfirm(fmt.Sprintf("Check it against the API? That sends %s.", goes))
	})
	out.say(adaptEvent{Event: "check", Check: &ck})
	next := fmt.Sprintf("save it with: conch sandbox provider save %s", name)
	if !ck.Signed {
		next = fmt.Sprintf("%s (the key check did not pass: %s)", next, ck.Why)
	}
	out.say(adaptEvent{Event: "done", Name: name, Path: path, Detail: next, OK: ck.Signed})
	return nil
}

// adaptRevise sends a change to the agent that wrote the draft, and
// checks the draft again.
func adaptRevise(name, text string, out adaptOut) error {
	if err := sandbox.CheckName(name); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(draftDir(name), "adapt.json"))
	if err != nil {
		return fmt.Errorf("no draft of %s: start one with conch sandbox adapt URL", name)
	}
	var st adaptState
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	var res proto.AgentPromptResult
	if err := call(c, proto.MethodAgentPrompt, proto.AgentPromptParams{ID: st.Pane, Text: text + "\n\nThen run conch sandbox provider check -offline ./provider.toml again."}, &res); err != nil {
		return fmt.Errorf("pane %s: %w", st.Pane, err)
	}
	out.say(adaptEvent{Event: "agent", Pane: st.Pane, Name: st.Agent, Detail: fmt.Sprintf("%s is changing the file in pane %s", st.Agent, st.Pane)})
	if _, _, err := awaitTurn(c, res, map[string]bool{proto.AgentDone: true, proto.AgentIdle: true, proto.AgentBlocked: true}, "done", adaptWait); err != nil {
		return err
	}
	return reportDraft(name, st.Agent, out)
}

// providerCheckJSON is what checking a provider file found.
type providerCheckJSON struct {
	Problems []string `json:"problems,omitempty"`
	// Signed says the key was taken and the list call answered.
	Signed bool   `json:"signed"`
	All    int    `json:"all"`
	Mine   int    `json:"mine"`
	Why    string `json:"why,omitempty"`
	KeyEnv string `json:"key_env"`
}

// checkProvider asks the provider for its list with the key, making
// nothing.
func checkProvider(f *sandbox.ProviderFile) providerCheckJSON {
	return checkProviderAsking(f, func(string) bool { return true })
}

// adaptConfirm asks the person a yes-or-no question; tests answer it.
var adaptConfirm = askYes

// checkProviderAsking is checkProvider once ask has agreed to where the
// key goes. A file an agent drafted names both the variable the key is
// read from and the host it is sent to, so checking it unasked would send
// any variable of the person's — $GITHUB_TOKEN, say — wherever docs the
// agent read told it to.
func checkProviderAsking(f *sandbox.ProviderFile, ask func(goes string) bool) providerCheckJSON {
	ck := providerCheckJSON{KeyEnv: f.KeyEnv}
	cfg := loadSandboxCfg().Of(f.Name)
	p := sandbox.NewCustom(f, cfg)
	if err := p.Check(); err != nil {
		ck.Why = strings.TrimPrefix(err.Error(), sandbox.ErrNotConfigured.Error()+": ")
		return ck
	}
	if goes := keyGoesTo(f, cfg); !ask(goes) {
		ck.Why = fmt.Sprintf("it would send %s, which the file chose: read the file, then run conch sandbox provider check %s", goes, f.Name)
		return ck
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	all, mine, err := p.Count(ctx)
	if err != nil {
		ck.Why = err.Error()
		return ck
	}
	ck.Signed, ck.All, ck.Mine = true, all, mine
	return ck
}

// keyGoesTo says which key a check sends and to which host, as NewCustom
// will pick them: a key saved in Settings before the file's variable, and
// an API address set there before the file's.
func keyGoesTo(f *sandbox.ProviderFile, cfg config.ProviderCfg) string {
	key := "$" + f.KeyEnv
	switch {
	case strings.TrimSpace(cfg.APIKey) != "":
		key = "the " + f.Name + " key saved in Settings"
	case cfg.APIKeyEnv != "":
		key = "$" + cfg.APIKeyEnv
	}
	host := f.API
	if u, err := url.Parse(f.API); err == nil && u.Host != "" {
		host = u.Scheme + "://" + u.Host
	}
	return key + " to " + host
}

func printCheck(w io.Writer, ck *providerCheckJSON) {
	if ck == nil {
		return
	}
	for _, p := range ck.Problems {
		fmt.Fprintln(w, "  -", p)
	}
	switch {
	case ck.Signed:
		fmt.Fprintf(w, "\nsigned in with $%s: the list answered, %d sandboxes (%d of them conch's)\n", ck.KeyEnv, ck.All, ck.Mine)
	case ck.Why != "":
		fmt.Fprintf(w, "\nnot checked against the API: %s\n", ck.Why)
	}
}

// sandboxProviderCmd is `conch sandbox provider check|show|save|rm`.
func sandboxProviderCmd(args []string) error {
	usage := errors.New("usage: conch sandbox provider check [-offline] [-json] NAME|FILE | show NAME | save NAME | rm NAME | search [TERM] | install [-y] [NAME] | update [-y] [NAME] | publish [-dry-run] NAME")
	if len(args) == 0 {
		return usage
	}
	switch args[0] {
	case "check":
		return providerCheck(args[1:])
	case "show":
		if len(args) != 2 {
			return usage
		}
		path, err := providerPath(args[1])
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	case "save":
		if len(args) != 2 {
			return usage
		}
		return providerSave(args[1])
	case "search":
		return providerSearch(args[1:])
	case "install":
		return providerInstall(args[1:])
	case "update":
		return providerUpdate(args[1:])
	case "publish":
		return providerPublish(args[1:])
	case "rm", "remove":
		if len(args) != 2 {
			return usage
		}
		return providerRemove(args[1])
	}
	return usage
}

// providerPath finds a provider's file: a path, a saved provider, or a
// draft.
func providerPath(ref string) (string, error) {
	if strings.ContainsAny(ref, "/.") {
		return ref, nil
	}
	for _, p := range []string{sandbox.ProviderFilePath(ref), filepath.Join(draftDir(ref), "provider.toml")} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no provider file for %s", ref)
}

func providerCheck(args []string) error {
	fs := flag.NewFlagSet("sandbox provider check", flag.ContinueOnError)
	offline := fs.Bool("offline", false, "check the file only, not the API")
	asJSON := fs.Bool("json", false, "JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch sandbox provider check [-offline] [-json] NAME|FILE")
	}
	path, err := providerPath(fs.Arg(0))
	if err != nil {
		return err
	}
	f, problems, err := sandbox.ReadProviderFile(path)
	if err != nil {
		return err
	}
	ck := providerCheckJSON{Problems: problems, KeyEnv: f.KeyEnv}
	if len(problems) == 0 && !*offline {
		ck = checkProvider(f)
	}
	if *asJSON {
		b, _ := json.Marshal(struct {
			providerCheckJSON
			Mapping []sandbox.Mapping `json:"mapping"`
		}{ck, f.Describe()})
		fmt.Println(string(b))
	} else {
		if len(problems) == 0 {
			fmt.Println(path + ": no problems")
		} else {
			fmt.Println(path + ":")
		}
		printCheck(os.Stdout, &ck)
	}
	if len(problems) > 0 {
		return exitStatus(1)
	}
	return nil
}

func providerSave(name string) error {
	if err := sandbox.CheckName(name); err != nil {
		return err
	}
	if sandbox.Builtin(name) {
		return fmt.Errorf("%s is built into conch", name)
	}
	draft := filepath.Join(draftDir(name), "provider.toml")
	f, problems, err := sandbox.ReadProviderFile(draft)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no draft of %s: start one with conch sandbox adapt URL", name)
	}
	if err != nil {
		return err
	}
	if len(problems) > 0 || f.Name != name {
		return fmt.Errorf("the draft is not right yet: conch sandbox provider check %s", name)
	}
	b, err := os.ReadFile(draft)
	if err != nil {
		return err
	}
	if err := os.WriteFile(sandbox.ProviderFilePath(name), b, 0o600); err != nil {
		return err
	}
	_ = os.RemoveAll(draftDir(name))
	fmt.Printf("saved %s: conch sandbox -provider %s create makes one\n", sandbox.ProviderFilePath(name), name)
	return nil
}

// providerRemove forgets a provider file. Its sandboxes are not touched:
// they are still the provider's, and still cost.
func providerRemove(name string) error {
	if err := sandbox.CheckName(name); err != nil {
		return err
	}
	if sandbox.Builtin(name) {
		return fmt.Errorf("%s is built into conch and cannot be removed", name)
	}
	path := sandbox.ProviderFilePath(name)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no provider %s", name)
	}
	if ms := machinesOn(name); len(ms) > 0 {
		return fmt.Errorf("%s still has machines here (%s): delete those sandboxes first, or they would go on costing with no way to stop them from conch", name, strings.Join(ms, ", "))
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	_ = os.RemoveAll(draftDir(name))
	if err := sandbox.ForgetInstalled(name); err != nil {
		return err
	}
	fmt.Println("removed", path)
	return nil
}

// askYes asks on a terminal, and is no when nobody can answer.
func askYes(q string) bool {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return false
	}
	fmt.Print(q + " [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y")
}

func loadSandboxCfg() config.SandboxCfg {
	cfg, err := config.Load()
	if err != nil {
		return config.SandboxCfg{}
	}
	return cfg.Sandbox
}

// machinesOn names the machines that are sandboxes of provider.
func machinesOn(provider string) []string {
	ms, err := remote.Machines()
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range ms {
		if p, _, ok := remote.ParseSandboxTarget(m.Target); ok && p == provider {
			out = append(out, m.Label)
		}
	}
	return out
}
