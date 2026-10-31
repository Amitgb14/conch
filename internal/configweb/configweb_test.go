package configweb

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

// home gives a test a CONCH_HOME of its own, so nothing touches the real
// config.toml.
func home(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("CONCH_HOME", d)
	t.Setenv("HOME", d)
	return d
}

func newServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s, err := New(Choices{Providers: []string{"daytona", "boat"}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func do(t *testing.T, ts *httptest.Server, method, token string, body any) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		data, _ := json.Marshal(b)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, ts.URL+"/api/config", r)
	if token != "" {
		req.Header.Set(TokenHeader, token)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res, data
}

func get(t *testing.T, s *Server, ts *httptest.Server) Payload {
	t.Helper()
	res, data := do(t, ts, http.MethodGet, s.Token(), nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET: %d %s", res.StatusCode, data)
	}
	var p Payload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTokenIsRequired(t *testing.T) {
	home(t)
	s, ts := newServer(t)
	for _, tok := range []string{"", "nope", s.Token() + "x", strings.ToUpper(s.Token())} {
		res, data := do(t, ts, http.MethodGet, tok, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q: %d, want 401", tok, res.StatusCode)
		}
		if strings.Contains(string(data), s.Token()) {
			t.Errorf("a refusal gave the token away: %s", data)
		}
	}
	// A save without it writes nothing.
	res, _ := do(t, ts, http.MethodPut, "", map[string]any{"version": "none", "raw": "[ui]\ntheme = \"nord\"\n"})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("PUT without the token: %d", res.StatusCode)
	}
	if _, err := os.Stat(config.File()); err == nil {
		t.Fatal("a save without the token wrote config.toml")
	}
}

func TestTokensDiffer(t *testing.T) {
	a, _ := New(Choices{})
	b, _ := New(Choices{})
	if a.Token() == b.Token() || len(a.Token()) < 32 {
		t.Fatalf("tokens %q and %q", a.Token(), b.Token())
	}
	if u := a.URL("127.0.0.1:1234"); u != "http://127.0.0.1:1234/#t="+a.Token() {
		t.Fatalf("URL %q", u)
	}
}

// A name that resolves to 127.0.0.1 must not reach the API, even with the
// token: that is how a site would get past the browser's same-origin rule.
func TestOtherHostsAreRefused(t *testing.T) {
	home(t)
	s, ts := newServer(t)
	for _, host := range []string{"evil.example", "evil.example:80", "127.0.0.1.nip.io"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/config", nil)
		req.Host = host
		req.Header.Set(TokenHeader, s.Token())
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("API from host %q: %d", host, res.StatusCode)
		}
		req, _ = http.NewRequest(http.MethodGet, ts.URL+"/", nil)
		req.Host = host
		res, err = ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("page from host %q: %d", host, res.StatusCode)
		}
	}
	for _, host := range []string{"localhost:1", "127.0.0.1:9", "[::1]:9", "localhost"} {
		if !localHost(host) {
			t.Errorf("%q should count as this computer", host)
		}
	}
}

func TestReadWithNoFileIsTheDefaults(t *testing.T) {
	home(t)
	s, ts := newServer(t)
	p := get(t, s, ts)
	if p.Exists || p.Version != "none" || p.Raw != "" {
		t.Fatalf("exists %v version %q raw %q", p.Exists, p.Version, p.Raw)
	}
	if p.Path != config.File() {
		t.Fatalf("path %q", p.Path)
	}
	ui, _ := p.Config["ui"].(map[string]any)
	if ui["theme"] != "conch" || ui["mouse"] != true {
		t.Fatalf("ui %v", ui)
	}
	if len(p.Schema) == 0 || len(p.Problems) != 0 {
		t.Fatalf("schema %d problems %v", len(p.Schema), p.Problems)
	}
}

