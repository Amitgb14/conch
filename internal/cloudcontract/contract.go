// Package cloudcontract checks requests and answers against Conch
// Cloud's contract (conch-cloud's contract/openapi.yaml), for the fakes
// conch's tests put in the control plane's place. The contract is owned
// by conch-cloud; it is read from a checkout beside this one (or
// CONCH_CLOUD_CONTRACT), and conch's own CI has none, so a test that finds
// no contract says so and checks behaviour against its fake alone.
//
// Only the subset of JSON Schema the contract uses is understood. Tests
// import this package; nothing conch ships does.
package cloudcontract

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Amitgb14/conch/internal/update"
)

// Env names an openapi.yaml to check against.
const Env = "CONCH_CLOUD_CONTRACT"

// Spec is a loaded contract.
type Spec struct {
	doc  map[string]any
	Path string
}

var (
	mu    sync.Mutex
	specs = map[string]*Spec{}
	whys  = map[string]string{}
)

// Load is the contract of version (or a later one of the same major), or
// nil with why it is not there.
func Load(version string) (*Spec, string) {
	mu.Lock()
	defer mu.Unlock()
	if s, ok := specs[version]; ok {
		return s, whys[version]
	}
	s, why := load(version)
	specs[version], whys[version] = s, why
	return s, why
}

func load(version string) (*Spec, string) {
	path := os.Getenv(Env)
	if path == "" {
		path = find(version)
	}
	if path == "" {
		return nil, "no conch-cloud checkout with contract " + version + " beside this one (set " + Env + " to its openapi.yaml)"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err.Error()
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, path + ": " + err.Error()
	}
	return &Spec{doc: doc, Path: path}, ""
}

// Version is the contract's info.version.
func (s *Spec) Version() string {
	info, _ := s.doc["info"].(map[string]any)
	v, _ := info["version"].(string)
	return v
}

// HasPath says whether the contract has path, a template or a real one.
func (s *Spec) HasPath(path string) bool { return s.pathItem(path) != nil }

// find looks up from here for conch-cloud/contract/openapi.yaml, or a
// conch-cloud worktree's, of version or later in the same major.
func find(version string) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	best, bestV := "", ""
	for ; ; dir = filepath.Dir(dir) {
		cands := []string{filepath.Join(dir, "conch-cloud", "contract", "openapi.yaml")}
		more, _ := filepath.Glob(filepath.Join(dir, "conch-cloud.worktrees", "*", "contract", "openapi.yaml"))
		cands = append(cands, more...)
		for _, c := range cands {
			v := specVersion(c)
			if v == "" || strings.Split(v, ".")[0] != strings.Split(version, ".")[0] || update.Compare(v, version) < 0 {
				continue
			}
			if best == "" || update.Compare(v, bestV) > 0 {
				best, bestV = c, v
			}
		}
		if filepath.Dir(dir) == dir {
			return best
		}
	}
}

var specVersionLine = regexp.MustCompile(`(?m)^info:\n(?:  .*\n)*?  version: *([0-9.]+)`)

func specVersion(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	m := specVersionLine.FindSubmatch(b)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// secured says whether op takes a bearer token: the document's security
// unless the operation names its own, and an empty list is none.
func (s *Spec) secured(op map[string]any) bool {
	sec, ok := op["security"]
	if !ok {
		sec = s.doc["security"]
	}
	list, _ := sec.([]any)
	return len(list) > 0
}

// checkEvents checks a server-sent event stream: each event's data is
// the schema as JSON.
func (s *Spec) checkEvents(sch any, body []byte, at string) []string {
	var errs []string
	sc := bufio.NewScanner(bytes.NewReader(body))
	n := 0
	for sc.Scan() {
		line := sc.Text()
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		n++
		var v any
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &v); err != nil {
			errs = append(errs, fmt.Sprintf("%s event %d: not JSON: %v", at, n, err))
			continue
		}
		errs = append(errs, s.check(sch, v, fmt.Sprintf("%s event %d", at, n), inResponse)...)
	}
	return errs
}

func (s *Spec) pathItem(path string) map[string]any {
	paths, _ := s.doc["paths"].(map[string]any)
	if it, ok := paths[path].(map[string]any); ok {
		return it
	}
	for tmpl, it := range paths {
		re := "^" + regexp.MustCompile(`\\\{[^}]+\\\}`).ReplaceAllString(regexp.QuoteMeta(tmpl), `[^/]+`) + "$"
		if regexp.MustCompile(re).MatchString(path) {
			m, _ := it.(map[string]any)
			return m
		}
	}
	return nil
}

