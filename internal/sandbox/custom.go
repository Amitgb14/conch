package sandbox

import (
	_ "embed"

	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/Amitgb14/conch/internal/config"
)

// A provider conch does not know is described in a file rather than
// written as code: $CONCH_HOME/providers/NAME.toml says which calls make,
// look at, list, start, stop and delete a sandbox, how its states read,
// and how ssh gets in. An agent can draft one from the provider's API docs
// (conch sandbox adapt), and the person reads it before it is used.
//
// It is a description on purpose. A generated adapter would be code run
// with the person's API key, written by a model from a web page; a file of
// calls and mappings does nothing this one provider does not already do,
// and a wrong one fails a check rather than doing something else.

// ProviderFileGuide is the format, as the agent drafting a file is told it.
//
//go:embed providerfile.md
var ProviderFileGuide string

// ProviderFormat is the newest provider file this conch reads. A file
// that needs what format 1 lacked says format = 2, which a conch from
// before formats calls an unknown key and refuses: refusing is right,
// since it would otherwise send {public_key} as those very letters.
const ProviderFormat = 2

// PublicKey is the public key a sandbox is made with when a provider file
// puts {public_key} in its create body. internal/remote sets it (the
// person's own key, else conch's), since that is where keys are found.
var PublicKey func(ctx context.Context) (string, error)

// ProviderFile is the shape of a provider file.
type ProviderFile struct {
	// Format is 1 when left out.
	Format int    `toml:"format"`
	Name   string `toml:"name"`
	Label  string `toml:"label"`
	// API is the base every call's path is put after.
	API    string `toml:"api"`
	KeyEnv string `toml:"key_env"`
	// AuthHeader and AuthValue send the key: "Authorization" and
	// "Bearer {key}" when left out.
	AuthHeader string `toml:"auth_header"`
	AuthValue  string `toml:"auth_value"`
	// Docs is where it was adapted from; AdaptedBy and Adapted who wrote
	// it and when. Only shown.
	Docs      string `toml:"docs"`
	AdaptedBy string `toml:"adapted_by"`
	Adapted   string `toml:"adapted"`
	// Note is said before a sandbox is made with it.
	Note string `toml:"note"`

	Create CreateCall        `toml:"create"`
	Get    GetCall           `toml:"get"`
	List   ListCall          `toml:"list"`
	Start  Call              `toml:"start"`
	Stop   Call              `toml:"stop"`
	Delete Call              `toml:"delete"`
	States map[string]string `toml:"states"`
	SSH    SSHWay            `toml:"ssh"`
}

// Call is one request: "METHOD /path", the path's {id} filled in, and a
// body whose string values may be placeholders.
type Call struct {
	Call string         `toml:"call"`
	Body map[string]any `toml:"body"`
}

// CreateCall makes a sandbox; ID is where its id is in the reply.
type CreateCall struct {
	Call
	ID string `toml:"id"`
}

// GetCall looks at one; each field is a path into the reply — into Item,
// where the reply wraps the sandbox ({"server": {...}}). A list's items
// are read with the same paths, unwrapped.
type GetCall struct {
	Call
	Item     string `toml:"item"`
	State    string `toml:"state"`
	Name     string `toml:"name"`
	Reason   string `toml:"reason"`
	CPU      string `toml:"cpu"`
	MemoryMB string `toml:"memory_mb"`
	DiskMB   string `toml:"disk_mb"`
	Created  string `toml:"created"`
}

// ListCall lists the account's sandboxes. Items is where the list is in
// the reply, and each item is read as Get reads its reply, with ID where
// the item's id is. Next is where the next page's cursor is, sent back as
// CursorParam. Mine is the start of the name conch gives every sandbox it
// makes, which is how its own are told from the account's others.
type ListCall struct {
	Call
	Items       string `toml:"items"`
	ID          string `toml:"id"`
	Next        string `toml:"next"`
	CursorParam string `toml:"cursor_param"`
	Mine        string `toml:"mine"`
}

