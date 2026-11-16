package integration

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Format is the provider file format this conch reads for an integration.
// Sandbox files were formats 1 and 2; 3 adds kind and operations, so a
// conch from before it refuses such a file as having unknown keys, rather
// than reading a CI file as a sandbox provider with nothing in it.
const Format = 3

// The kinds of integration: which contract a file fills.
const (
	KindSandbox = "sandbox"
	KindCI      = "ci"
	KindTickets = "tickets"
	KindNotify  = "notify"
	KindHost    = "host"
)

// File is the shape of an integration file.
type File struct {
	Format int    `toml:"format"`
	Kind   string `toml:"kind"`
	Name   string `toml:"name"`
	Label  string `toml:"label"`
	// API is the base every call's path is put after; the key goes only
	// there. With BaseURL "ask", the person gives it at install (a
	// self-hosted Jenkins or GitLab) and API, if any, is the default.
	API     string `toml:"api"`
	BaseURL string `toml:"base_url"`
	KeyEnv  string `toml:"key_env"`
	// AuthHeader and AuthValue send the key as sandbox files do; Auth says
	// it otherwise. One or the other.
	AuthHeader string `toml:"auth_header"`
	AuthValue  string `toml:"auth_value"`
	Auth       Auth   `toml:"auth"`
	// FollowHosts are the hosts a URL found in a reply may be fetched
	// from — never with the key.
	FollowHosts []string `toml:"follow_hosts"`
	Docs        string   `toml:"docs"`
	AdaptedBy   string   `toml:"adapted_by"`
	Adapted     string   `toml:"adapted"`
	Note        string   `toml:"note"`
	// KeyHelp says where the key is made and what access it needs.
	KeyHelp string `toml:"key_help"`

	Detect  Detect            `toml:"detect"`
	Project Project           `toml:"project"`
	States  map[string]string `toml:"states"`

	// The ci kind's operations.
	Runs      RunsOp    `toml:"runs"`
	FailedLog LogOp     `toml:"failed_log"`
	Rerun     Operation `toml:"rerun"`
	Stats     RunsOp    `toml:"stats"`

	// shipped is set for a file conch carries rather than one installed.
	shipped bool
}

// Auth is how the key is sent. Type "header" (the default) sends Header:
// Value, Value holding {key}; "bearer" sends Authorization: Bearer KEY,
// with the Scopes the person should give the token; "basic" sends the
// user from UserEnv and the key as a password.
type Auth struct {
	Type    string   `toml:"type"`
	Header  string   `toml:"header"`
	Value   string   `toml:"value"`
	Scopes  []string `toml:"scopes"`
	UserEnv string   `toml:"user_env"`
}

// Detect says which projects use the service: a file in the checkout, a
// host in the remote's URL, a pull request check's name (a glob).
type Detect struct {
	Files   []string `toml:"files"`
	Remotes []string `toml:"remotes"`
	Checks  []string `toml:"checks"`
}

// Project makes the service's name for a project, {project}, out of its
// remote: {host} {owner} {repo} {path} (owner/repo, or group/sub/repo)
// and {path_escaped} (that path as one URL-escaped segment).
type Project struct {
	ID string `toml:"id"`
}

// Operation is one of the kind's jobs: one call, or a chain of steps.
type Operation struct {
	Call   string         `toml:"call"`
	Body   map[string]any `toml:"body"`
	Follow string         `toml:"follow"`
	Text   string         `toml:"text"`
	Steps  []Step         `toml:"steps"`
}

// RunsOp lists runs: Items is where the list is in the last step's
// value, and each field a path into an item — or, holding a {, a
// template over {item.x} and the steps.
type RunsOp struct {
	Operation
	Items    string `toml:"items"`
	ID       string `toml:"id"`
	Name     string `toml:"name"`
	State    string `toml:"state"`
	Commit   string `toml:"commit"`
	URL      string `toml:"url"`
	Started  string `toml:"started"`
	Finished string `toml:"finished"`
}

// LogOp is a failed run's output, {run} its id; Job names what failed,
// as a template over the steps.
type LogOp struct {
	Operation
	Job string `toml:"job"`
}

