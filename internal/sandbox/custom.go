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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/integration"
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
// Format 3 adds headers, which a format-2 conch would leave out and so
// send requests the provider refuses.
// Format 4 adds settings, sizes, lifetimes, snapshots, preview links,
// usage and an ssh address that arrives late (format4.go): what conch's
// own providers did, so that they could become files.
const ProviderFormat = 4

// PublicKey is the public key a sandbox is made with when a provider file
// puts {public_key} in its create body. internal/remote sets it (the
// person's own key, else conch's), since that is where keys are found.
var PublicKey func(ctx context.Context) (string, error)

// KeyLookup finds a key by its variable's name somewhere besides the
// environment — the Keychain, where Integrations keeps the keys it is
// given — for a provider whose key is in neither config.toml nor the
// environment. conch's main sets it; nil (every test) asks nothing.
var KeyLookup func(keyEnv string) string

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
	// Headers are sent with every call; a call's own headers are laid
	// over them.
	Headers map[string]string `toml:"headers"`

	Create CreateCall        `toml:"create"`
	Get    GetCall           `toml:"get"`
	List   ListCall          `toml:"list"`
	Start  Call              `toml:"start"`
	Stop   Call              `toml:"stop"`
	Delete Call              `toml:"delete"`
	States map[string]string `toml:"states"`
	SSH    SSHWay            `toml:"ssh"`

	// Format 4 (format4.go).
	Settings []Setting    `toml:"setting"`
	Size     SizeSpec     `toml:"size"`
	Lifetime LifeSpec     `toml:"lifetime"`
	Snapshot SnapshotSpec `toml:"snapshot"`
	Preview  PreviewSpec  `toml:"preview"`
	Usage    UsageSpec    `toml:"usage"`
}

// Call is one request: "METHOD /path", the path's {id} filled in, a body
// whose string values may be placeholders, and headers of its own — an
// Idempotency-Key = "{uuid}", say, or a delete's confirmation.
type Call struct {
	Call    string            `toml:"call"`
	Body    map[string]any    `toml:"body"`
	Headers map[string]string `toml:"headers"`
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
	// MemoryGiB and DiskGiB (format 4) are for an API that says GiB.
	MemoryGiB string `toml:"memory_gib"`
	DiskGiB   string `toml:"disk_gib"`
	// Desired (format 4) is where the provider is taking the sandbox, read
	// through [states]: Daytona's says "destroyed" for one deleted a moment
	// ago that still reports itself started.
	Desired string `toml:"desired"`
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
	// AllMine (format 4) says the call asks only for conch's own — by a
	// label create puts on them — so every item is conch's, whatever its
	// name: sandboxes conch's own Daytona provider made were named after
	// their label alone.
	AllMine bool `toml:"all_mine"`
}

// SSHWay is how ssh gets in. Key says whose key opens it: "account", a key
// registered with the person's account there; "authorize", one conch
// hands over through Authorize; "none", a credential in User from Access.
type SSHWay struct {
	Host string  `toml:"host"`
	User string  `toml:"user"`
	Port SSHPort `toml:"port"`
	Key  string  `toml:"key"`
	// Authorize lets a public key in ({public_key} in its body).
	Authorize Call `toml:"authorize"`
	// Access is asked before each connection when the way in is a
	// credential handed out per connection; Host and User may then read
	// its reply as {reply.PATH}.
	Access Call `toml:"access"`
	// Endpoint (format 4) is host:port from Access's reply, used instead
	// of Host and Port when it is there: boat.dev reaches a machine with no
	// address of its own through a forwarder.
	Endpoint string `toml:"endpoint"`
	// WaitSecs (format 4) is how long to keep asking Access while it gives
	// no host: a sandbox may answer started a moment before it has one.
	WaitSecs int `toml:"wait_secs"`
	// Command (format 4) is an ssh command line in Access's reply, "ssh -p
	// PORT USER@HOST": its host and port win when it reads, which is how a
	// self-hosted Daytona's gateway is found.
	Command string `toml:"command"`
	// Expires (format 4) is when Access's credential stops working.
	Expires string `toml:"expires"`
}

// SSHPort is ssh's port: a number, or (format 3) "{reply.PATH}", read
// from [ssh.access]'s reply for a gateway that says its port per
// connection, as it says its host.
type SSHPort struct {
	N     int
	Reply string // the PATH of "{reply.PATH}"
}

var replyRef = regexp.MustCompile(`\{reply\.([^{}]+)\}`)