// SSHWay is how ssh gets in. Key says whose key opens it: "account", a key
// registered with the person's account there; "authorize", one conch
// hands over through Authorize; "none", a credential in User from Access.
type SSHWay struct {
	Host string `toml:"host"`
	User string `toml:"user"`
	Port int    `toml:"port"`
	Key  string `toml:"key"`
	// Authorize lets a public key in ({public_key} in its body).
	Authorize Call `toml:"authorize"`
	// Access is asked before each connection when the way in is a
	// credential handed out per connection; Host and User may then read
	// its reply as {reply.PATH}.
	Access Call `toml:"access"`
}

// conch's own states a provider file may map to.
var customStates = map[string]State{
	"starting": StateStarting, "started": StateStarted, "stopping": StateStopping,
	"stopped": StateStopped, "error": StateError, "gone": StateDestroyed,
}

var providerName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

// CheckName says whether name can be a provider's. A name becomes a path —
// providers/NAME.toml, its draft's folder — and arrives from the registry's
// index, a command line and an agent's draft, so it is checked before any
// path is made of it: "../x" would otherwise write, or remove, a file
// outside providers/.
func CheckName(name string) error {
	if !providerName.MatchString(name) {
		return fmt.Errorf("%q is not a provider name: lowercase letters, digits and -, starting with a letter, 2 to 31 long", name)
	}
	return nil
}

// ProvidersDir is where provider files are kept.
func ProvidersDir() string { return filepath.Join(config.Dir(), "providers") }

// ProviderFilePath is where the file for name is kept.
func ProviderFilePath(name string) string { return filepath.Join(ProvidersDir(), name+".toml") }

// ReadProviderFile reads and checks a provider file, returning what is
// wrong with it — everything, not the first thing, since the person or the
// agent fixing it wants the list.
func ReadProviderFile(path string) (*ProviderFile, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(data) > 256<<10 {
		return nil, nil, fmt.Errorf("%s is over 256 KB: not a provider file", path)
	}
	var f ProviderFile
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	var problems []string
	for _, k := range md.Undecoded() {
		// A body is free-form: what is inside it is the provider's.
		if slicesContains(k, "body") {
			continue
		}
		problems = append(problems, fmt.Sprintf("unknown key %s", k))
	}
	problems = append(problems, f.Problems()...)
	return &f, problems, nil
}

