package integration

import (
	"fmt"
	"regexp"
	"slices"
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
	KindDeploy  = "deploy"
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

	Detect Detect `toml:"detect"`
	// Source is kind import's: where the app keeps what is brought in.
	Source  ImportSource      `toml:"source"`
	Project Project           `toml:"project"`
	States  map[string]string `toml:"states"`

	// The ci kind's operations.
	Runs      RunsOp    `toml:"runs"`
	FailedLog LogOp     `toml:"failed_log"`
	Rerun     Operation `toml:"rerun"`
	Stats     RunsOp    `toml:"stats"`

	// The tickets kind's operations.
	List    TicketsOp `toml:"list"`
	Mine    TicketsOp `toml:"mine"`
	Get     TicketsOp `toml:"get"`
	Comment Operation `toml:"comment"`
	Link    Operation `toml:"link"`
	Move    Operation `toml:"move"`

	// The notify kind's operation.
	Send Operation `toml:"send"`

	// Hook is kind hook's: an action run on events (hook.go).
	Hook HookSpec `toml:"hook"`

	// The deploy kind's operations; its failed_log is FailedLog, with
	// {deploy} for {run}.
	Previews DeployOp  `toml:"previews"`
	Redeploy Operation `toml:"redeploy"`
	Promote  Operation `toml:"promote"`

	// shipped is set for a file conch carries rather than one installed.
	shipped bool
}

// Auth is how the key is sent. Type "header" (the default) sends Header:
// Value, Value holding {key}; "bearer" sends Authorization: Bearer KEY,
// with the Scopes the person should give the token; "basic" sends the
// user from UserEnv and the key as a password; "url" is a webhook's: the
// key is the whole address calls go to, which must be on api's host.
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
	Call string `toml:"call"`
	// Body is a table sent as JSON, or a string sent as plain text.
	Body    any               `toml:"body"`
	Headers map[string]string `toml:"headers"`
	Follow  string            `toml:"follow"`
	Text    string            `toml:"text"`
	Steps   []Step            `toml:"steps"`
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

// DeployOp lists a branch's deploys: a run's fields and three more —
// where it went (production, preview…), why it failed, and the service's
// own page for it.
type DeployOp struct {
	RunsOp
	Environment string `toml:"environment"`
	Error       string `toml:"error"`
	Inspect     string `toml:"inspect"`
}

// LogOp is a failed run's output, {run} its id; Job names what failed,
// as a template over the steps.
type LogOp struct {
	Operation
	Job string `toml:"job"`
}

// TicketsOp reads tickets: for list and mine, Items is where the list is
// in the last step's value; for get, where the ticket is ("" for all of
// it). Each field is a path into a ticket or, holding a {, a template
// over {item.x} and the steps; Description is the ticket's text. Comments (get only) is a path into the
// ticket, or {NAME.path} for an earlier step's value; CommentAuthor and
// CommentBody are paths into each comment. StatusNames, {NAME.path},
// is a list of {id, name} an earlier step read, for a service whose
// ticket gives its status as an id: the status is the name of that id.
type TicketsOp struct {
	Operation
	Items         string `toml:"items"`
	Key           string `toml:"key"`
	Title         string `toml:"title"`
	Description   string `toml:"description"`
	URL           string `toml:"url"`
	Status        string `toml:"status"`
	StatusNames   string `toml:"status_names"`
	Comments      string `toml:"comments"`
	CommentAuthor string `toml:"comment_author"`
	CommentBody   string `toml:"comment_body"`
}

// Step is one call, or one per item of an earlier step's list.
type Step struct {
	// As names its value for the steps after it and the templates:
	// {as.x}. {reply.x} is the step just before, named or not.
	As      string            `toml:"as"`
	Call    string            `toml:"call"`
	Body    any               `toml:"body"`
	Headers map[string]string `toml:"headers"`
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
	return []Step{{Call: op.Call, Body: op.Body, Headers: op.Headers, Follow: op.Follow, Text: op.Text}}
}

// Has says whether the file fills the operation.
func (op Operation) Has() bool { return op.Call != "" || len(op.Steps) > 0 }

// contract is what a kind's file must and may fill, and the placeholders
// each operation has beyond every call's.
type contract struct {
	required, optional []string
	states             []string
	vars               map[string][]string
}

