package brain

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

func ids(ms []Model) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return strings.Join(out, ",")
}

// The Claude Code CLI can't be asked: its aliases and names are known.
func TestModelsClaude(t *testing.T) {
	for _, p := range []string{"", "claude"} {
		ms, err := Models(context.Background(), config.BrainCfg{Provider: p})
		if err != nil || !strings.HasPrefix(ids(ms), "opus,sonnet,haiku,") {
			t.Fatalf("%q: %v %s", p, err, ids(ms))
		}
		ms[0].ID = "changed" // a copy: the list itself is not the caller's
	}
	if ClaudeModels[0].ID != "opus" {
		t.Fatal("the caller changed ClaudeModels")
	}
	small := 0
	for _, m := range ClaudeModels {
		if m.Small {
			small++
		}
	}
	if small == 0 {
		t.Fatal("nothing marked for summaries")
	}
}

// The Anthropic API is asked, with its key and version; without a key, or
// when asking fails, the known names come back with why.
func TestModelsAnthropic(t *testing.T) {
	var gotKey, gotVersion, gotPath string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotVersion, gotPath = r.Header.Get("x-api-key"), r.Header.Get("anthropic-version"), r.URL.Path
		if r.Header.Get("x-api-key") == "bad" {
			http.Error(w, `{"error":"invalid x-api-key"}`, http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5"},{"id":"claude-haiku-5-5","display_name":""}]}`))
	}))
	defer api.Close()

	t.Setenv("MY_KEY", "")
	ms, err := Models(context.Background(), config.BrainCfg{Provider: "anthropic", APIKeyEnv: "MY_KEY", BaseURL: api.URL})
	if err == nil || !strings.Contains(err.Error(), "$MY_KEY") || ids(ms) != ids(anthropicKnown) {
		t.Fatalf("no key: %v %s", err, ids(ms))
	}

	t.Setenv("MY_KEY", "sk-test")
	ms, err = Models(context.Background(), config.BrainCfg{Provider: "anthropic", APIKeyEnv: "MY_KEY", BaseURL: api.URL + "/"})
	if err != nil || ids(ms) != "claude-opus-5-5,claude-haiku-5-5" {
		t.Fatalf("listed: %v %s", err, ids(ms))
	}
	if gotKey != "sk-test" || gotVersion == "" || gotPath != "/v1/models" {
		t.Fatalf("asked with key %q version %q at %q", gotKey, gotVersion, gotPath)
	}
	if ms[1].Label != "claude-haiku-5-5" || !ms[1].Small || ms[0].Small {
		t.Fatalf("labels and small: %+v", ms)
	}

	t.Setenv("MY_KEY", "bad")
	ms, err = Models(context.Background(), config.BrainCfg{Provider: "anthropic", APIKeyEnv: "MY_KEY", BaseURL: api.URL})
	if err == nil || !strings.Contains(err.Error(), "401") || ids(ms) != ids(anthropicKnown) {
		t.Fatalf("refused: %v %s", err, ids(ms))
	}
}

// An OpenAI-compatible endpoint is asked at /models, sorted; Ollama needs
// no key, and nothing to ask is said rather than tried.
func TestModelsOpenAI(t *testing.T) {
	var auth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`{"object":"list","data":[{"id":"qwen3:8b"},{"id":"llama3.2:3b"}]}`))
		default:
			w.Write([]byte(`not json`))
		}
	}))
	defer api.Close()

	t.Setenv("OPENAI_API_KEY", "")
	ms, err := Models(context.Background(), config.BrainCfg{Provider: "openai", BaseURL: api.URL + "/v1"})
	if err != nil || ids(ms) != "llama3.2:3b,qwen3:8b" || auth != "" {
		t.Fatalf("ollama: %v %s (auth %q)", err, ids(ms), auth)
	}
	t.Setenv("OPENAI_API_KEY", "sk-o")
	if _, err := Models(context.Background(), config.BrainCfg{Provider: "openai", BaseURL: api.URL + "/v1"}); err != nil || auth != "Bearer sk-o" {
		t.Fatalf("with a key: %v (auth %q)", err, auth)
	}
	if _, err := Models(context.Background(), config.BrainCfg{Provider: "openai", BaseURL: api.URL + "/other"}); err == nil || !strings.Contains(err.Error(), "not a list of models") {
		t.Fatalf("a page that is not a list: %v", err)
	}
	t.Setenv("OPENAI_API_KEY", "")
	if ms, err := Models(context.Background(), config.BrainCfg{Provider: "openai"}); err == nil || len(ms) != 0 || !strings.Contains(err.Error(), "$OPENAI_API_KEY") {
		t.Fatalf("nothing to ask: %v %s", err, ids(ms))
	}
	if _, err := Models(context.Background(), config.BrainCfg{Provider: "nope"}); err == nil {
		t.Fatal("an unknown provider listed models")
	}
}

// A cancelled request says so rather than hanging Settings.
func TestModelsCancelled(t *testing.T) {
	block := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer api.Close()
	defer close(block)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := Models(ctx, config.BrainCfg{Provider: "openai", BaseURL: api.URL}); err == nil {
		t.Fatal("a cancelled request listed models")
	}
}