func TestSaveTheForm(t *testing.T) {
	home(t)
	s, ts := newServer(t)
	var got []config.Config
	s.OnSave = func(c config.Config) { got = append(got, c) }
	// A provider this build doesn't know is kept, as Save keeps it.
	if err := os.WriteFile(config.File(), []byte("[sandbox.e2b]\ntarget = \"eu\"\nidle_stop = 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := get(t, s, ts)
	cfg := p.Config
	cfg["ui"].(map[string]any)["theme"] = "nord"
	cfg["web"] = map[string]any{"port": 9000, "url": "https://x.ts.net"}
	cfg["notify"].(map[string]any)["limit_at"] = []any{50, 90}
	sb := cfg["sandbox"].(map[string]any)
	sb["e2b"].(map[string]any)["idle_stop"] = nil // back to the default
	sb["daytona"] = map[string]any{"price_cpu_hour": 1, "price_gib_hour": 0.25, "env": []any{"A", "B"}, "restore_running": false}
	cfg["actions"] = []any{map[string]any{"name": "Test", "run": "go test ./...", "on": []any{"branch"}, "keep": false}, nil}
	res, data := do(t, ts, http.MethodPut, s.Token(), map[string]any{"version": p.Version, "config": cfg})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT: %d %s", res.StatusCode, data)
	}
	var after Payload
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatal(err)
	}
	if after.Version == p.Version || !after.Exists {
		t.Fatalf("the answer should be the new file: %+v", after.Version)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UI.Theme != "nord" || loaded.Web.Port != 9000 || loaded.Web.URL != "https://x.ts.net" {
		t.Fatalf("loaded %+v %+v", loaded.UI, loaded.Web)
	}
	if !reflect.DeepEqual(loaded.Notify.LimitAt, []int{50, 90}) {
		t.Fatalf("limit_at %v", loaded.Notify.LimitAt)
	}
	e2b := loaded.Sandbox.Of("e2b")
	if e2b.Target != "eu" || e2b.IdleStop != nil {
		t.Fatalf("e2b %+v", e2b)
	}
	d := loaded.Sandbox.Of("daytona")
	if d.PriceCPUHour != 1 || d.PriceGiBHour != 0.25 || d.RestoresRunning() || !reflect.DeepEqual(d.Env, []string{"A", "B"}) {
		t.Fatalf("daytona %+v", d)
	}
	if len(loaded.Actions) != 1 || loaded.Actions[0].Keeps() || loaded.Actions[0].Name != "Test" {
		t.Fatalf("actions %+v", loaded.Actions)
	}
	if len(got) != 1 || got[0].UI.Theme != "nord" {
		t.Fatalf("OnSave got %+v", got)
	}
	if st, _ := os.Stat(config.File()); st.Mode().Perm() != 0o600 {
		t.Fatalf("config.toml is %v", st.Mode().Perm())
	}
}

func TestSaveRefusesAStaleVersion(t *testing.T) {
	home(t)
	s, ts := newServer(t)
	p := get(t, s, ts)
	// Somebody else writes the file after the page read it.
	if err := os.WriteFile(config.File(), []byte("[ui]\ntheme = \"dracula\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	s.OnSave = func(config.Config) { called = true }
	p.Config["ui"].(map[string]any)["theme"] = "nord"
	res, data := do(t, ts, http.MethodPut, s.Token(), map[string]any{"version": p.Version, "config": p.Config})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("PUT: %d %s", res.StatusCode, data)
	}
	var e apiError
	_ = json.Unmarshal(data, &e)
	if e.Current == nil || !strings.Contains(e.Current.Raw, "dracula") {
		t.Fatalf("the refusal should carry the newer file: %s", data)
	}
	raw, _ := os.ReadFile(config.File())
	if !strings.Contains(string(raw), "dracula") || called {
		t.Fatalf("the newer file was written over: %s (OnSave %v)", raw, called)
	}
	// The version it handed back saves.
	res, data = do(t, ts, http.MethodPut, s.Token(), map[string]any{"version": e.Current.Version, "config": p.Config})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT with the new version: %d %s", res.StatusCode, data)
	}
}

func TestOnlyOneOfTwoSavesFromOneVersionLands(t *testing.T) {
	home(t)
	s, ts := newServer(t)
	p := get(t, s, ts)
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw := "[ui]\ntheme = \"nord\"\n# " + strings.Repeat("x", i) + "\n"
			res, _ := do(t, ts, http.MethodPut, s.Token(), map[string]any{"version": p.Version, "raw": raw})
			codes[i] = res.StatusCode
		}()
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
		default:
			t.Fatalf("codes %v", codes)
		}
	}
	if ok != 1 {
		t.Fatalf("%d saves landed from one version: %v", ok, codes)
	}
}