var contracts = map[string]contract{
	KindCI: {required: []string{"runs"}, optional: []string{"failed_log", "rerun", "stats"},
		states: []string{"pass", "fail", "pending", "canceled"},
		vars:   map[string][]string{"failed_log": {"run"}, "rerun": {"run"}}},
	KindTickets: {required: []string{"list", "get"}, optional: []string{"mine", "comment", "link", "move"},
		vars: map[string][]string{"list": {"scope", "limit"}, "mine": {"scope", "limit"}, "get": {"ticket"},
			"comment": {"ticket", "body"}, "link": {"ticket", "url", "title"}, "move": {"ticket", "status"}}},
	KindNotify: {required: []string{"send"},
		vars: map[string][]string{"send": {"title", "body", "url"}}},
	KindDeploy: {required: []string{"previews"}, optional: []string{"failed_log", "redeploy", "promote"},
		states: []string{"ready", "building", "failed", "canceled"},
		vars:   map[string][]string{"failed_log": {"deploy"}, "redeploy": {"deploy"}, "promote": {"deploy"}}},
}

// verdicts are the states a kind's file must map something to: without
// them nothing it reads is ever finished.
var verdicts = map[string][2]string{KindCI: {"pass", "fail"}, KindDeploy: {"ready", "failed"}}

// namedOp is an operation and the name a file gives it.
type namedOp struct {
	name string
	op   Operation
}

// ops is every operation a file may have, in the contracts' order.
func (f *File) ops() []namedOp {
	return []namedOp{{"runs", f.Runs.Operation}, {"failed_log", f.FailedLog.Operation}, {"rerun", f.Rerun}, {"stats", f.Stats.Operation},
		{"list", f.List.Operation}, {"mine", f.Mine.Operation}, {"get", f.Get.Operation},
		{"comment", f.Comment}, {"link", f.Link}, {"move", f.Move}, {"send", f.Send},
		{"previews", f.Previews.Operation}, {"redeploy", f.Redeploy}, {"promote", f.Promote}}
}

// opPurpose says what each operation is for, as an agent adapting a file
// is told it.
var opPurpose = map[string]string{
	"runs":       "a branch's runs, newest first: {branch}, {head} and {project} are known",
	"failed_log": "what a failed run printed, {run} its id: the text a branch's agent is sent",
	"rerun":      "re-run what failed in run {run}",
	"stats":      "recent runs with their durations, for the CI panel",
	"list":       "the open tickets, {scope} the team or project and {limit} how many: the Tickets view",
	"mine":       "as list, the open tickets assigned to the key's person",
	"get":        "one ticket, {ticket}, with its comments: a task's prompt",
	"comment":    "add {body} as a comment on ticket {ticket}",
	"link":       "attach {url} (titled {title}) to ticket {ticket}: a branch or pull request",
	"move":       "move ticket {ticket} to {status}, as the person typed it",
	"send":       "send {title}, {body} and {url}: an agent waiting or done, a pull request merged",
	"previews":   "a branch's deploys, newest first, at {head} when there is one: {branch}, {head} and the remote's parts are known; each one's url is its preview's address",
	"redeploy":   "build deploy {deploy} again",
	"promote":    "release deploy {deploy} to production; conch asks the person first",
}

// purpose is what op is for in a file of kind: failed_log is a run's in
// ci and a deploy's here.
func purpose(kind, op string) string {
	if kind == KindDeploy && op == "failed_log" {
		return "what failed deploy {deploy} printed while it was built: the text a branch's agent is sent"
	}
	return opPurpose[op]
}

// KindContract is what a kind's file fills, for whoever writes one.
type KindContract struct {
	Required []string
	Optional []string
	States   []string
	// Text is the contract in words.
	Text string
}

// Contract is kind's contract; false for a kind this conch reads no file
// of (a sandbox file is the sandbox package's).
func Contract(kind string) (KindContract, bool) {
	c, ok := contracts[kind]
	if !ok {
		return KindContract{}, false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "A %s file fills these operations:\n", kind)
	for _, op := range c.required {
		fmt.Fprintf(&b, "- [%s] (required): %s\n", op, purpose(kind, op))
	}
	for _, op := range c.optional {
		fmt.Fprintf(&b, "- [%s]: %s\n", op, purpose(kind, op))
	}
	fmt.Fprintf(&b, "Every state the service documents maps, in [states], to one of %s.\n", strings.Join(c.states, ", "))
	return KindContract{Required: slices.Clone(c.required), Optional: slices.Clone(c.optional), States: slices.Clone(c.states), Text: b.String()}, true
}

