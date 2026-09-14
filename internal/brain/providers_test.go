package brain

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

// a6Script writes an executable shell script named name into a new temp
// directory and returns the directory. Scripts use absolute tool paths so
// they work with a PATH that holds only the temp directory.
func a6Script(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// a6Isolate points PATH and HOME at empty temp dirs so no real claude binary
// can ever be found.
func a6Isolate(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	return home
}

func TestA6ProviderNamesAndLabels(t *testing.T) {
	for _, name := range Providers {
		p, err := New(config.BrainCfg{Provider: name})
		if err != nil {
			t.Fatal(err)
		}
		if p.Name() != name {
			t.Fatalf("Name() = %q, want %q", p.Name(), name)
		}
		if ProviderLabel(name) == name {
			t.Fatalf("no label for %s", name)
		}
	}
	if p, _ := New(config.BrainCfg{}); p.Name() != "claude" {
		t.Fatalf("default provider: %s", p.Name())
	}
	if ProviderLabel("mystery") != "mystery" {
		t.Fatal("unknown label should echo the name")
	}
	if _, err := New(config.BrainCfg{Provider: "x"}); err == nil || !strings.Contains(err.Error(), "claude, anthropic, openai") {
		t.Fatalf("unknown provider error: %v", err)
	}
}

func TestA6ClaudeBinaryLookup(t *testing.T) {
	home := a6Isolate(t)
	p := newClaudeCLI(config.BrainCfg{})
	if err := p.Check(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("no claude anywhere: %v", err)
	}
	if _, err := p.Complete(context.Background(), Request{Prompt: "x"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Complete without binary: %v", err)
	}

	// A non-executable file in ~/.local/bin is ignored.
	local := filepath.Join(home, ".local", "bin")
	os.MkdirAll(local, 0o755)
	os.WriteFile(filepath.Join(local, "claude"), []byte("#!/bin/sh\n"), 0o644)
	if p.Check() == nil {
		t.Fatal("non-executable ~/.local/bin/claude accepted")
	}
	// A directory is not executable either.
	os.Remove(filepath.Join(local, "claude"))
	os.Mkdir(filepath.Join(local, "claude"), 0o755)
	if p.Check() == nil {
		t.Fatal("directory accepted as claude")
	}
	os.Remove(filepath.Join(local, "claude"))
	os.WriteFile(filepath.Join(local, "claude"), []byte("#!/bin/sh\necho '{\"result\":\"home\"}'\n"), 0o755)
	if bin, err := p.binary(); err != nil || bin != filepath.Join(local, "claude") {
		t.Fatalf("home fallback: %q %v", bin, err)
	}

	// PATH wins over the home fallback.
	dir := a6Script(t, "claude", "echo '{\"result\":\"path\"}'\n")
	t.Setenv("PATH", dir)
	res, err := p.Complete(context.Background(), Request{Prompt: "x"})
	if err != nil || res.Text != "path" {
		t.Fatalf("PATH lookup: %+v %v", res, err)
	}
	// Command is looked up on PATH too.
	named := newClaudeCLI(config.BrainCfg{Command: "claude"})
	if err := named.Check(); err != nil {
		t.Fatalf("Command on PATH: %v", err)
	}
}

func TestA6ClaudeArgsAndModels(t *testing.T) {
	a6Isolate(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	bin := filepath.Join(a6Script(t, "claude", "for a in \"$@\"; do printf '%s\\n' \"$a\"; done > "+log+"\n/bin/cat >/dev/null\necho '{\"result\":\"ok\",\"total_cost_usd\":0.5}'\n"), "claude")

	read := func() []string {
		b, _ := os.ReadFile(log)
		return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	}
	after := func(args []string, flag string) (string, bool) {
		for i, a := range args {
			if a == flag && i+1 < len(args) {
				return args[i+1], true
			}
		}
		return "", false
	}

	p := newClaudeCLI(config.BrainCfg{Command: bin, Model: "opus", SummaryModel: "sonnet-small"})
	res, err := p.Complete(context.Background(), Request{Prompt: "hi", System: "be brief"})
	if err != nil || res.Text != "ok" || res.CostUSD != 0.5 || res.JSON != nil {
		t.Fatalf("complete: %+v %v", res, err)
	}
	args := read()
	if m, _ := after(args, "--model"); m != "opus" {
		t.Fatalf("model: %v", args)
	}
	if s, ok := after(args, "--system-prompt"); !ok || s != "be brief" {
		t.Fatalf("system prompt: %v", args)
	}
	if _, ok := after(args, "--json-schema"); ok {
		t.Fatalf("schema without Schema: %v", args)
	}

	if _, err := p.Complete(context.Background(), Request{Prompt: "hi", Small: true}); err != nil {
		t.Fatal(err)
	}
	args = read()
	if m, _ := after(args, "--model"); m != "sonnet-small" {
		t.Fatalf("summary model: %v", args)
	}
	for _, a := range args {
		if a == "--system-prompt" {
			t.Fatalf("empty system prompt passed: %v", args)
		}
	}
}

func TestA6ClaudeOutputErrors(t *testing.T) {
	a6Isolate(t)
	cases := []struct {
		name, body string
		schema     bool
		want       string
	}{
		{"stderr wins", "echo 'not json'\necho 'auth failed\nsecond line' >&2\nexit 1\n", false, "claude: auth failed"},
		{"stdout when no stderr", "echo 'garbage output'\n", false, "claude: garbage output"},
		{"exit status when silent", "exit 3\n", false, "exit status 3"},
		{"structured null falls back to text", `echo '{"result":"no json here","structured_output":null}'` + "\n", true, "did not return valid JSON"},
		{"is_error", "/bin/cat <<'EOF'\n" + `{"is_error":true,"result":"rate limited\nmore"}` + "\nEOF\n", false, "claude: rate limited"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin := filepath.Join(a6Script(t, "claude", "/bin/cat >/dev/null\n"+tc.body), "claude")
			p := newClaudeCLI(config.BrainCfg{Command: bin})
			req := Request{Prompt: "x"}
			if tc.schema {
				req.Schema = summarySchema
			}
			_, err := p.Complete(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "second line") || strings.Contains(err.Error(), "more") {
				t.Fatalf("error should keep only the first line: %v", err)
			}
		})
	}
}

func TestA6ClaudeStructuredOutputPreferred(t *testing.T) {
	a6Isolate(t)
	bin := filepath.Join(a6Script(t, "claude", "/bin/cat >/dev/null\n"+`echo '{"result":"{\"doing\":\"text\"}","structured_output":{"doing":"structured","needs":""}}'`+"\n"), "claude")
	p := newClaudeCLI(config.BrainCfg{Command: bin})
	res, err := p.Complete(context.Background(), Request{Prompt: "x", Schema: summarySchema})
	if err != nil || !strings.Contains(string(res.JSON), "structured") {
		t.Fatalf("res %s err %v", res.JSON, err)
	}
}

func TestA6ClaudeContextCanceled(t *testing.T) {
	a6Isolate(t)
	bin := filepath.Join(a6Script(t, "claude", "exec /bin/sleep 30\n"), "claude")
	p := newClaudeCLI(config.BrainCfg{Command: bin})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Complete(ctx, Request{Prompt: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

// a6API is a fake Anthropic/OpenAI endpoint that records requests.
type a6API struct {
	mu      sync.Mutex
	body    map[string]any
	headers http.Header
	path    string
}

func (a *a6API) server(t *testing.T, status int, reply string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		a.mu.Lock()
		a.body, a.headers, a.path = body, r.Header.Clone(), r.URL.Path
		a.mu.Unlock()
		w.WriteHeader(status)
		w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestA6AnthropicRequestShapes(t *testing.T) {
	var api a6API
	srv := api.server(t, 200, `{"content":[{"type":"text","text":"Here: "},{"type":"text","text":"{\"doing\":\"x\",\"needs\":\"\"}"}]}`)
	t.Setenv("MY_KEY", "secret")
	p := newAnthropic(config.BrainCfg{BaseURL: srv.URL + "/", APIKeyEnv: "MY_KEY"})
	res, err := p.Complete(context.Background(), Request{Prompt: "p", System: "sys", Schema: summarySchema})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != `Here: {"doing":"x","needs":""}` || string(res.JSON) != `{"doing":"x","needs":""}` {
		t.Fatalf("text fallback: %+v", res)
	}
	if api.path != "/v1/messages" || api.headers.Get("x-api-key") != "secret" || api.headers.Get("anthropic-version") == "" || api.headers.Get("Content-Type") != "application/json" {
		t.Fatalf("request: %s %v", api.path, api.headers)
	}
	if api.body["model"] != "claude-sonnet-5" || api.body["system"] != "sys" || api.body["tools"] == nil {
		t.Fatalf("body: %v", api.body)
	}

	// Without a schema: no tools, no system, configured summary model.
	p = newAnthropic(config.BrainCfg{BaseURL: srv.URL, APIKeyEnv: "MY_KEY", Model: "big", SummaryModel: "small"})
	if _, err := p.Complete(context.Background(), Request{Prompt: "p", Small: true}); err != nil {
		t.Fatal(err)
	}
	if api.body["model"] != "small" || api.body["tools"] != nil || api.body["system"] != nil {
		t.Fatalf("plain body: %v", api.body)
	}
	if _, err := p.Complete(context.Background(), Request{Prompt: "p"}); err != nil || api.body["model"] != "big" {
		t.Fatalf("configured model: %v %v", err, api.body)
	}

	// A custom key env that is unset names that env in the error.
	t.Setenv("MY_KEY", "")
	if err := p.Check(); err == nil || !strings.Contains(err.Error(), "$MY_KEY") || !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("check: %v", err)
	}
	if _, err := p.Complete(context.Background(), Request{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("complete without key: %v", err)
	}
}

func TestA6AnthropicErrors(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "k")
	var api a6API
	bad := api.server(t, 529, "Overloaded\nretry later")
	p := newAnthropic(config.BrainCfg{BaseURL: bad.URL})
	_, err := p.Complete(context.Background(), Request{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "anthropic: 529") || !strings.Contains(err.Error(), "Overloaded") || strings.Contains(err.Error(), "retry later") {
		t.Fatalf("status error: %v", err)
	}

	garbage := api.server(t, 200, "not json")
	p = newAnthropic(config.BrainCfg{BaseURL: garbage.URL})
	if _, err := p.Complete(context.Background(), Request{Prompt: "x"}); err == nil || !strings.HasPrefix(err.Error(), "anthropic:") {
		t.Fatalf("garbage body: %v", err)
	}

	noJSON := api.server(t, 200, `{"content":[{"type":"text","text":"sorry, I can't"}]}`)
	p = newAnthropic(config.BrainCfg{BaseURL: noJSON.URL})
	if _, err := p.Complete(context.Background(), Request{Prompt: "x", Schema: summarySchema}); err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("schema without JSON: %v", err)
	}

	// A closed port: connection errors surface.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	p = newAnthropic(config.BrainCfg{BaseURL: "http://" + addr})
	if _, err := p.Complete(context.Background(), Request{Prompt: "x"}); err == nil {
		t.Fatal("closed port should fail")
	}
	// An unparsable URL fails building the request.
	p = newAnthropic(config.BrainCfg{BaseURL: "http://bad host\x7f"})
	if _, err := p.Complete(context.Background(), Request{Prompt: "x"}); err == nil {
		t.Fatal("bad URL should fail")
	}
}

func TestA6OpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if err := newOpenAI(config.BrainCfg{}).Check(); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("no model: %v", err)
	}
	remote := newOpenAI(config.BrainCfg{Model: "gpt", BaseURL: "https://example.invalid/v1"})
	if err := remote.Check(); err == nil || !strings.Contains(err.Error(), "$OPENAI_API_KEY") {
		t.Fatalf("remote without key: %v", err)
	}
	if _, err := remote.Complete(context.Background(), Request{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("complete remote without key: %v", err)
	}
	if err := newOpenAI(config.BrainCfg{Model: "m", BaseURL: "http://localhost:11434/v1"}).Check(); err != nil {
		t.Fatalf("localhost needs no key: %v", err)
	}

	var api a6API
	srv := api.server(t, 200, `{"choices":[{"message":{"content":"plain answer"}}]}`)
	t.Setenv("OPENAI_API_KEY", "sk-1")
	p := newOpenAI(config.BrainCfg{Model: "big", SummaryModel: "small", BaseURL: srv.URL + "/v1/"})
	res, err := p.Complete(context.Background(), Request{Prompt: "p", System: "sys", Small: true})
	if err != nil || res.Text != "plain answer" || res.JSON != nil {
		t.Fatalf("res %+v %v", res, err)
	}
	if api.path != "/v1/chat/completions" || api.headers.Get("Authorization") != "Bearer sk-1" {
		t.Fatalf("request: %s %v", api.path, api.headers)
	}
	msgs, _ := api.body["messages"].([]any)
	if api.body["model"] != "small" || len(msgs) != 2 || api.body["response_format"] != nil {
		t.Fatalf("body: %v", api.body)
	}
	if _, err := p.Complete(context.Background(), Request{Prompt: "p", Schema: summarySchema}); err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("schema with prose: %v", err)
	}
	if api.body["model"] != "big" || api.body["response_format"] == nil {
		t.Fatalf("schema body: %v", api.body)
	}
	msgs, _ = api.body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("no system message expected: %v", msgs)
	}

	// Local servers get no Authorization header without a key.
	t.Setenv("OPENAI_API_KEY", "")
	local := newOpenAI(config.BrainCfg{Model: "m", BaseURL: srv.URL})
	if _, err := local.Complete(context.Background(), Request{Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if api.headers.Get("Authorization") != "" {
		t.Fatalf("unexpected auth header: %v", api.headers)
	}

	empty := api.server(t, 200, `{"choices":[]}`)
	if _, err := newOpenAI(config.BrainCfg{Model: "m", BaseURL: empty.URL}).Complete(context.Background(), Request{}); err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("empty choices: %v", err)
	}
	failing := api.server(t, 500, `{"error":"down"}`)
	if _, err := newOpenAI(config.BrainCfg{Model: "m", BaseURL: failing.URL}).Complete(context.Background(), Request{}); err == nil || !strings.Contains(err.Error(), "openai: 500") {
		t.Fatalf("500: %v", err)
	}
}

func TestA6PostJSONUnmarshalableBody(t *testing.T) {
	if err := postJSON(context.Background(), "http://127.0.0.1:1", nil, map[string]any{"c": make(chan int)}, nil); err == nil {
		t.Fatal("unencodable body accepted")
	}
}

func TestA6DecodeJSONAndFirstLine(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{`{"a":1}`, `{"a":1}`, true},
		{"prose before {\"a\":{\"b\":2}} and after", `{"a":{"b":2}}`, true},
		{"```json\n{\"a\":1}\n```", `{"a":1}`, true},
		{"no braces", "", false},
		{"} backwards {", "", false},
		{"[1,2]", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, err := decodeJSON(c.in)
		if (err == nil) != c.ok || (c.ok && string(got) != c.want) {
			t.Errorf("decodeJSON(%q) = %s, %v", c.in, got, err)
		}
	}
	long := strings.Repeat("x", 400)
	if got := firstLine([]byte("  " + long + "\nnext")); len(got) != 300+len("…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("firstLine long: %d", len(got))
	}
	if firstLine([]byte("\n\n  a\nb")) != "a" {
		t.Fatal("firstLine should trim leading blank lines")
	}
	if firstNonEmpty() != "" || firstNonEmpty("", "") != "" || firstNonEmpty("", "b", "c") != "b" {
		t.Fatal("firstNonEmpty")
	}
	if schemaJSON(map[string]any{"html": "<a&b>"}) != `{"html":"<a&b>"}` {
		t.Fatalf("schemaJSON escapes HTML: %s", schemaJSON(map[string]any{"html": "<a&b>"}))
	}
}
