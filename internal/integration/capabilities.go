package integration

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

// Capabilities is what an integration can do, as the registry works it
// out from its file when the index is built (conch-integrations' "What
// each one can do"): it cannot disagree with the file it came from. An
// entry without them — a built-in, an index from before them, the old
// mirror — is nil, and conch decides from the file as it always has.
//
// A sandbox provider's fill Operations, SSH, Create, Settings, Size,
// Lifetime, States, List, Auth and Needs; an integration's Operations,
// Auth, Needs, Detect, States and Follow.
type Capabilities struct {
	// Operations are the calls the file has, in conch's order; format 4's
	// are snapshot_save, snapshot_list, snapshot_forget, preview and
	// usage.
	Operations []string `json:"operations"`
	// SSH is how ssh gets in; nil for a provider that is off (no [ssh]
	// yet).
	SSH *SSHCaps `json:"ssh,omitempty"`
	// Create are the placeholders the create call fills, its query's too:
	// what conch asks for and nothing else.
	Create   []string `json:"create,omitempty"`
	Settings []string `json:"settings,omitempty"`
	// Size is how a size is chosen: names, limits, the plan's, or "".
	Size string `json:"size,omitempty"`
	// Lifetime is whether a sandbox is given a length of life. The
	// registry's capabilities_version 1 said so as a bool, version 2 as
	// the numbers ({"default_mins": 120, "max_mins": 43200}); both are
	// read (UnmarshalJSON), and conch writes the bool it always has.
	Lifetime bool      `json:"lifetime,omitempty"`
	States   []string  `json:"states,omitempty"`
	List     *ListCaps `json:"list,omitempty"`
	Auth     *AuthCaps `json:"auth,omitempty"`
	Needs    []string  `json:"needs,omitempty"`
	Detect   []string  `json:"detect,omitempty"`
	// DetectOn is what each way of detecting looks for: an import's files,
	// ~/ paths on this machine, which say it is here before it is
	// installed.
	DetectOn map[string][]string `json:"detect_on,omitempty"`
	Follow   bool                `json:"follow,omitempty"`
}

// SSHCaps is whose key opens a sandbox (account, authorize, none), whether
// conch asks [ssh.access] first, and where the port comes from (fixed,
// reply, endpoint).
type SSHCaps struct {
	Key    string `json:"key"`
	Access bool   `json:"access"`
	Port   string `json:"port"`
}

// ListCaps is whether the list is paged, and whether it lists only conch's.
type ListCaps struct {
	Paged   bool `json:"paged"`
	AllMine bool `json:"all_mine"`
}

// AuthCaps is how the key is sent: a header (named), bearer, basic, or in
// the URL.
type AuthCaps struct {
	Type   string `json:"type"`
	Header string `json:"header,omitempty"`
}

// Can is whether op is one of its operations. Nil can everything: conch
// then goes by the file.
func (c *Capabilities) Can(op string) bool {
	return c == nil || slices.Contains(c.Operations, op)
}

// Takes is whether create fills placeholder (cpu, memory_mb, …). Nil
// takes everything.
func (c *Capabilities) Takes(placeholder string) bool {
	return c == nil || slices.Contains(c.Create, placeholder)
}

// TakesPart is whether create sends a part of a size — cpu, memory or
// disk — in either unit.
func (c *Capabilities) TakesPart(part string) bool {
	switch part {
	case "memory", "disk":
		return c.Takes(part+"_gib") || c.Takes(part+"_mb")
	}
	return c.Takes(part)
}

// Off is a sandbox provider the registry lists but conch cannot reach a
// machine of: its file has no [ssh] yet.
func (c *Capabilities) Off(kind string) bool {
	return c != nil && kind == KindSandbox && c.SSH == nil
}

// sshWords says how ssh gets in, for people.
func (s *SSHCaps) words() string {
	if s == nil {
		return "no ssh yet"
	}
	var key string
	switch s.Key {
	case "authorize":
		key = "a key conch hands it"
	case "none":
		key = "a token the provider gives for each connection"
	default:
		key = "a key on your account there"
	}
	switch s.Port {
	case "reply":
		key += ", on the port its API names"
	case "endpoint":
		key += ", through the provider's forwarder"
	}
	return key
}

// SSHWords says how ssh gets into a provider's sandboxes; "" for an
// integration, or none known.
func (c *Capabilities) SSHWords(kind string) string {
	if c == nil || kind != KindSandbox {
		return ""
	}
	return c.SSH.words()
}