// Problems lists what is wrong with the file, or nothing.
func (f *ProviderFile) Problems() []string {
	var p []string
	add := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }
	if f.Format < 0 || f.Format > ProviderFormat {
		add("format %d: this conch reads up to format %d; update conch to use it", f.Format, ProviderFormat)
	} else if need := f.needs2(); need != "" && f.Format < 2 {
		add("%s needs format = 2", need)
	}
	switch {
	case !providerName.MatchString(f.Name):
		add("name %q: lowercase letters, digits and -, starting with a letter", f.Name)
	case slicesContains(builtin, f.Name):
		add("name %q is a provider conch has built in", f.Name)
	}
	if u, err := url.Parse(f.API); err != nil || u.Scheme != "https" || u.Host == "" {
		if !(err == nil && u.Scheme == "http" && isLoopback(u.Hostname())) {
			add("api %q: an https:// URL", f.API)
		}
	}
	if f.KeyEnv == "" || !regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(f.KeyEnv) {
		add("key_env %q: the name of an environment variable, e.g. ACME_API_KEY", f.KeyEnv)
	}
	if f.AuthValue != "" && !strings.Contains(f.AuthValue, "{key}") {
		add("auth_value %q never sends {key}", f.AuthValue)
	}
	for _, c := range []struct {
		what string
		call Call
	}{{"create", f.Create.Call}, {"get", f.Get.Call}, {"list", f.List.Call}, {"start", f.Start}, {"stop", f.Stop}, {"delete", f.Delete}} {
		if err := checkCall(c.call.Call); err != nil {
			add("[%s] call: %v", c.what, err)
			continue
		}
		if c.what != "create" && c.what != "list" && !strings.Contains(c.call.Call, "{id}") {
			add("[%s] call %q names no {id}", c.what, c.call.Call)
		}
		if err := checkBody(c.call.Body); err != nil {
			add("[%s] body: %v", c.what, err)
		}
	}
	if f.Create.ID == "" {
		add("[create] id: where the new sandbox's id is in the reply")
	}
	if f.Get.State == "" {
		add("[get] state: where the sandbox's state is in the reply")
	}
	if f.List.Items == "" || f.List.ID == "" {
		add("[list] items and id: where the list is, and each one's id")
	}
	if f.List.Mine == "" || f.Get.Name == "" {
		add("[list] mine and [get] name: conch names what it makes, and finds its own by that name")
	} else if !bodyUses(f.Create.Body, "{name}") {
		add("[create] body never sends {name}, so List could not find conch's own")
	}
	if (f.List.Next == "") != (f.List.CursorParam == "") {
		add("[list] next and cursor_param go together")
	}
	started := false
	for theirs, ours := range f.States {
		if _, ok := customStates[ours]; !ok {
			add("[states] %s = %q: one of starting, started, stopping, stopped, error, gone", theirs, ours)
		}
		started = started || ours == "started"
	}
	if !started {
		add("[states] maps nothing to started")
	}
	if f.SSH.Host == "" || f.SSH.User == "" {
		add("[ssh] host and user: conch reaches every sandbox over ssh, so a provider with no ssh cannot be used")
	}
	if f.SSH.Port < 0 || f.SSH.Port > 65535 {
		add("[ssh] port %d", f.SSH.Port)
	}
	switch f.SSH.Key {
	case "account", "none":
	case "authorize":
		if err := checkCall(f.SSH.Authorize.Call); err != nil {
			add("[ssh.authorize] call: %v", err)
		} else if !bodyUses(f.SSH.Authorize.Body, "{public_key}") {
			add("[ssh.authorize] body never sends {public_key}")
		}
	default:
		add("[ssh] key %q: account, authorize or none", f.SSH.Key)
	}
	if f.SSH.Access.Call != "" {
		if err := checkCall(f.SSH.Access.Call); err != nil {
			add("[ssh.access] call: %v", err)
		}
	}
	sort.Strings(p)
	return p
}