func (s *Spec) op(path, method string) (map[string]any, bool) {
	it := s.pathItem(path)
	if it == nil {
		return nil, false
	}
	op, ok := it[strings.ToLower(method)].(map[string]any)
	return op, ok
}

func (s *Spec) resolve(sch any) map[string]any {
	m, _ := sch.(map[string]any)
	for m != nil {
		ref, ok := m["$ref"].(string)
		if !ok {
			return m
		}
		var cur any = s.doc
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			cm, _ := cur.(map[string]any)
			cur = cm[part]
		}
		m, _ = cur.(map[string]any)
	}
	return m
}

// mode says which way a body goes: a readOnly property may not be sent,
// a writeOnly one may not come back.
type mode int

const (
	inRequest mode = iota
	inResponse
)

// check is every way v breaks the schema.
func (s *Spec) check(sch any, v any, at string, m mode) []string {
	sc := s.resolve(sch)
	if sc == nil {
		return nil
	}
	var errs []string
	fail := func(f string, a ...any) { errs = append(errs, at+": "+fmt.Sprintf(f, a...)) }

	if all, ok := sc["allOf"].([]any); ok {
		merged := map[string]any{}
		for k, v := range sc {
			if k != "allOf" {
				merged[k] = v
			}
		}
		props := map[string]any{}
		var req []any
		for _, part := range append(all, map[string]any{"properties": sc["properties"], "required": sc["required"]}) {
			p := s.resolve(part)
			if pp, ok := p["properties"].(map[string]any); ok {
				for k, v := range pp {
					props[k] = v
				}
			}
			if r, ok := p["required"].([]any); ok {
				req = append(req, r...)
			}
			if t, ok := p["type"]; ok && merged["type"] == nil {
				merged["type"] = t
			}
		}
		merged["properties"], merged["required"] = props, req
		return s.check(merged, v, at, m)
	}
	if one, ok := sc["oneOf"].([]any); ok {
		if d, ok := sc["discriminator"].(map[string]any); ok {
			obj, _ := v.(map[string]any)
			name, _ := d["propertyName"].(string)
			kind, _ := obj[name].(string)
			mapping, _ := d["mapping"].(map[string]any)
			if ref, ok := mapping[kind].(string); ok {
				return s.check(map[string]any{"$ref": ref}, v, at, m)
			}
			return []string{fmt.Sprintf("%s: %s %q is none of the oneOf", at, name, kind)}
		}
		passed := 0
		for _, o := range one {
			if len(s.check(o, v, at, m)) == 0 {
				passed++
			}
		}
		if passed != 1 {
			fail("matches %d of oneOf, want exactly 1", passed)
		}
		return errs
	}
	if c, ok := sc["const"]; ok && fmt.Sprint(c) != fmt.Sprint(v) {
		fail("%v is not the const %v", v, c)
	}
	if enum, ok := sc["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if fmt.Sprint(e) == fmt.Sprint(v) {
				found = true
			}
		}
		if !found {
			fail("%v is not one of %v", v, enum)
		}
	}
	typ, _ := sc["type"].(string)
	switch typ {
	case "string":
		str, ok := v.(string)
		if !ok {
			fail("%T, want a string", v)
			break
		}
		if p, ok := sc["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(str) {
			fail("%q does not match %s", str, p)
		}
		if ml, ok := sc["maxLength"].(int); ok && len(str) > ml {
			fail("longer than %d", ml)
		}
		switch sc["format"] {
		case "date-time":
			if _, err := time.Parse(time.RFC3339, str); err != nil {
				fail("%q is not a date-time", str)
			}
		case "uri":
			if u, err := url.Parse(str); err != nil || u.Scheme == "" {
				fail("%q is not a uri", str)
			}
		}
	case "integer", "number":
		n, ok := v.(float64)
		if !ok {
			fail("%T, want a %s", v, typ)
			break
		}
		if typ == "integer" && n != math.Trunc(n) {
			fail("%v is not an integer", n)
		}
		if mn, ok := sc["minimum"].(int); ok && n < float64(mn) {
			fail("%v is under the minimum %d", n, mn)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			fail("%T, want a boolean", v)
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			fail("%T, want an array", v)
			break
		}
		for i, it := range arr {
			errs = append(errs, s.check(sc["items"], it, fmt.Sprintf("%s[%d]", at, i), m)...)
		}
	case "object", "":
		obj, ok := v.(map[string]any)
		if !ok {
			if typ == "object" {
				fail("%T, want an object", v)
			}
			break
		}
		props, hasProps := sc["properties"].(map[string]any)
		req, _ := sc["required"].([]any)
		if _, hasAP := sc["additionalProperties"]; !hasProps && !hasAP {
			// An object the contract gives no shape at all (Event.data)
			// is free-form, as JSON Schema says.
			for _, r := range req {
				if _, ok := obj[r.(string)]; !ok {
					fail("missing required %q", r)
				}
			}
			break
		}
		for _, r := range req {
			if _, ok := obj[r.(string)]; !ok {
				fail("missing required %q", r)
			}
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p, known := props[k]
			if !known {
				switch ap := sc["additionalProperties"].(type) {
				case map[string]any:
					errs = append(errs, s.check(ap, obj[k], at+"."+k, m)...)
				case bool:
					if !ap {
						fail("%q is not in the contract", k)
					}
				default:
					fail("%q is not in the contract", k)
				}
				continue
			}
			ps := s.resolve(p)
			if m == inRequest && ps["readOnly"] == true {
				fail("%q is read-only and was sent", k)
			}
			if m == inResponse && ps["writeOnly"] == true {
				fail("%q is write-only and came back", k)
			}
			errs = append(errs, s.check(p, obj[k], at+"."+k, m)...)
		}
	}
	return errs
}