func (p *SSHPort) UnmarshalTOML(v any) error {
	switch x := v.(type) {
	case int64:
		p.N = int(x)
		if int64(p.N) != x {
			p.N = -1 // out of range, said by Problems
		}
	case string:
		m := replyRef.FindStringSubmatch(x)
		if m == nil || m[0] != x {
			return fmt.Errorf(`[ssh] port %q: a number, or "{reply.PATH}" alone`, x)
		}
		p.Reply = m[1]
	default:
		return fmt.Errorf(`[ssh] port: a number, or "{reply.PATH}"`)
	}
	return nil
}

func (p SSHPort) String() string {
	if p.Reply != "" {
		return "{reply." + p.Reply + "}"
	}
	return strconv.Itoa(p.N)
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
	} else {
		if need := f.needs2(); need != "" && f.Format < 2 {
			add("%s needs format = 2", need)
		}
		if need := f.needs3(); need != "" && f.Format < 3 {
			add("%s needs format = 3", need)
		}
		if need := f.needs4(); need != "" && f.Format < 4 {
			add("%s needs format = 4", need)
		}
	}
	if !providerName.MatchString(f.Name) {
		add("name %q: lowercase letters, digits and -, starting with a letter", f.Name)
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
	for _, c := range f.headerSets() {
		for _, e := range f.checkHeaders(c.headers) {
			add("%s: %s", c.what, e)
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
	switch {
	case f.List.AllMine:
		// The list call asks only for conch's own; names say nothing.
	case f.List.Mine == "" || f.Get.Name == "":
		add("[list] mine and [get] name: conch names what it makes, and finds its own by that name")
	case !bodyUses(f.Create.Body, "{name}"):
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
	if f.SSH.Port.N < 0 || f.SSH.Port.N > 65535 {
		add("[ssh] port %d", f.SSH.Port.N)
	}
	if f.SSH.Port.Reply != "" && f.SSH.Access.Call == "" {
		add("[ssh] port %s: there is no [ssh.access] reply to read it from", f.SSH.Port)
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
	p = append(p, f.problems4()...)
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

// needs3 names the first thing in the file that format 2 did not have.
func (f *ProviderFile) needs3() string {
	for _, c := range f.headerSets() {
		if len(c.headers) > 0 {
			return c.what
		}
	}
	if f.SSH.Port.Reply != "" {
		return "[ssh] port from the reply"
	}
	return ""
}

type headerSet struct {
	what    string
	headers map[string]string
}

// headerSets is every place a file may give headers, named as a problem
// with them is said.
func (f *ProviderFile) headerSets() []headerSet {
	return []headerSet{{"headers", f.Headers}, {"[create] headers", f.Create.Headers}, {"[get] headers", f.Get.Headers},
		{"[list] headers", f.List.Headers}, {"[start] headers", f.Start.Headers}, {"[stop] headers", f.Stop.Headers},
		{"[delete] headers", f.Delete.Headers}, {"[ssh.authorize] headers", f.SSH.Authorize.Headers},
		{"[ssh.access] headers", f.SSH.Access.Headers}}
}

// placeholders a header value may use: {uuid} is new for every request.
// Never {key}: the key goes in auth_header alone, so a file cannot send it
// anywhere a person reading the file would not look for it.
var headerPlaceholders = map[string]bool{"{id}": true, "{name}": true, "{uuid}": true}

var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// conchHeaders are set by conch itself, or by Go for the request; a file
// that set them would break the request or move the key.
var conchHeaders = []string{"Host", "Content-Length", "Content-Type", "Accept", "Transfer-Encoding", "Connection"}

func (f *ProviderFile) checkHeaders(h map[string]string) []string {
	var p []string
	auth := http.CanonicalHeaderKey(firstSet(f.AuthHeader, "Authorization"))
	for _, k := range sortedKeys(h) {
		val := h[k]
		ck := http.CanonicalHeaderKey(k)
		switch {
		case !headerName.MatchString(k):
			p = append(p, fmt.Sprintf("%q is not a header name", k))
			continue
		case ck == auth:
			p = append(p, fmt.Sprintf("%s carries the key: set it with auth_header and auth_value", k))
			continue
		case slicesContains(conchHeaders, ck):
			p = append(p, fmt.Sprintf("%s is conch's to set", k))
			continue
		case strings.ContainsAny(val, "\r\n\x00"):
			p = append(p, fmt.Sprintf("%s's value has a line break", k))
			continue
		}
		var bad []string
		for _, ph := range placeholder.FindAllString(val, -1) {
			if !headerPlaceholders[ph] {
				bad = append(bad, ph)
			}
		}
		if len(bad) > 0 {
			p = append(p, fmt.Sprintf("%s: unknown placeholder %s (a header may use {id}, {name}, {uuid})", k, strings.Join(bad, ", ")))
		}
	}
	return p
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

func init() {
	for _, p := range placeholders4 {
		bodyPlaceholders[p] = true
	}
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
		s := c.Call
		h := f.headersOf(c)
		for _, k := range sortedKeys(h) {
			s += fmt.Sprintf(" [%s: %s]", k, h[k])
		}
		if len(c.Body) == 0 {
			return s
		}
		b, _ := json.Marshal(c.Body)
		return s + " " + string(b)
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
	if f.SSH.Port.Reply != "" || f.SSH.Port.N != 0 && f.SSH.Port.N != 22 {
		way += " -p " + f.SSH.Port.String()
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
	// What config.toml chose for new sandboxes (format 4's settings and
	// size), and the life one is given, in minutes.
	region, image, size string
	cpu, mem, disk      int
	autoStop            int
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
	if c.key == "" && KeyLookup != nil {
		c.key = strings.TrimSpace(KeyLookup(keyEnv))
	}
	if cfg.APIURL != "" {
		f.API = cfg.APIURL
	}
	c.region, c.image, c.size = cfg.RegionOr(), cfg.ImageOr(), cfg.Size
	c.cpu, c.mem, c.disk, c.autoStop = cfg.CPU, cfg.Memory, cfg.Disk, cfg.AutoStop
	// A size name in the image's place is what conch's own boat.dev
	// provider read there (snapshot = "large"), so it is still a size.
	if c.size == "" && slices.Contains(f.Size.Names, c.image) {
		c.size, c.image = c.image, ""
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
	n += "."
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

// fillPath puts each value in the path escaped, as a path segment or a
// query value; a query parameter left empty is dropped, so "?region=" asks
// for the account's default rather than for a region called nothing.
func fillPath(path string, v vars) string {
	path = placeholder.ReplaceAllStringFunc(path, func(p string) string {
		x, ok := v[p]
		if !ok {
			return p
		}
		return url.PathEscape(fmt.Sprint(x))
	})
	base, query, ok := strings.Cut(path, "?")
	if !ok {
		return path
	}
	var keep []string
	for _, kv := range strings.Split(query, "&") {
		if k, val, has := strings.Cut(kv, "="); kv != "" && !(has && val == "" && k != "") {
			keep = append(keep, kv)
		}
	}
	if len(keep) == 0 {
		return base
	}
	return base + "?" + strings.Join(keep, "&")
}

func (c *Custom) do(ctx context.Context, call Call, v vars) (any, error) {
	if err := c.Check(); err != nil {
		return nil, err
	}
	method, path, _ := strings.Cut(strings.TrimSpace(call.Call), " ")
	path = fillPath(path, v)
	var body any
	if call.Body != nil {
		b, _ := v.fill(call.Body)
		body = b
	}
	return c.request(ctx, method, path, body, c.headers(call, v))
}

// headersOf is the file's headers with the call's own laid over them, by
// name as HTTP compares names (Idempotency-Key and idempotency-key are one).
func (f *ProviderFile) headersOf(call Call) map[string]string {
	if len(f.Headers) == 0 && len(call.Headers) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, set := range []map[string]string{f.Headers, call.Headers} {
		for k, val := range set {
			out[http.CanonicalHeaderKey(k)] = val
		}
	}
	return out
}

// headers fills a call's headers for one request. A header whose value is
// a placeholder with nothing to put there is left out, as a body's is;
// {uuid} is new for each request, so no two are taken for a retry.
func (c *Custom) headers(call Call, v vars) http.Header {
	h := c.f.headersOf(call)
	if len(h) == 0 {
		return nil
	}
	hv := vars{"{uuid}": newUUID()}
	for k, x := range v {
		hv[k] = x
	}
	out := http.Header{}
	for k, val := range h {
		x, ok := hv.fill(val)
		// Inside a longer value an unfilled placeholder would go out as its
		// own letters; the header is left out instead.
		if s := fmt.Sprint(x); ok && s != "" && !placeholder.MatchString(s) {
			out.Set(k, s)
		}
	}
	return out
}

// newUUID is a random (version 4) UUID.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (c *Custom) request(ctx context.Context, method, path string, body any, headers http.Header) (any, error) {
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
	// The file's headers first, so none can take the key's or conch's place
	// (Problems refuses those names too).
	for k, vals := range headers {
		req.Header[k] = vals
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
		msg := replyMessage(data)
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

// Pick reads a dotted path out of a JSON value: "a.b", "items.0.id",
// "networks.v4[type=public].ip_address". The grammar is the integration
// files', which grew out of this one.
func Pick(v any, path string) any { return integration.Pick(v, path, nil) }

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
	if c.f.Get.Desired != "" && customStates[c.f.States[pickStr(v, c.f.Get.Desired)]].Going() {
		state = StateDestroyed
	}
	reason := pickStr(v, c.f.Get.Reason)
	// An error by another name (build_failed) keeps the provider's word.
	if state == StateError && raw != string(StateError) {
		reason = firstSet(reason, raw)
	}
	return Sandbox{
		ID: id, Name: firstSet(pickStr(v, c.f.Get.Name), id), State: state, Reason: reason,
		CPU: int(pickNum(v, c.f.Get.CPU)), Memory: int(pickNum(v, c.f.Get.MemoryMB)/1024 + pickNum(v, c.f.Get.MemoryGiB)),
		Disk: int(pickNum(v, c.f.Get.DiskMB)/1024 + pickNum(v, c.f.Get.DiskGiB)), Created: pickTime(v, c.f.Get.Created),
	}
}

// nameFor is a name of conch's: the file's prefix, the label as far as a
// name takes it, and a few random letters, as for Tensorlake.
func (c *Custom) nameFor(label string) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	slug := strings.Trim(nameChars.ReplaceAllString(strings.ToLower(label), "-"), "-")
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
	spec = c.withDefaults(spec)
	if err := c.CheckSize(spec.Size, spec.CPU, spec.Memory, spec.Disk); err != nil {
		return Sandbox{}, err
	}
	for _, s := range [][2]string{{"region", spec.Region}, {"image", spec.Snapshot}} {
		if err := c.CheckSetting(s[0], s[1]); err != nil {
			return Sandbox{}, err
		}
	}
	v := vars{"{name}": name, "{label}": spec.Name, "{cpu}": spec.CPU, "{memory_gib}": spec.Memory,
		"{memory_mb}": spec.Memory * 1024, "{disk_gib}": spec.Disk, "{disk_mb}": spec.Disk * 1024,
		"{snapshot}": spec.Snapshot, "{image}": spec.Snapshot, "{region}": spec.Region, "{size}": spec.Size,
		"{env}": spec.Env, "{auto_stop_secs}": spec.AutoStop * 60}
	if c.f.Lifetime.DefaultMins > 0 {
		v["{ttl_secs}"] = int(c.ttl(spec.AutoStop).Seconds())
	}
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

// withDefaults fills what the spec leaves empty with what config.toml
// chose for this provider. A size name given where the image goes is a
// size, as conch's own boat.dev provider read it.
func (c *Custom) withDefaults(spec Spec) Spec {
	if spec.Size == "" && slices.Contains(c.f.Size.Names, spec.Snapshot) {
		spec.Size, spec.Snapshot = spec.Snapshot, ""
	}
	spec.Region = firstSet(spec.Region, c.region)
	spec.Snapshot = firstSet(spec.Snapshot, c.image)
	if spec.CPU == 0 && spec.Memory == 0 && spec.Disk == 0 && spec.Size == "" {
		spec.Size, spec.CPU, spec.Memory, spec.Disk = firstSet(c.size, c.f.Size.Default), c.cpu, c.mem, c.disk
		if len(c.f.Size.Names) == 0 {
			spec.Size = ""
		}
	}
	if spec.AutoStop == 0 {
		spec.AutoStop = c.autoStop
	}
	return spec
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
	s := c.sandbox(out, id)
	if s.State.Going() {
		// On its way out, and not coming back: said as what callers can
		// act on, rather than a state they would wait on for ever.
		return Sandbox{}, ErrNotFound
	}
	return s, nil
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
			if s := c.sandbox(it, pickStr(it, c.f.List.ID)); s.ID != "" && c.mine(it) && !s.State.Going() {
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

// mine says whether a listed sandbox is conch's: by its name, unless the
// list call asked only for conch's own.
func (c *Custom) mine(item any) bool {
	return c.f.List.AllMine || strings.HasPrefix(pickStr(item, c.f.Get.Name), c.f.List.Mine)
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
		if c.mine(it) {
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
		v := vars{"{id}": id}
		if c.f.Lifetime.DefaultMins > 0 {
			v["{ttl_secs}"] = int(c.ttl(c.autoStop).Seconds())
		}
		if _, err := c.do(ctx, c.f.Start, v); err != nil {
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
	if errors.Is(err, ErrNotFound) {
		// A 404 says the sandbox is gone — or that the file's route is
		// wrong, which reads the same. Every caller takes ErrNotFound as
		// "already deleted" and forgets the machine, so a wrong [delete]
		// would leave a sandbox running and billing with nothing to say
		// so. Ask whether it is still there.
		s, gerr := c.Get(ctx, id)
		switch {
		case errors.Is(gerr, ErrNotFound):
			return ErrNotFound
		case gerr != nil:
			return fmt.Errorf("delete sandbox: %s's [delete] call (%s) answered 404, and asking whether %s is still there failed: %w", c.f.Name, c.f.Delete.Call, id, gerr)
		case s.State.Going():
			return nil
		default:
			return fmt.Errorf("delete sandbox: %s's [delete] call (%s) answered 404, but %s is still there (%s): the provider file's [delete] call may be wrong — check it, and delete the sandbox in the provider's console meanwhile", c.f.Name, c.f.Delete.Call, id, s.State)
		}
	}
	if err != nil {
		return fmt.Errorf("delete sandbox: %w", err)
	}
	return nil
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
	fill := func(s string) string {
		s = v.str(s)
		return replyRef.ReplaceAllStringFunc(s, func(m string) string {
			return pickStr(reply, strings.TrimSuffix(strings.TrimPrefix(m, "{reply."), "}"))
		})
	}
	deadline := time.Now().Add(time.Duration(c.f.SSH.WaitSecs) * waitUnit)
	for c.f.SSH.Access.Call != "" {
		if reply, err = c.do(ctx, c.f.SSH.Access, v); err != nil {
			return Access{}, fmt.Errorf("ask for ssh access: %w", err)
		}
		// A sandbox that has just come up may have no address for a
		// moment; a file that says so is asked again for a while.
		if fill(c.f.SSH.Host) != "" || (c.f.SSH.Endpoint != "" && strings.TrimSpace(fill(c.f.SSH.Endpoint)) != "") ||
			c.f.SSH.WaitSecs == 0 || time.Now().After(deadline) {
			break
		}
		if err := pause(ctx); err != nil {
			return Access{}, fmt.Errorf("sandbox %s has no address yet: %w", id, err)
		}
	}
	a := Access{User: fill(c.f.SSH.User), Host: fill(c.f.SSH.Host), Port: c.f.SSH.Port.N, PlainUser: c.f.SSH.Key != "none"}
	if c.f.SSH.Command != "" {
		if host, port, ok := parseSSHCommand(fill(c.f.SSH.Command), a.User); ok {
			a.Host, a.Port = host, port
		}
	}
	if c.f.SSH.Expires != "" {
		a.Expires, _ = time.Parse(time.RFC3339, fill(c.f.SSH.Expires))
	}
	if ep := strings.TrimSpace(fill(c.f.SSH.Endpoint)); c.f.SSH.Endpoint != "" && ep != "" {
		host, port, err := endpoint(ep)
		if err != nil {
			return Access{}, fmt.Errorf("%s: %w", c.label(), err)
		}
		if a.User == "" {
			return Access{}, fmt.Errorf("%s gave no ssh user for %s", c.label(), id)
		}
		a.Host, a.Port = host, port
		return a, nil
	}
	if a.User == "" || a.Host == "" {
		if c.f.SSH.WaitSecs > 0 {
			return Access{}, fmt.Errorf("sandbox %s has no address yet (it is %s)", id, s.State)
		}
		return Access{}, fmt.Errorf("%s gave no ssh user or host for %s", c.label(), id)
	}
	if path := c.f.SSH.Port.Reply; path != "" {
		// Said rather than left to ssh's default: the default is what a
		// gateway that names its port does not listen on.
		raw := pickStr(reply, path)
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 65535 {
			return Access{}, fmt.Errorf("%s gave no ssh port for %s at %q (got %q)", c.label(), id, path, raw)
		}
		a.Port = n
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
		if err := pause(ctx); err != nil {
			return s, fmt.Errorf("sandbox %s is still %s: %w", s.ID, s.State, err)
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

// IsCustom says whether name is a provider described by a file: every
// provider is, now that conch has none of its own, so it is whether one
// loads.
func IsCustom(name string) bool {
	good, _ := CustomProviders()
	return slicesContains(good, name)
}