// needs2 names the first thing in the file that format 1 did not have.
func (f *ProviderFile) needs2() string {
	if f.Get.Item != "" {
		return "[get] item"
	}
	if bodyUses(f.Create.Body, "{public_key}") {
		return "{public_key} in [create] body"
	}
	for _, path := range []string{f.Create.ID, f.Get.State, f.Get.Name, f.Get.Reason, f.Get.CPU, f.Get.MemoryMB,
		f.Get.DiskMB, f.Get.Created, f.List.Items, f.List.ID, f.List.Next, f.SSH.Host, f.SSH.User} {
		if strings.Contains(path, "[") {
			return fmt.Sprintf("the selector in %q", path)
		}
	}
	return ""
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func isLoopback(host string) bool { return host == "127.0.0.1" || host == "localhost" || host == "::1" }

var methods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

func checkCall(c string) error {
	method, path, ok := strings.Cut(strings.TrimSpace(c), " ")
	switch {
	case c == "":
		return errors.New(`missing: "METHOD /path"`)
	case !ok || !methods[method]:
		return fmt.Errorf("%q: METHOD /path, the method one of GET, POST, PUT, PATCH, DELETE", c)
	case !strings.HasPrefix(path, "/") || strings.Contains(path, "://"):
		return fmt.Errorf("%q: a path under api, starting with /", c)
	}
	return nil
}

// placeholders a body may use.
var bodyPlaceholders = map[string]bool{
	"{name}": true, "{label}": true, "{cpu}": true, "{memory_gib}": true, "{memory_mb}": true,
	"{disk_gib}": true, "{disk_mb}": true, "{snapshot}": true, "{env}": true, "{auto_stop_secs}": true,
	"{public_key}": true, "{id}": true,
}

var placeholder = regexp.MustCompile(`\{[a-z_.]+\}`)

func checkBody(body map[string]any) error {
	var bad []string
	walk(body, func(s string) {
		for _, p := range placeholder.FindAllString(s, -1) {
			if !bodyPlaceholders[p] {
				bad = append(bad, p)
			}
		}
	})
	if len(bad) > 0 {
		return fmt.Errorf("unknown placeholder %s", strings.Join(bad, ", "))
	}
	return nil
}

func bodyUses(body map[string]any, p string) bool {
	found := false
	walk(body, func(s string) { found = found || strings.Contains(s, p) })
	return found
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

// Mapping is one line of what conch will do, for a person to check.
type Mapping struct {
	Does  string `json:"does"`
	Call  string `json:"call"`
	Reads string `json:"reads,omitempty"`
}

// Describe says in plain lines which call conch makes for what, and what
// it reads back: the review a person does before saving the file.
func (f *ProviderFile) Describe() []Mapping {
	body := func(c Call) string {
		if len(c.Body) == 0 {
			return c.Call
		}
		b, _ := json.Marshal(c.Body)
		return c.Call + " " + string(b)
	}
	m := []Mapping{
		{"New sandbox", body(f.Create.Call), f.Create.ID},
		{"Look at one", body(f.Get.Call), joinSet(f.Get.State, f.Get.Name, f.Get.CPU, f.Get.MemoryMB, f.Get.DiskMB)},
		{"List conch's", body(f.List.Call), fmt.Sprintf("%s, %s starting %q", f.List.Items, f.Get.Name, f.List.Mine)},
		{"Start", body(f.Start), "waits for started"},
		{"Stop", body(f.Stop), "waits for stopped"},
		{"Delete", body(f.Delete), ""},
	}
	var states []string
	for k, v := range f.States {
		states = append(states, k+"→"+v)
	}
	sort.Strings(states)
	m = append(m, Mapping{"States", strings.Join(states, " "), ""})
	way := fmt.Sprintf("ssh %s@%s", f.SSH.User, f.SSH.Host)
	if f.SSH.Port != 0 && f.SSH.Port != 22 {
		way += fmt.Sprintf(" -p %d", f.SSH.Port)
	}
	switch f.SSH.Key {
	case "account":
		way += ", with your key registered with the account"
	case "authorize":
		way += ", with your key, handed over by " + body(f.SSH.Authorize)
	case "none":
		way += ", with what " + firstSet(f.SSH.Access.Call, "the user name") + " gives"
	}
	return append(m, Mapping{"Way in", way, ""})
}

func joinSet(vs ...string) string {
	var out []string
	for _, v := range vs {
		if v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, ", ")
}

// Custom is a provider described by a file.
type Custom struct {
	f   *ProviderFile
	key string
	hc  *http.Client
}

// NewCustom returns the provider f describes, with its key from cfg or
// the environment.
func NewCustom(f *ProviderFile, cfg config.ProviderCfg) *Custom {
	c := &Custom{f: f, hc: &http.Client{Timeout: time.Minute}}
	keyEnv := firstSet(cfg.APIKeyEnv, f.KeyEnv)
	c.key = strings.TrimSpace(cfg.APIKey)
	if c.key == "" {
		c.key = strings.TrimSpace(os.Getenv(keyEnv))
	}
	if cfg.APIURL != "" {
		f.API = cfg.APIURL
	}
	return c
}

// File is the description it was made from.
func (c *Custom) File() *ProviderFile { return c.f }

func (c *Custom) Name() string { return c.f.Name }

func (c *Custom) Check() error {
	if c.key == "" {
		return fmt.Errorf("%w: set $%s to a %s API key, or put the key in Settings → Sandboxes", ErrNotConfigured, c.f.KeyEnv, c.label())
	}
	return nil
}

func (c *Custom) label() string { return firstSet(c.f.Label, c.f.Name) }

// Note is the file's own, after saying where the provider came from.
func (c *Custom) Note() string {
	n := c.label() + " was adapted from its API docs"
	if c.f.AdaptedBy != "" {
		n += " by " + c.f.AdaptedBy
	}
	n += ", not built into conch."
	if c.f.SSH.Key == "account" {
		n += " It lets in the ssh keys registered with your account there: register yours once."
	}
	if c.f.Note != "" {
		n += " " + c.f.Note
	}
	return n
}

// AccountKey says ssh is opened by a key registered with the account.
func (c *Custom) AccountKey() bool { return c.f.SSH.Key == "account" }

// AuthorizesKey says whether conch hands its key over through the API.
func (c *Custom) AuthorizesKey() bool { return c.f.SSH.Key == "authorize" }

// AuthorizeKey hands a public key over, when the file says how.
func (c *Custom) AuthorizeKey(ctx context.Context, id, publicKey string) error {
	if !c.AuthorizesKey() {
		return nil
	}
	if id == "" {
		return ErrNotFound
	}
	if strings.TrimSpace(publicKey) == "" {
		return errors.New("no public key to authorize")
	}
	if _, err := c.do(ctx, c.f.SSH.Authorize, vars{"{id}": id, "{public_key}": strings.TrimSpace(publicKey)}); err != nil {
		return fmt.Errorf("authorize the ssh key: %w", err)
	}
	return nil
}

// vars fills placeholders. A body value that is a placeholder alone takes
// the value's own type, and is left out when the value is empty — the
// provider's default, as with every built-in provider.
type vars map[string]any

func (v vars) str(s string) string {
	return placeholder.ReplaceAllStringFunc(s, func(p string) string {
		x, ok := v[p]
		if !ok {
			return p
		}
		return fmt.Sprint(x)
	})
}

func empty(x any) bool {
	switch y := x.(type) {
	case nil:
		return true
	case string:
		return y == ""
	case int:
		return y == 0
	case map[string]string:
		return len(y) == 0
	}
	return false
}

func (v vars) fill(x any) (any, bool) {
	switch y := x.(type) {
	case string:
		if val, ok := v[y]; ok {
			return val, !empty(val)
		}
		if placeholder.MatchString(y) && placeholder.FindString(y) == y {
			return nil, false // a placeholder with nothing to put there
		}
		return v.str(y), true
	case map[string]any:
		out := map[string]any{}
		for k, e := range y {
			if f, ok := v.fill(e); ok {
				out[k] = f
			}
		}
		return out, len(out) > 0 || len(y) == 0
	case []any:
		var out []any
		for _, e := range y {
			if f, ok := v.fill(e); ok {
				out = append(out, f)
			}
		}
		return out, true
	}
	return x, true
}

func (c *Custom) do(ctx context.Context, call Call, v vars) (any, error) {
	if err := c.Check(); err != nil {
		return nil, err
	}
	method, path, _ := strings.Cut(strings.TrimSpace(call.Call), " ")
	if id, ok := v["{id}"].(string); ok {
		path = strings.ReplaceAll(path, "{id}", url.PathEscape(id))
	}
	var body any
	if call.Body != nil {
		b, _ := v.fill(call.Body)
		body = b
	}
	return c.request(ctx, method, path, body)
}

func (c *Custom) request(ctx context.Context, method, path string, body any) (any, error) {
	var rd io.Reader
	if body != nil {
		enc, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(enc)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.f.API, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set(firstSet(c.f.AuthHeader, "Authorization"), strings.ReplaceAll(firstSet(c.f.AuthValue, "Bearer {key}"), "{key}", c.key))
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBody {
		return nil, fmt.Errorf("%s: reply to %s %s is over %d bytes", c.f.Name, method, path, maxBody)
	}
	if resp.StatusCode == http.StatusNotFound && method != http.MethodPost {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := tensorlakeMessage(data)
		e := fmt.Sprintf("%s: %s %s answered %d", c.f.Name, method, path, resp.StatusCode)
		if msg != "" {
			e += " " + msg
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			e += fmt.Sprintf(" (check $%s, or the key in Settings → Sandboxes)", c.f.KeyEnv)
		}
		return nil, errors.New(e)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var out any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: %s %s answered with something other than JSON", c.f.Name, method, path)
	}
	return out, nil
}

// Pick reads a dotted path out of a JSON value: "a.b", "items.0.id".
func Pick(v any, path string) any {
	if path == "" {
		return nil
	}
	for _, part := range strings.Split(path, ".") {
		// field[key=value]: the first item of the list at field (or of
		// this list, when field is empty) whose key reads value.
		if i := strings.IndexByte(part, '['); i >= 0 && strings.HasSuffix(part, "]") {
			key, want, ok := strings.Cut(part[i+1:len(part)-1], "=")
			if field := part[:i]; field != "" {
				m, isMap := v.(map[string]any)
				if !isMap {
					return nil
				}
				v = m[field]
			}
			list, isList := v.([]any)
			if !ok || key == "" || !isList {
				return nil
			}
			v = nil
			for _, it := range list {
				if pickStr(it, key) == want {
					v = it
					break
				}
			}
			if v == nil {
				return nil
			}
			continue
		}
		switch x := v.(type) {
		case map[string]any:
			v = x[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

func pickStr(v any, path string) string {
	switch x := Pick(v, path).(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func pickNum(v any, path string) float64 {
	switch x := Pick(v, path).(type) {
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func pickTime(v any, path string) time.Time {
	switch x := Pick(v, path).(type) {
	case string:
		t, _ := time.Parse(time.RFC3339, x)
		return t
	case json.Number:
		n, err := x.Int64()
		if err != nil || n <= 0 {
			return time.Time{}
		}
		if n > 1e12 { // milliseconds
			return time.UnixMilli(n)
		}
		return time.Unix(n, 0)
	}
	return time.Time{}
}

// sandbox reads one sandbox out of a reply or a list item.
func (c *Custom) sandbox(v any, id string) Sandbox {
	raw := pickStr(v, c.f.Get.State)
	state, ok := customStates[c.f.States[raw]]
	if !ok {
		state = State(raw)
	}
	return Sandbox{
		ID: id, Name: firstSet(pickStr(v, c.f.Get.Name), id), State: state, Reason: pickStr(v, c.f.Get.Reason),
		CPU: int(pickNum(v, c.f.Get.CPU)), Memory: int(pickNum(v, c.f.Get.MemoryMB) / 1024),
		Disk: int(pickNum(v, c.f.Get.DiskMB) / 1024), Created: pickTime(v, c.f.Get.Created),
	}
}

// nameFor is a name of conch's: the file's prefix, the label as far as a
// name takes it, and a few random letters, as for Tensorlake.
func (c *Custom) nameFor(label string) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	slug := strings.Trim(tensorlakeNameChars.ReplaceAllString(strings.ToLower(label), "-"), "-")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
	}
	if slug == "" {
		return c.f.List.Mine + hex.EncodeToString(b)
	}
	return c.f.List.Mine + slug + "-" + hex.EncodeToString(b)
}

func (c *Custom) Create(ctx context.Context, spec Spec) (Sandbox, error) {
	if len(spec.Env) > 0 && !bodyUses(c.f.Create.Body, "{env}") {
		return Sandbox{}, fmt.Errorf("%s's file says no way to pass variables in: remove env from [sandbox.%s], or add {env} to [create] body", c.label(), c.f.Name)
	}
	name := c.nameFor(spec.Name)
	v := vars{"{name}": name, "{label}": spec.Name, "{cpu}": spec.CPU, "{memory_gib}": spec.Memory,
		"{memory_mb}": spec.Memory * 1024, "{disk_gib}": spec.Disk, "{disk_mb}": spec.Disk * 1024,
		"{snapshot}": spec.Snapshot, "{env}": spec.Env, "{auto_stop_secs}": spec.AutoStop * 60}
	if bodyUses(c.f.Create.Body, "{public_key}") {
		if PublicKey == nil {
			return Sandbox{}, fmt.Errorf("%s makes a sandbox with your public key, and conch has none to give", c.label())
		}
		key, err := PublicKey(ctx)
		if err != nil {
			return Sandbox{}, fmt.Errorf("the public key %s is made with: %w", c.label(), err)
		}
		v["{public_key}"] = strings.TrimSpace(key)
	}
	out, err := c.do(ctx, c.f.Create.Call, v)
	if err != nil {
		return Sandbox{}, fmt.Errorf("create sandbox: %w", err)
	}
	id := pickStr(out, c.f.Create.ID)
	if id == "" {
		return Sandbox{}, fmt.Errorf("create sandbox: %s answered with no id at %q", c.label(), c.f.Create.ID)
	}
	return c.wait(ctx, Sandbox{ID: id, Name: name, State: StateStarting}, StateStarted)
}

func (c *Custom) Get(ctx context.Context, id string) (Sandbox, error) {
	if id == "" {
		return Sandbox{}, ErrNotFound
	}
	out, err := c.do(ctx, c.f.Get.Call, vars{"{id}": id})
	if err != nil {
		return Sandbox{}, err
	}
	if c.f.Get.Item != "" {
		out = Pick(out, c.f.Get.Item)
	}
	if out == nil {
		return Sandbox{}, ErrNotFound
	}
	return c.sandbox(out, id), nil
}

func (c *Custom) List(ctx context.Context) ([]Sandbox, error) {
	var list []Sandbox
	cursor := ""
	for page := 0; ; page++ {
		if page == maxPages {
			return list, fmt.Errorf("list sandboxes: more than %d pages", maxPages)
		}
		call := c.f.List.Call
		if cursor != "" {
			sep := "?"
			if strings.Contains(call.Call, "?") {
				sep = "&"
			}
			call.Call += sep + url.QueryEscape(c.f.List.CursorParam) + "=" + url.QueryEscape(cursor)
		}
		out, err := c.do(ctx, call, vars{})
		if err != nil {
			return list, fmt.Errorf("list sandboxes: %w", err)
		}
		items, _ := Pick(out, c.f.List.Items).([]any)
		for _, it := range items {
			if s := c.sandbox(it, pickStr(it, c.f.List.ID)); s.ID != "" && strings.HasPrefix(pickStr(it, c.f.Get.Name), c.f.List.Mine) {
				list = append(list, s)
			}
		}
		next := pickStr(out, c.f.List.Next)
		if c.f.List.Next == "" || next == "" || next == cursor || len(items) == 0 {
			return list, nil
		}
		cursor = next
	}
}

// Count is how many sandboxes the account has, conch's or not: the check
// that the key and the list call work, without making anything.
func (c *Custom) Count(ctx context.Context) (all, mine int, err error) {
	out, err := c.do(ctx, c.f.List.Call, vars{})
	if err != nil {
		return 0, 0, err
	}
	items, ok := Pick(out, c.f.List.Items).([]any)
	if !ok && out != nil && Pick(out, c.f.List.Items) != nil {
		return 0, 0, fmt.Errorf("[list] items %q is not a list in the reply", c.f.List.Items)
	}
	for _, it := range items {
		if strings.HasPrefix(pickStr(it, c.f.Get.Name), c.f.List.Mine) {
			mine++
		}
	}
	return len(items), mine, nil
}

func (c *Custom) Start(ctx context.Context, id string) (Sandbox, error) {
	s, err := c.Get(ctx, id)
	if err != nil {
		return Sandbox{}, err
	}
	if s.State == StateStarted {
		return s, nil
	}
	if s.State.Going() {
		return s, &FailedError{ID: s.ID, State: s.State, Reason: s.Reason}
	}
	if !s.State.comingUp() {
		if _, err := c.do(ctx, c.f.Start, vars{"{id}": id}); err != nil {
			return s, fmt.Errorf("start sandbox: %w", err)
		}
		s.State = StateStarting
	}
	return c.wait(ctx, s, StateStarted)
}

func (c *Custom) Stop(ctx context.Context, id string) error {
	s, err := c.Get(ctx, id)
	if err != nil {
		return err
	}
	if s.State == StateStopped {
		return nil
	}
	if s.State != StateStopping {
		if _, err := c.do(ctx, c.f.Stop, vars{"{id}": id}); err != nil {
			return fmt.Errorf("stop sandbox: %w", err)
		}
		s.State = StateStopping
	}
	_, err = c.wait(ctx, s, StateStopped)
	return err
}

func (c *Custom) Delete(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	_, err := c.do(ctx, c.f.Delete, vars{"{id}": id})
	if err == nil || errors.Is(err, ErrNotFound) {
		return err
	}
	return fmt.Errorf("delete sandbox: %w", err)
}

func (c *Custom) SSHAccess(ctx context.Context, id string) (Access, error) {
	if id == "" {
		return Access{}, ErrNotFound
	}
	s, err := c.Get(ctx, id)
	if err != nil {
		return Access{}, err
	}
	v := vars{"{id}": id, "{name}": s.Name}
	reply := any(nil)
	if c.f.SSH.Access.Call != "" {
		if reply, err = c.do(ctx, c.f.SSH.Access, v); err != nil {
			return Access{}, fmt.Errorf("ask for ssh access: %w", err)
		}
	}
	fill := func(s string) string {
		s = v.str(s)
		return regexp.MustCompile(`\{reply\.([^{}]+)\}`).ReplaceAllStringFunc(s, func(m string) string {
			return pickStr(reply, strings.TrimSuffix(strings.TrimPrefix(m, "{reply."), "}"))
		})
	}
	a := Access{User: fill(c.f.SSH.User), Host: fill(c.f.SSH.Host), Port: c.f.SSH.Port, PlainUser: c.f.SSH.Key != "none"}
	if a.User == "" || a.Host == "" {
		return Access{}, fmt.Errorf("%s gave no ssh user or host for %s", c.label(), id)
	}
	return a, nil
}

func (c *Custom) wait(ctx context.Context, s Sandbox, want State) (Sandbox, error) {
	for {
		switch {
		case s.State == want:
			return s, nil
		case s.State.failed():
			return s, &FailedError{ID: s.ID, State: s.State, Reason: s.Reason}
		}
		select {
		case <-ctx.Done():
			return s, fmt.Errorf("sandbox %s is still %s: %w", s.ID, s.State, ctx.Err())
		case <-time.After(pollEvery):
		}
		got, err := c.Get(ctx, s.ID)
		if err != nil {
			return s, err
		}
		s = got
	}
}

// CustomProviders lists the provider files that load, by name. One that
// does not — unreadable, wrong, or a built-in's name — is left out and
// said in bad, so the list never offers a provider that would fail.
func CustomProviders() (good []string, bad map[string]string) {
	paths, _ := filepath.Glob(filepath.Join(ProvidersDir(), "*.toml"))
	sort.Strings(paths)
	// The tree asks at every redraw whether a machine is a sandbox, so the
	// answer is kept until a file is added, removed or changed.
	var key strings.Builder
	key.WriteString(ProvidersDir())
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil {
			fmt.Fprintf(&key, "|%s:%d:%d", p, st.Size(), st.ModTime().UnixNano())
		}
	}
	customMu.Lock()
	defer customMu.Unlock()
	if customCache.key == key.String() && customCache.bad != nil {
		return append([]string(nil), customCache.good...), customCache.bad
	}
	good, bad = readCustom(paths)
	customCache.key, customCache.good, customCache.bad = key.String(), good, bad
	return append([]string(nil), good...), bad
}

var (
	customMu    sync.Mutex
	customCache struct {
		key  string
		good []string
		bad  map[string]string
	}
)

func readCustom(paths []string) (good []string, bad map[string]string) {
	bad = map[string]string{}
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), ".toml")
		f, problems, err := ReadProviderFile(p)
		switch {
		case err != nil:
			bad[name] = err.Error()
		case len(problems) > 0:
			bad[name] = problems[0]
		case f.Name != name:
			bad[name] = fmt.Sprintf("the file names %q", f.Name)
		default:
			good = append(good, name)
		}
	}
	return good, bad
}

// openCustom opens a provider file by name.
func openCustom(name string, cfg config.SandboxCfg) (Provider, error) {
	if !providerName.MatchString(name) {
		return nil, fmt.Errorf("unknown sandbox provider %q", name)
	}
	f, problems, err := ReadProviderFile(ProviderFilePath(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("unknown sandbox provider %q", name)
	}
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s: %s", ProviderFilePath(name), strings.Join(problems, "; "))
	}
	return NewCustom(f, cfg.Of(name)), nil
}

// IsCustom says whether name is a provider described by a file.
func IsCustom(name string) bool {
	if slicesContains(builtin, name) {
		return false
	}
	good, _ := CustomProviders()
	return slicesContains(good, name)
}