func TestSaveRawKeepsComments(t *testing.T) {
	home(t)
	s, ts := newServer(t)
	p := get(t, s, ts)
	raw := "# mine\n[ui]\ntheme = \"nord\" # the cold one\n"
	res, data := do(t, ts, http.MethodPut, s.Token(), map[string]any{"version": p.Version, "raw": raw})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT: %d %s", res.StatusCode, data)
	}
	got, _ := os.ReadFile(config.File())
	if string(got) != raw {
		t.Fatalf("file %q, want %q", got, raw)
	}
	if after := get(t, s, ts); after.Config["ui"].(map[string]any)["theme"] != "nord" || after.Raw != raw {
		t.Fatalf("after %+v", after.Config["ui"])
	}
}

// What conch can't read is refused, and the file left as it was.
func TestSaveRefusesWhatConchCannotRead(t *testing.T) {
	home(t)
	s, ts := newServer(t)
	if err := os.WriteFile(config.File(), []byte("[ui]\ntheme = \"nord\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := get(t, s, ts)
	cases := []struct {
		name string
		body any
		want int
		says string
	}{
		{"broken TOML", map[string]any{"version": p.Version, "raw": "[ui\ntheme ="}, http.StatusUnprocessableEntity, "TOML"},
		{"a wrong type in the raw text", map[string]any{"version": p.Version, "raw": "[web]\nport = \"x\"\n"}, http.StatusUnprocessableEntity, "port"},
		{"a wrong type in the form", map[string]any{"version": p.Version, "config": map[string]any{"web": map[string]any{"port": "abc"}}}, http.StatusUnprocessableEntity, "port"},
		{"a fraction for a whole number", map[string]any{"version": p.Version, "config": map[string]any{"web": map[string]any{"port": 1.5}}}, http.StatusUnprocessableEntity, ""},
		{"a table that is a string", map[string]any{"version": p.Version, "config": map[string]any{"ui": "nord"}}, http.StatusUnprocessableEntity, ""},
		{"a form that is null", `{"version":"x","config":null}`, http.StatusUnprocessableEntity, "no tables"},
		{"a form that is a list", `{"version":"x","config":[1]}`, http.StatusUnprocessableEntity, ""},
		{"nothing at all", map[string]any{"version": p.Version}, http.StatusUnprocessableEntity, "nothing"},
		{"not JSON", "{", http.StatusBadRequest, ""},
		{"truncated JSON", `{"version":"` + p.Version + `","raw":"[ui]`, http.StatusBadRequest, ""},
		{"too much", `{"version":"x","raw":"` + strings.Repeat("a", maxBody+10) + `"}`, http.StatusBadRequest, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, data := do(t, ts, http.MethodPut, s.Token(), c.body)
			if res.StatusCode != c.want {
				t.Fatalf("%d %s, want %d", res.StatusCode, data, c.want)
			}
			if c.says != "" && !strings.Contains(string(data), c.says) {
				t.Fatalf("%s doesn't say %q", data, c.says)
			}
			got, _ := os.ReadFile(config.File())
			if string(got) != "[ui]\ntheme = \"nord\"\n" {
				t.Fatalf("the file changed: %q", got)
			}
		})
	}
}

// A file conch cannot read is shown as the defaults, with what is wrong;
// saving the form over it keeps it aside, as the settings screen does.
func TestUnreadableFile(t *testing.T) {
	home(t)
	s, ts := newServer(t)
	bad := "[ui\ntheme = nord"
	if err := os.WriteFile(config.File(), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	p := get(t, s, ts)
	if len(p.Problems) == 0 || !strings.Contains(p.Problems[0], "can't be read") || p.Raw != bad {
		t.Fatalf("problems %v raw %q", p.Problems, p.Raw)
	}
	if p.Config["ui"].(map[string]any)["theme"] != "conch" {
		t.Fatalf("not the defaults: %v", p.Config["ui"])
	}
	res, data := do(t, ts, http.MethodPut, s.Token(), map[string]any{"version": p.Version, "config": p.Config})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT: %d %s", res.StatusCode, data)
	}
	kept, err := os.ReadFile(config.File() + ".invalid")
	if err != nil || string(kept) != bad {
		t.Fatalf("the unreadable file wasn't kept: %q %v", kept, err)
	}
}

func TestProblemsAreSaid(t *testing.T) {
	home(t)
	s, ts := newServer(t)
	raw := "[[actions]]\nname = \"x\"\n\n[[permissions]]\ntool = \"Bash\"\naction = \"maybe\"\n"
	if err := os.WriteFile(config.File(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	p := get(t, s, ts)
	joined := strings.Join(p.Problems, "\n")
	if !strings.Contains(joined, "permission rule 1") || len(p.Problems) < 2 {
		t.Fatalf("problems %v", p.Problems)
	}
}

func TestMethods(t *testing.T) {
	home(t)
	s, ts := newServer(t)
	for _, m := range []string{http.MethodPost, http.MethodDelete, http.MethodPatch} {
		res, _ := do(t, ts, m, s.Token(), nil)
		if res.StatusCode != http.StatusMethodNotAllowed || res.Header.Get("Allow") == "" {
			t.Errorf("%s: %d", m, res.StatusCode)
		}
	}
}

func TestThePageIsServed(t *testing.T) {
	home(t)
	_, ts := newServer(t)
	res, err := ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `id="root"`) {
		t.Fatalf("index: %d %.200s", res.StatusCode, body)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP %q", csp)
	}
	// Every asset the page names is in the build: a stale dist/ would
	// name a file that is not there.
	for _, ref := range assetRefs(string(body)) {
		res, err := ts.Client().Get(ts.URL + "/" + strings.TrimPrefix(ref, "./"))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", ref, res.StatusCode)
		}
	}
	if len(assetRefs(string(body))) < 2 {
		t.Fatalf("the page names no script and stylesheet: is dist/ the placeholder? %s", body)
	}
}

func assetRefs(html string) []string {
	var out []string
	for _, attr := range []string{`src="`, `href="`} {
		rest := html
		for {
			i := strings.Index(rest, attr)
			if i < 0 {
				break
			}
			rest = rest[i+len(attr):]
			j := strings.IndexByte(rest, '"')
			if strings.HasPrefix(rest[:j], "./") {
				out = append(out, rest[:j])
			}
			rest = rest[j:]
		}
	}
	return out
}

func TestListenAndClose(t *testing.T) {
	home(t)
	s, _ := New(Choices{})
	run, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(run.URL, "http://127.0.0.1:") || !strings.HasSuffix(run.URL, "/#t="+s.Token()) {
		t.Fatalf("URL %q", run.URL)
	}
	base := strings.TrimSuffix(run.URL, "#t="+s.Token())
	res, err := http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := http.Get(base); err == nil {
		t.Fatal("still answering after Close")
	}
}

func TestTOMLValues(t *testing.T) {
	cases := []struct {
		in   string
		want any
		err  bool
	}{
		{`1`, int64(1), false},
		{`-3`, int64(-3), false},
		{`1.5`, 1.5, false},
		{`1e400`, nil, true},
		{`99999999999999999999`, 1e20, false},
		{`"x"`, "x", false},
		{`[1,null,2]`, []any{int64(1), int64(2)}, false},
		{`{"a":null,"b":{"c":true}}`, map[string]any{"b": map[string]any{"c": true}}, false},
	}
	for _, c := range cases {
		dec := json.NewDecoder(strings.NewReader(c.in))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
		got, err := tomlValues(v)
		if (err != nil) != c.err {
			t.Errorf("%s: err %v", c.in, err)
			continue
		}
		if !c.err && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %#v, want %#v", c.in, got, c.want)
		}
	}
}