// Step is one call, or one per item of an earlier step's list.
type Step struct {
	// As names its value for the steps after it and the templates:
	// {as.x}. {reply.x} is the step just before, named or not.
	As   string         `toml:"as"`
	Call string         `toml:"call"`
	Body map[string]any `toml:"body"`
	// Each names an earlier step whose value is a list: the call is made
	// once per item, {item}, and the texts put together.
	Each string `toml:"each"`
	// Pick is what is kept of a JSON reply; all of it when empty. Nothing
	// picked ends the operation: there is no such thing.
	Pick string `toml:"pick"`
	// Follow is where URLs are in the reply; each is fetched, without the
	// key, from a follow host, and read by Text.
	Follow string `toml:"follow"`
	// Text makes the value text: "." the reply as it is, or a path into
	// a JSON reply whose values are put together a line each.
	Text string `toml:"text"`
}

// steps is the operation as steps, a lone call being one.
func (op Operation) steps() []Step {
	if len(op.Steps) > 0 || op.Call == "" {
		return op.Steps
	}
	return []Step{{Call: op.Call, Body: op.Body, Follow: op.Follow, Text: op.Text}}
}

// Has says whether the file fills the operation.
func (op Operation) Has() bool { return op.Call != "" || len(op.Steps) > 0 }

// contract is what a kind's file must and may fill.
type contract struct {
	required, optional []string
	states             []string
}

var contracts = map[string]contract{
	KindCI: {required: []string{"runs"}, optional: []string{"failed_log", "rerun", "stats"},
		states: []string{"pass", "fail", "pending", "canceled"}},
}

// Operations lists the operations the file fills, in the contract's order.
func (f *File) Operations() []string {
	var out []string
	for _, o := range []struct {
		name string
		op   Operation
	}{{"runs", f.Runs.Operation}, {"failed_log", f.FailedLog.Operation}, {"rerun", f.Rerun}, {"stats", f.Stats.Operation}} {
		if o.op.Has() {
			out = append(out, o.name)
		}
	}
	return out
}

// Hosts are every host the file reaches: its api's, with the key, then
// the follow hosts, without it.
func (f *File) Hosts() []string {
	var out []string
	if h := hostOf(f.API); h != "" {
		out = append(out, h)
	}
	return append(out, f.FollowHosts...)
}

// Problems lists what is wrong with the file, or nothing.
func (f *File) Problems() []string {
	var p []string
	add := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }
	switch {
	case f.Format > Format:
		add("format %d: this conch reads up to format %d; update conch to use it", f.Format, Format)
	case f.Format != Format:
		add("format = %d: an integration file says so, so that an older conch refuses it", Format)
	}
	c, known := contracts[f.Kind]
	switch {
	case known:
	case f.Kind == KindSandbox:
		add("kind %q: a sandbox provider is read by conch sandbox, from providers/", f.Kind)
	case f.Kind == KindTickets || f.Kind == KindNotify || f.Kind == KindHost:
		add("kind %q: this conch does not read it yet", f.Kind)
	default:
		add("kind %q: one of ci, tickets, notify, host, sandbox", f.Kind)
	}
	if !nameRE.MatchString(f.Name) {
		add("name %q: lowercase letters, digits and -, starting with a letter", f.Name)
	}
	switch f.BaseURL {
	case "":
		if !webURL(f.API) {
			add("api %q: an https:// URL", f.API)
		}
	case "ask":
		if f.API != "" && !webURL(f.API) {
			add("api %q: an https:// URL, the default for base_url", f.API)
		}
	default:
		add(`base_url %q: "ask", for the person to give it at install, or left out`, f.BaseURL)
	}
	if !envRE.MatchString(f.KeyEnv) {
		add("key_env %q: the name of an environment variable, e.g. ACME_TOKEN", f.KeyEnv)
	}
	f.checkAuth(add)
	for _, h := range f.FollowHosts {
		if h == "" || strings.ContainsAny(h, "/:@ *") {
			add("follow_hosts %q: a host name, nothing else", h)
		}
	}
	if len(f.Detect.Files)+len(f.Detect.Remotes)+len(f.Detect.Checks) == 0 {
		add("[detect] files, remotes or checks: how conch knows a project uses it")
	}
	for _, name := range f.Detect.Files {
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			add("[detect] files %q: a path inside the checkout", name)
		}
	}
	for _, h := range f.Detect.Remotes {
		if h == "" || strings.ContainsAny(h, "/:@ ") {
			add("[detect] remotes %q: a host name", h)
		}
	}
	for _, g := range f.Detect.Checks {
		if strings.TrimSpace(g) == "" {
			add("[detect] checks: an empty pattern")
		}
	}
	if f.Project.ID == "" {
		add("[project] id: the service's name for a project, e.g. gh/{owner}/{repo}")
	}
	for _, ref := range placeholders(f.Project.ID) {
		if !remoteParts[ref] {
			add("[project] id: {%s} is not a part of the remote ({host} {owner} {repo} {path} {path_escaped})", ref)
		}
	}
	if !known {
		sort.Strings(p)
		return p
	}
	ours := map[string]bool{}
	for _, s := range c.states {
		ours[s] = true
	}
	mapped := map[string]bool{}
	for theirs, s := range f.States {
		if !ours[s] {
			add("[states] %s = %q: one of %s", theirs, s, strings.Join(c.states, ", "))
		}
		mapped[s] = true
	}
	if !mapped["pass"] || !mapped["fail"] {
		add("[states] maps nothing to pass or to fail")
	}
	for _, r := range c.required {
		if !slicesHas(f.Operations(), r) {
			add("[%s]: required for kind %s", r, f.Kind)
		}
	}
	f.checkRuns("runs", f.Runs, add)
	if f.Stats.Has() {
		f.checkRuns("stats", f.Stats, add)
	}
	if f.FailedLog.Has() {
		names := f.checkSteps("failed_log", f.FailedLog.Operation, runPlaceholders, add)
		if steps := f.FailedLog.steps(); len(steps) > 0 && steps[len(steps)-1].Text == "" {
			add("[failed_log] the last step is the log: give it text")
		}
		checkTemplate("failed_log", "job", f.FailedLog.Job, names, add)
	}
	if f.Rerun.Has() {
		f.checkSteps("rerun", f.Rerun, runPlaceholders, add)
	}
	f.checkKey(add)
	sort.Strings(p)
	return p
}