// NeedsWords says what the person supplies: "an API key, a base URL".
func (c *Capabilities) NeedsWords() string {
	if c == nil {
		return ""
	}
	var out []string
	for _, n := range c.Needs {
		switch n {
		case "key":
			out = append(out, "an API key")
		case "base_url":
			out = append(out, "the address of your own server")
		case "user":
			out = append(out, "a user name")
		default:
			out = append(out, n)
		}
	}
	return strings.Join(out, ", ")
}

// opWords are the operations as people read them.
var opWords = map[string]string{
	"create": "create", "get": "", "list": "", "start": "start", "stop": "stop", "delete": "delete",
	"snapshot_save": "snapshots", "snapshot_list": "", "snapshot_forget": "",
	"preview": "preview links", "usage": "usage",
	"runs": "runs", "failed_log": "failed logs", "rerun": "re-run", "stats": "stats",
	"comment": "comment", "link": "link", "move": "move", "send": "send",
}

// OperationWords is what it can do, as a short list: "create, start, stop,
// delete, snapshots, preview links". Lookups a person would not name (get,
// list) are left out of a sandbox's; a tracker's list and get are its
// point, so they stay.
func (c *Capabilities) OperationWords(kind string) string {
	if c == nil {
		return ""
	}
	var out []string
	for _, op := range c.Operations {
		w, known := opWords[op]
		switch {
		case kind == KindTickets && (op == "list" || op == "get"):
			w = op
		case !known:
			w = op
		}
		if w != "" && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return strings.Join(out, ", ")
}

// FileCapabilities is what an integration's file can do, as the
// registry's index would say it (scripts/registry.py
// integration_capabilities): for one installed before the registry said,
// or a file of the person's own.
func FileCapabilities(f *File) *Capabilities {
	c := &Capabilities{Operations: append([]string{}, f.Operations()...), Detect: []string{}, States: []string{}}
	// An import sends nothing anywhere, so it has no way of sending a key.
	if typ := firstSet(f.Auth.Type, "header"); f.Kind != KindImport {
		c.Auth = &AuthCaps{Type: typ}
		if typ == "header" {
			c.Auth.Header = firstSet(f.Auth.Header, "Authorization")
		}
	}
	if f.BaseURL == "ask" {
		c.Needs = append(c.Needs, "base_url")
	}
	if f.KeyEnv != "" {
		c.Needs = append(c.Needs, "key")
	}
	if f.Auth.UserEnv != "" {
		c.Needs = append(c.Needs, "user")
	}
	for _, d := range []struct {
		name string
		n    int
	}{{"checks", len(f.Detect.Checks)}, {"files", len(f.Detect.Files)}, {"remotes", len(f.Detect.Remotes)}} {
		if d.n > 0 {
			c.Detect = append(c.Detect, d.name)
		}
	}
	for _, st := range f.States {
		if !slices.Contains(c.States, st) {
			c.States = append(c.States, st)
		}
	}
	slices.Sort(c.States)
	c.Follow = len(f.FollowHosts) > 0
	return c
}

// CapabilitiesOf is what the installed file f can do: the registry's,
// kept when it was installed, while the file is still that one; else
// worked out from the file.
func CapabilitiesOf(f *File) *Capabilities {
	if recs, err := Records(); err == nil {
		if rec, ok := recs[f.Name]; ok && rec.Capabilities != nil {
			if edited, err := Edited(f.Name); err == nil && !edited {
				return rec.Capabilities
			}
		}
	}
	return FileCapabilities(f)
}

// KeepCapabilities records what the registry says name's file can do, for
// one installed before it said.
func KeepCapabilities(name string, caps *Capabilities) error {
	recs, err := Records()
	if err != nil {
		return err
	}
	rec, ok := recs[name]
	if !ok {
		return nil
	}
	rec.Capabilities = caps
	recs[name] = rec
	return writeRecords(recs)
}

// UnmarshalJSON reads capabilities of either version: lifetime as a bool
// (1) or as its numbers (2). An index this conch could not read a field
// of lost every entry at once — the Integrations page empty of the
// registry's sandboxes and icons — so a lifetime of a shape it does not
// know is read as none rather than failing the index.
func (c *Capabilities) UnmarshalJSON(data []byte) error {
	type plain Capabilities
	var v struct {
		*plain
		Lifetime json.RawMessage `json:"lifetime"`
	}
	v.plain = (*plain)(c)
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	c.Lifetime = lifetimeGiven(v.Lifetime)
	return nil
}

// lifetimeGiven is whether a capabilities' lifetime says sandboxes are
// given one: true, or numbers with any above zero.
func lifetimeGiven(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return false
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	var n struct {
		DefaultMins int `json:"default_mins"`
		MaxMins     int `json:"max_mins"`
	}
	if json.Unmarshal(raw, &n) == nil {
		return n.DefaultMins > 0 || n.MaxMins > 0
	}
	return false
}