// Every key config.toml can hold has a field on the page: a setting added
// to config.Config and not here would be one the page silently drops —
// it isn't shown, so it can't be changed, though Save keeps its value.
func TestSchemaCoversConfig(t *testing.T) {
	schema := Schema(Choices{})
	have := map[string]bool{}
	for _, s := range schema {
		for _, f := range s.Fields {
			key := f.Key
			if s.Key != "" && f.Key != "" {
				key = s.Key + "." + f.Key
			} else if s.Key != "" {
				key = s.Key
			}
			have[key] = true
			for _, sub := range f.Fields {
				have[key+"[]."+sub.Key] = true
			}
		}
	}
	var missing []string
	ct := reflect.TypeOf(config.Config{})
	for i := 0; i < ct.NumField(); i++ {
		f := ct.Field(i)
		table := tomlName(f)
		ft := f.Type
		switch {
		case ft.Kind() == reflect.Struct:
			for j := 0; j < ft.NumField(); j++ {
				if k := table + "." + tomlName(ft.Field(j)); !have[k] {
					missing = append(missing, k)
				}
			}
		case ft.Kind() == reflect.Slice || ft.Kind() == reflect.Map:
			elem := ft.Elem()
			if !have[table] {
				missing = append(missing, table)
			}
			for j := 0; j < elem.NumField(); j++ {
				if k := table + "[]." + tomlName(elem.Field(j)); !have[k] {
					missing = append(missing, k)
				}
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("config.toml keys the page has no field for: %v", missing)
	}
	// And nothing on the page names a key that isn't there.
	ids := map[string]bool{}
	for _, s := range schema {
		if ids[s.ID] {
			t.Errorf("section %q twice", s.ID)
		}
		ids[s.ID] = true
		if s.Title == "" || s.Icon == "" || len(s.Fields) == 0 {
			t.Errorf("section %q is missing its title, icon or fields", s.ID)
		}
	}
}

func tomlName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
	return name
}

// Every value the schema offers in a select is one conch takes, and the
// defaults the page shows are conch's own.
func TestSchemaDefaultsMatchConfig(t *testing.T) {
	for _, s := range Schema(Choices{}) {
		for _, f := range s.Fields {
			if f.Kind == KindSelect && len(f.Options) == 0 {
				t.Errorf("%s.%s offers nothing", s.Key, f.Key)
			}
			for _, sub := range f.Fields {
				if sub.Key == "idle_stop" && sub.Default != config.IdleStopDefault {
					t.Errorf("idle_stop default %v", sub.Default)
				}
				if (sub.Key == "restore_running" || sub.Key == "keep") && sub.Default != true {
					t.Errorf("%s default %v", sub.Key, sub.Default)
				}
			}
		}
	}
	// The defaults read back through the form unchanged.
	m, err := toMap(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(m)
	cfg, err := fromJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, config.Default()) {
		t.Fatalf("defaults through the form:\n%+v\nwant\n%+v", cfg, config.Default())
	}
}

// The page's logic, tested with node as the phone's is; skipped where
// there is no node, as on a machine that only builds conch.
func TestUILogic(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	out, err := exec.Command(node, "--test", filepath.Join("app", "src", "lib", "model.test.mjs")).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// What is embedded is a build of app/, not the placeholder a checkout
// without one would have.
func TestDistIsBuilt(t *testing.T) {
	idx, err := dist.ReadFile("dist/index.html")
	if err != nil || !strings.Contains(string(idx), "assets/") {
		t.Fatalf("dist/index.html isn't a build: %v %.100s", err, idx)
	}
}

// A number left unset is not shown as a 0 nobody typed; one somebody set
// to 0 on purpose (idle_stop: never) is.
func TestUnsetNumbersAreLeftOut(t *testing.T) {
	home(t)
	s, ts := newServer(t)
	raw := "[sandbox.daytona]\ntarget = \"eu\"\nidle_stop = 0\n\n[[actions]]\nname = \"a\"\nrun = \"b\"\n"
	if err := os.WriteFile(config.File(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	p := get(t, s, ts)
	web, _ := p.Config["web"].(map[string]any)
	if _, ok := web["port"]; ok {
		t.Fatalf("web %v", web)
	}
	d := p.Config["sandbox"].(map[string]any)["daytona"].(map[string]any)
	for _, k := range []string{"auto_stop", "price_cpu_hour", "price_gib_hour", "price_disk_gib_hour"} {
		if _, ok := d[k]; ok {
			t.Errorf("%s shown as %v", k, d[k])
		}
	}
	if d["idle_stop"] != float64(0) {
		t.Errorf("idle_stop %v, want the 0 that was set", d["idle_stop"])
	}
	// Numbers that are not omitempty keep their 0.
	if p.Config["remote"].(map[string]any)["upload_max_mb"] == nil {
		t.Error("upload_max_mb went missing")
	}
}