func (f *File) checkAuth(add func(string, ...any)) {
	a := f.Auth
	if (f.AuthHeader != "" || f.AuthValue != "") && (a.Type != "" || a.Header != "" || a.Value != "") {
		add("auth_header/auth_value and [auth]: say it once")
	}
	if f.AuthValue != "" && !strings.Contains(f.AuthValue, "{key}") {
		add("auth_value %q never sends {key}", f.AuthValue)
	}
	switch a.Type {
	case "", "header":
		if a.Value != "" && !strings.Contains(a.Value, "{key}") {
			add("[auth] value %q never sends {key}", a.Value)
		}
	case "bearer", "basic":
		if a.Header != "" || a.Value != "" {
			add("[auth] %s sends the key itself: no header or value", a.Type)
		}
	default:
		add("[auth] type %q: header, bearer or basic", a.Type)
	}
	if a.Type == "basic" {
		if !envRE.MatchString(a.UserEnv) {
			add("[auth] user_env %q: the variable the user name is read from", a.UserEnv)
		}
	} else if a.UserEnv != "" {
		add("[auth] user_env is for basic")
	}
	if len(a.Scopes) > 0 && a.Type != "bearer" {
		add("[auth] scopes are for bearer")
	}
}

// remoteParts fill [project] id; basePlaceholders every call;
// runPlaceholders add the run conch asks about.
var (
	remoteParts      = map[string]bool{"host": true, "owner": true, "repo": true, "path": true, "path_escaped": true}
	basePlaceholders = map[string]bool{"project": true, "branch": true, "head": true,
		"host": true, "owner": true, "repo": true, "path": true, "path_escaped": true}
	runPlaceholders = map[string]bool{"run": true}
)

func (f *File) checkRuns(what string, op RunsOp, add func(string, ...any)) {
	if !op.Has() {
		return
	}
	names := f.checkSteps(what, op.Operation, nil, add)
	if op.Items == "" || op.ID == "" || op.State == "" {
		add("[%s] items, id and state: where the runs are, and each one's id and state", what)
	}
	names["item"] = true
	for _, field := range []struct{ key, v string }{{"id", op.ID}, {"name", op.Name}, {"state", op.State},
		{"commit", op.Commit}, {"url", op.URL}, {"started", op.Started}, {"finished", op.Finished}} {
		if strings.Contains(field.v, "{") {
			checkTemplate(what, field.key, field.v, names, add)
		}
	}
}

