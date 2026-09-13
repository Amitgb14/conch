// Package brain is conch's model-backed intelligence: it turns requests
// typed in the command bar into plans of conch actions, and summarises what
// agents are doing. The model sits behind a Provider, so Claude is the
// default but any backend can be plugged in.
package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
)

// Request is one model call. With Schema set, the provider must return a
// JSON object matching it in Response.JSON.
type Request struct {
	System string
	Prompt string
	Schema map[string]any
	// Small asks for the provider's fast, cheap model (summaries).
	Small bool
}

// Response is a model's answer.
type Response struct {
	Text string
	JSON json.RawMessage
	// CostUSD is what the call cost when the provider reports it.
	CostUSD float64
}

// Provider is a model backend.
type Provider interface {
	// Name identifies the provider, e.g. "claude".
	Name() string
	// Check reports why the provider can't be used (not installed, no key).
	Check() error
	Complete(ctx context.Context, req Request) (Response, error)
}

// Providers lists the built-in provider names.
var Providers = []string{"claude", "anthropic", "openai"}

// ProviderLabel describes a provider for settings.
func ProviderLabel(name string) string {
	switch name {
	case "claude":
		return "Claude Code CLI (your Claude login)"
	case "anthropic":
		return "Anthropic API (ANTHROPIC_API_KEY)"
	case "openai":
		return "OpenAI-compatible API (OpenAI, Ollama, LM Studio)"
	}
	return name
}

// New builds the configured provider.
func New(cfg config.BrainCfg) (Provider, error) {
	switch cfg.Provider {
	case "", "claude":
		return newClaudeCLI(cfg), nil
	case "anthropic":
		return newAnthropic(cfg), nil
	case "openai":
		return newOpenAI(cfg), nil
	}
	return nil, fmt.Errorf("unknown brain provider %q (want one of %s)", cfg.Provider, strings.Join(Providers, ", "))
}

// ErrNotConfigured means the provider lacks what it needs to run.
var ErrNotConfigured = errors.New("brain not configured")

func apiKey(cfg config.BrainCfg, fallback string) (env, key string) {
	env = cfg.APIKeyEnv
	if env == "" {
		env = fallback
	}
	return env, os.Getenv(env)
}

// decodeJSON extracts a JSON object from model text, tolerating code fences
// and prose around it.
func decodeJSON(text string) (json.RawMessage, error) {
	t := strings.TrimSpace(text)
	if i := strings.Index(t, "{"); i >= 0 {
		if j := strings.LastIndex(t, "}"); j > i {
			t = t[i : j+1]
		}
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(t), &probe); err != nil {
		return nil, fmt.Errorf("the model did not return valid JSON: %w", err)
	}
	return json.RawMessage(t), nil
}

func schemaJSON(schema map[string]any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(schema)
	return strings.TrimSpace(b.String())
}

// firstLine trims an error body for display.
func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