// Operations lists the operations the file fills, in the contract's order.
func (f *File) Operations() []string {
	var out []string
	for _, o := range f.ops() {
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
	if f.Kind == KindHook {
		if !nameRE.MatchString(f.Name) {
			add("name %q: lowercase letters, digits and -, starting with a letter", f.Name)
		}
		f.hookProblems(add)
		sort.Strings(p)
		return p
	}
	if f.Kind == KindImport {
		if !nameRE.MatchString(f.Name) {
			add("name %q: lowercase letters, digits and -, starting with a letter", f.Name)
		}
		f.importProblems(add)
		sort.Strings(p)
		return p
	}
	if f.Hook.Run != "" || len(f.Hook.On) > 0 || f.Hook.Timeout != 0 || len(f.Hook.Variables) > 0 {
		add("[hook]: only a hook file has one")
	}
	if f.Source.Reader != "" || f.Source.Path != "" || f.Source.Store != "" || len(f.Source.Platforms) > 0 || f.Source.MinReader != 0 {
		add("[source]: only an import file has one")
	}
	c, known := contracts[f.Kind]
	switch {
	case known:
	case f.Kind == KindSandbox:
		add("kind %q: a sandbox provider is read by conch sandbox, from providers/", f.Kind)
	case f.Kind == KindHost:
		add("kind %q: this conch does not read it yet", f.Kind)
	default:
		add("kind %q: one of ci, tickets, notify, deploy, import, hook, host, sandbox", f.Kind)
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
	// A CI service is found from the checkout; a tracker or a webhook may
	// have nothing there to find it by, and is tied to a project by
	// config.toml or used everywhere.
	if (f.Kind == KindCI || f.Kind == KindDeploy) && len(f.Detect.Files)+len(f.Detect.Remotes)+len(f.Detect.Checks) == 0 {
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
	if f.Project.ID == "" && f.Kind == KindCI {
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
	if len(c.states) == 0 && len(f.States) > 0 {
		add("[states]: kind %s has none; a ticket's status is shown as the service words it", f.Kind)
	}
	if len(c.states) > 0 {
		mapped := map[string]bool{}
		for theirs, s := range f.States {
			if !ours[s] {
				add("[states] %s = %q: one of %s", theirs, s, strings.Join(c.states, ", "))
			}
			mapped[s] = true
		}
		if v := verdicts[f.Kind]; !mapped[v[0]] || !mapped[v[1]] {
			add("[states] maps nothing to %s or to %s", v[0], v[1])
		}
	}
	has := f.Operations()
	for _, r := range c.required {
		if !slicesHas(has, r) {
			add("[%s]: required for kind %s", r, f.Kind)
		}
	}
	for _, o := range has {
		if !slicesHas(c.required, o) && !slicesHas(c.optional, o) {
			add("[%s]: not an operation of kind %s", o, f.Kind)
		}
	}
	switch f.Kind {
	case KindCI:
		f.checkRuns("runs", f.Runs, add)
		if f.Stats.Has() {
			f.checkRuns("stats", f.Stats, add)
		}
		f.checkLog(add)
		if f.Rerun.Has() {
			f.checkSteps("rerun", f.Rerun, add)
		}
	case KindTickets:
		f.checkTickets("list", f.List, add)
		f.checkTickets("mine", f.Mine, add)
		f.checkTickets("get", f.Get, add)
		for _, o := range []namedOp{{"comment", f.Comment}, {"link", f.Link}, {"move", f.Move}} {
			if o.op.Has() {
				f.checkSteps(o.name, o.op, add)
			}
		}
	case KindNotify:
		if f.Send.Has() {
			f.checkSteps("send", f.Send, add)
		}
	case KindDeploy:
		if f.Previews.Has() {
			names := f.checkRuns("previews", f.Previews.RunsOp, add)
			for _, field := range []struct{ key, v string }{{"environment", f.Previews.Environment},
				{"error", f.Previews.Error}, {"inspect", f.Previews.Inspect}} {
				if strings.Contains(field.v, "{") {
					checkTemplate("previews", field.key, field.v, names, add)
				}
			}
		}
		f.checkLog(add)
		for _, o := range []namedOp{{"redeploy", f.Redeploy}, {"promote", f.Promote}} {
			if o.op.Has() {
				f.checkSteps(o.name, o.op, add)
			}
		}
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
	case "bearer", "basic", "url":
		if a.Header != "" || a.Value != "" {
			add("[auth] %s sends the key itself: no header or value", a.Type)
		}
	default:
		add("[auth] type %q: header, bearer, basic or url", a.Type)
	}
	if a.Type == "url" && f.Kind != KindNotify {
		add("[auth] url is for kind notify, whose webhook's address is its key")
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

// urlAuth says whether the key is the address calls go to.
func (f *File) urlAuth() bool { return f.Auth.Type == "url" }

// remoteParts fill [project] id; basePlaceholders every call but a
// notification's, which is about no project.
var (
	remoteParts      = map[string]bool{"host": true, "owner": true, "repo": true, "path": true, "path_escaped": true}
	basePlaceholders = map[string]bool{"project": true, "branch": true, "head": true,
		"host": true, "owner": true, "repo": true, "path": true, "path_escaped": true}
)

// known is what a template in operation what may use before its steps.
func (f *File) known(what string) map[string]bool {
	known := map[string]bool{}
	if f.Kind != KindNotify {
		for k := range basePlaceholders {
			known[k] = true
		}
		// {project} is what [project] makes: a file without one has none.
		if f.Project.ID == "" {
			delete(known, "project")
		}
	}
	for _, k := range contracts[f.Kind].vars[what] {
		known[k] = true
	}
	return known
}

func (f *File) checkLog(add func(string, ...any)) {
	if !f.FailedLog.Has() {
		return
	}
	names := f.checkSteps("failed_log", f.FailedLog.Operation, add)
	if steps := f.FailedLog.steps(); len(steps) > 0 && steps[len(steps)-1].Text == "" {
		add("[failed_log] the last step is the log: give it text")
	}
	checkTemplate("failed_log", "job", f.FailedLog.Job, names, add)
}

// checkRuns checks a list of runs or deploys and returns the names its
// fields may use.
func (f *File) checkRuns(what string, op RunsOp, add func(string, ...any)) map[string]bool {
	if !op.Has() {
		return nil
	}
	names := f.checkSteps(what, op.Operation, add)
	if op.Items == "" || op.ID == "" || op.State == "" {
		thing := "runs"
		if f.Kind == KindDeploy {
			thing = "deploys"
		}
		add("[%s] items, id and state: where the %s are, and each one's id and state", what, thing)
	}
	names["item"] = true
	for _, field := range []struct{ key, v string }{{"id", op.ID}, {"name", op.Name}, {"state", op.State},
		{"commit", op.Commit}, {"url", op.URL}, {"started", op.Started}, {"finished", op.Finished}} {
		if strings.Contains(field.v, "{") {
			checkTemplate(what, field.key, field.v, names, add)
		}
	}
	return names
}

func (f *File) checkTickets(what string, op TicketsOp, add func(string, ...any)) {
	if !op.Has() {
		return
	}
	names := f.checkSteps(what, op.Operation, add)
	if what != "get" && op.Items == "" {
		add("[%s] items: where the tickets are", what)
	}
	if op.Key == "" || op.Title == "" {
		add("[%s] key and title: each ticket's key, as conch is given it back, and its title", what)
	}
	if what != "get" && (op.Comments != "" || op.CommentAuthor != "" || op.CommentBody != "") {
		add("[%s] comments: a ticket's comments are read by get", what)
	}
	if op.StatusNames != "" && !refOnly(op.StatusNames) {
		add("[%s] status_names %q: an earlier step's value, {NAME} or {NAME.path}", what, op.StatusNames)
	}
	if op.Comments != "" && op.CommentBody == "" {
		add("[%s] comment_body: where each comment's text is", what)
	}
	names["item"] = true
	for _, field := range []struct{ key, v string }{{"key", op.Key}, {"title", op.Title}, {"description", op.Description},
		{"url", op.URL}, {"status", op.Status}, {"comments", op.Comments}, {"status_names", op.StatusNames}} {
		if strings.Contains(field.v, "{") {
			checkTemplate(what, field.key, field.v, names, add)
		}
	}
}

// checkSteps checks an operation's steps and returns the names a
// template after them may use.
func (f *File) checkSteps(what string, op Operation, add func(string, ...any)) map[string]bool {
	known := f.known(what)
	if len(op.Steps) > 0 && (op.Call != "" || op.Follow != "" || op.Text != "" || op.Body != nil || op.Headers != nil) {
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
		if err := checkCall(s.Call, f.urlAuth()); err != nil {
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
		switch s.Body.(type) {
		case nil, string, map[string]any:
		default:
			add("%s body: a table, sent as JSON, or a string, sent as text", at)
		}
		for name := range s.Headers {
			if !headerRE.MatchString(name) || reservedHeader(name, f) {
				add("%s headers %q: a header of the service's own (not Authorization, Cookie, Host, Content-Type or the key's)", at, name)
			}
		}
		var strs []string
		walk(s.Body, func(v string) { strs = append(strs, v) })
		for _, v := range s.Headers {
			strs = append(strs, v)
		}
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

var (
	stepName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	headerRE = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
)

// reservedHeader is a header conch sets itself, or that carries a
// credential: a file may not set it.
func reservedHeader(name string, f *File) bool {
	switch strings.ToLower(name) {
	case "authorization", "cookie", "host", "content-type", "content-length", "user-agent", "proxy-authorization":
		return true
	}
	h := firstSet(f.Auth.Header, f.AuthHeader)
	return h != "" && strings.EqualFold(h, name)
}

func checkTemplate(what, key, tmpl string, known map[string]bool, add func(string, ...any)) {
	for _, ref := range placeholders(tmpl) {
		if root, _, _ := strings.Cut(ref, "."); !known[root] {
			add("[%s] %s: {%s} is nothing known", what, key, ref)
		}
	}
}

// checkKey refuses {key} anywhere but the auth value: a key in a path, a
// body or a header would be logged, shown, or sent where the file says.
func (f *File) checkKey(add func(string, ...any)) {
	var strs []string
	for _, o := range f.ops() {
		for _, s := range o.op.steps() {
			strs = append(strs, s.Call, s.Pick, s.Follow, s.Text)
			walk(s.Body, func(v string) { strs = append(strs, v) })
			for k, v := range s.Headers {
				strs = append(strs, k, v)
			}
		}
	}
	for _, t := range []TicketsOp{f.List, f.Mine, f.Get} {
		strs = append(strs, t.Key, t.Title, t.Description, t.URL, t.Status, t.Comments, t.StatusNames)
	}
	strs = append(strs, f.Project.ID, f.FailedLog.Job, f.Runs.URL, f.Runs.Name, f.API,
		f.Previews.URL, f.Previews.Name, f.Previews.Inspect, f.Previews.Environment, f.Previews.Error)
	for _, s := range strs {
		if strings.Contains(s, "{key}") {
			add("{key} in %q: the key goes only in the auth value", s)
			return
		}
	}
}

// refOnly says whether s is one placeholder and nothing else.
func refOnly(s string) bool {
	m := placeholderRE.FindString(s)
	return m != "" && m == s
}

// checkCall checks "METHOD /path". A file whose key is the address — a
// webhook's — gives the method alone, or a suffix to the address.
func checkCall(c string, urlAuth bool) error {
	method, path, ok := strings.Cut(strings.TrimSpace(c), " ")
	path = strings.TrimSpace(path)
	// A query may hold a URL (Vercel finds a project by its repository's);
	// only the path must not be one.
	bare, _, _ := strings.Cut(path, "?")
	switch {
	case c == "":
		return fmt.Errorf(`missing: "METHOD /path"`)
	case method != "GET" && method != "POST" && method != "PUT" && method != "PATCH":
		return fmt.Errorf("%q: METHOD /path, the method one of GET, POST, PUT, PATCH", c)
	case strings.Contains(bare, "://"):
		return fmt.Errorf("%q: a path under api, starting with /", c)
	case urlAuth && path != "" && !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "?"):
		return fmt.Errorf("%q: the method alone, or a /path or ?query added to the webhook's address", c)
	case !urlAuth && (!ok || !strings.HasPrefix(path, "/")):
		return fmt.Errorf("%q: METHOD /path, a path under api, starting with /", c)
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