// checkSteps checks an operation's steps and returns the names a
// template after them may use.
func (f *File) checkSteps(what string, op Operation, extra map[string]bool, add func(string, ...any)) map[string]bool {
	known := map[string]bool{}
	for k := range basePlaceholders {
		known[k] = true
	}
	for k := range extra {
		known[k] = true
	}
	if len(op.Steps) > 0 && (op.Call != "" || op.Follow != "" || op.Text != "" || op.Body != nil) {
		add("[%s]: a call or steps, not both", what)
	}
	steps := op.steps()
	if len(steps) == 0 {
		add("[%s]: no call", what)
	}
	for i, s := range steps {
		at := fmt.Sprintf("[%s] step %d", what, i+1)
		if len(op.Steps) == 0 {
			at = fmt.Sprintf("[%s]", what)
		}
		if err := checkCall(s.Call); err != nil {
			add("%s call: %v", at, err)
		}
		if s.Each != "" && !known[s.Each] {
			add("%s each %q: no earlier step is called that", at, s.Each)
		}
		if s.Follow != "" && len(f.FollowHosts) == 0 {
			add("%s follow: no follow_hosts it may reach", at)
		}
		if s.Text != "" && s.Pick != "" {
			add("%s: text reads the reply itself, so no pick", at)
		}
		if s.Follow != "" && s.Pick != "" {
			add("%s: follow reads the reply itself, so no pick", at)
		}
		if s.Text != "" && s.Text != "." && strings.ContainsAny(s.Text, "{}") {
			add("%s text %q: \".\" or a path", at, s.Text)
		}
		var strs []string
		walk(s.Body, func(v string) { strs = append(strs, v) })
		for _, ref := range placeholders(s.Call + " " + s.Pick + " " + strings.Join(strs, " ")) {
			root, _, _ := strings.Cut(ref, ".")
			ok := known[root] && root != s.As || root == "item" && s.Each != "" || root == "reply" && i > 0
			if !ok {
				add("%s: {%s} is nothing known before it", at, ref)
			}
		}
		if s.As != "" {
			if known[s.As] || s.As == "item" || s.As == "reply" || s.As == "key" || !stepName.MatchString(s.As) {
				add("%s as %q: a lowercase name of its own", at, s.As)
			}
			known[s.As] = true
		}
	}
	return known
}

var stepName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func checkTemplate(what, key, tmpl string, known map[string]bool, add func(string, ...any)) {
	for _, ref := range placeholders(tmpl) {
		if root, _, _ := strings.Cut(ref, "."); !known[root] {
			add("[%s] %s: {%s} is nothing known", what, key, ref)
		}
	}
}

// checkKey refuses {key} anywhere but the auth value: a key in a path or
// a body would be logged, shown, or sent where the file says.
func (f *File) checkKey(add func(string, ...any)) {
	var strs []string
	for _, op := range []Operation{f.Runs.Operation, f.FailedLog.Operation, f.Rerun, f.Stats.Operation} {
		for _, s := range op.steps() {
			strs = append(strs, s.Call, s.Pick, s.Follow, s.Text)
			walk(s.Body, func(v string) { strs = append(strs, v) })
		}
	}
	strs = append(strs, f.Project.ID, f.FailedLog.Job, f.Runs.URL, f.Runs.Name, f.API)
	for _, s := range strs {
		if strings.Contains(s, "{key}") {
			add("{key} in %q: the key goes only in the auth value", s)
			return
		}
	}
}

func checkCall(c string) error {
	method, path, ok := strings.Cut(strings.TrimSpace(c), " ")
	switch {
	case c == "":
		return fmt.Errorf(`missing: "METHOD /path"`)
	case !ok || method != "GET" && method != "POST" && method != "PUT" && method != "PATCH":
		return fmt.Errorf("%q: METHOD /path, the method one of GET, POST, PUT, PATCH", c)
	case !strings.HasPrefix(path, "/") || strings.Contains(path, "://"):
		return fmt.Errorf("%q: a path under api, starting with /", c)
	}
	return nil
}

var placeholderRE = regexp.MustCompile(`\{([a-z_][a-z0-9_]*(?:\.[^{}]*)?)\}`)

func placeholders(s string) []string {
	var out []string
	for _, m := range placeholderRE.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

func walk(v any, fn func(string)) {
	switch x := v.(type) {
	case string:
		fn(x)
	case map[string]any:
		for _, e := range x {
			walk(e, fn)
		}
	case []any:
		for _, e := range x {
			walk(e, fn)
		}
	case []map[string]any:
		for _, e := range x {
			walk(e, fn)
		}
	}
}

func slicesHas(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