// checkRequest is every way a request to path breaks the contract.
func (s *Spec) CheckRequest(method, path string, h http.Header, body []byte) []string {
	op, ok := s.op(path, method)
	if !ok {
		return []string{fmt.Sprintf("%s %s is not in the contract", method, path)}
	}
	var errs []string
	if h.Get("Conch-Contract") == "" {
		errs = append(errs, method+" "+path+": no Conch-Contract header")
	}
	if s.secured(op) && !strings.HasPrefix(h.Get("Authorization"), "Bearer ") {
		errs = append(errs, method+" "+path+": no bearer token")
	}
	if !s.secured(op) && h.Get("Authorization") != "" {
		errs = append(errs, method+" "+path+": a token sent where the contract takes none")
	}
	rb, _ := op["requestBody"].(map[string]any)
	if rb == nil {
		if len(bytes.TrimSpace(body)) > 0 {
			errs = append(errs, method+" "+path+": a body the contract does not take")
		}
		return errs
	}
	content, _ := rb["content"].(map[string]any)
	js, _ := content["application/json"].(map[string]any)
	if js == nil {
		return errs
	}
	if required, _ := rb["required"].(bool); !required && len(bytes.TrimSpace(body)) == 0 {
		return errs // an optional body, not sent
	}
	if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		errs = append(errs, method+" "+path+": Content-Type "+ct)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return append(errs, method+" "+path+": body is not JSON: "+err.Error())
	}
	return append(errs, s.check(js["schema"], v, method+" "+path+" body", inRequest)...)
}

// checkResponse is every way an answer breaks the contract.
func (s *Spec) CheckResponse(method, path string, status int, h http.Header, body []byte) []string {
	op, ok := s.op(path, method)
	if !ok {
		return nil // said by checkRequest
	}
	responses, _ := op["responses"].(map[string]any)
	r, ok := responses[fmt.Sprint(status)]
	if !ok {
		if status < 400 {
			return []string{fmt.Sprintf("%s %s answered %d, which the contract does not list", method, path, status)}
		}
		r = responses["default"]
	}
	rm := s.resolve(r)
	if rm == nil {
		return []string{fmt.Sprintf("%s %s %d: no response in the contract", method, path, status)}
	}
	content, _ := rm["content"].(map[string]any)
	if len(content) == 0 {
		if len(bytes.TrimSpace(body)) > 0 {
			return []string{fmt.Sprintf("%s %s %d: a body where the contract has none", method, path, status)}
		}
		return nil
	}
	ct := strings.Split(h.Get("Content-Type"), ";")[0]
	media, ok := content[ct].(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s %s %d: Content-Type %q, the contract says %v", method, path, status, ct, mapKeys(content))}
	}
	at := fmt.Sprintf("%s %s %d", method, path, status)
	if ct == "text/event-stream" {
		return s.checkEvents(media["schema"], body, at)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return []string{fmt.Sprintf("%s: not JSON: %v", at, err)}
	}
	return s.check(media["schema"], v, at, inResponse)
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
