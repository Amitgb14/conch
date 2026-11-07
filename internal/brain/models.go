package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// Model is one model a provider offers, for Settings → Brain to list.
type Model struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Small marks one suited to summaries: fast and cheap.
	Small bool `json:"small,omitempty"`
}

// ClaudeModels are what the Claude Code CLI takes for --model: its aliases,
// which follow the newest of each, then the models by name. The CLI has no
// way to list them, so this is the list; any other name still works,
// typed.
var ClaudeModels = []Model{
	{ID: "opus", Label: "Opus (the newest)"},
	{ID: "sonnet", Label: "Sonnet (the newest)"},
	{ID: "haiku", Label: "Haiku (the newest)", Small: true},
	{ID: "claude-fable-5-1", Label: "Claude Fable 5.1"},
	{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"},
	{ID: "claude-sonnet-5-5", Label: "Claude Sonnet 5.5"},
	{ID: "claude-haiku-5-5", Label: "Claude Haiku 5.5", Small: true},
}

// anthropicKnown is the Anthropic API's models by name, for when the API
// can't be asked (no key yet).
var anthropicKnown = []Model{
	{ID: "claude-fable-5-1", Label: "Claude Fable 5.1"},
	{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"},
	{ID: "claude-sonnet-5-5", Label: "Claude Sonnet 5.5"},
	{ID: "claude-haiku-5-5", Label: "Claude Haiku 5.5", Small: true},
}

// modelsTimeout bounds asking an endpoint for its models: Settings is
// waiting on it, and a list typed by hand still works.
const modelsTimeout = 8 * time.Second

// Models lists what the provider in cfg offers. The Claude Code CLI's are
// known; the Anthropic API and an OpenAI-compatible endpoint (Ollama, LM
// Studio, OpenAI) are asked. When asking fails the error says why, and
// the models returned are the ones known without asking, if any, so the
// list is never empty for want of a key.
func Models(ctx context.Context, cfg config.BrainCfg) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()
	switch cfg.Provider {
	case "", "claude":
		return append([]Model(nil), ClaudeModels...), nil
	case "anthropic":
		env, key := apiKey(cfg, "ANTHROPIC_API_KEY")
		if key == "" {
			return append([]Model(nil), anthropicKnown...), fmt.Errorf("set $%s to list the API's own models", env)
		}
		base := strings.TrimRight(firstNonEmpty(cfg.BaseURL, "https://api.anthropic.com"), "/")
		var out struct {
			Data []struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
		}
		err := getJSON(ctx, base+"/v1/models?limit=100", map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"}, &out)
		if err != nil {
			return append([]Model(nil), anthropicKnown...), fmt.Errorf("anthropic: %w", err)
		}
		var ms []Model
		for _, d := range out.Data {
			ms = append(ms, Model{ID: d.ID, Label: firstNonEmpty(d.DisplayName, d.ID), Small: strings.Contains(d.ID, "haiku")})
		}
		return ms, nil
	case "openai":
		env, key := apiKey(cfg, "OPENAI_API_KEY")
		o := openAI{cfg: cfg}
		if key == "" && !o.local() && cfg.BaseURL == "" {
			return nil, fmt.Errorf("set $%s, or an API endpoint such as Ollama's, to list its models", env)
		}
		base := strings.TrimRight(firstNonEmpty(cfg.BaseURL, "https://api.openai.com/v1"), "/")
		headers := map[string]string{}
		if key != "" {
			headers["Authorization"] = "Bearer " + key
		}
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := getJSON(ctx, base+"/models", headers, &out); err != nil {
			return nil, fmt.Errorf("openai: %w", err)
		}
		var ms []Model
		for _, d := range out.Data {
			ms = append(ms, Model{ID: d.ID, Label: d.ID})
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
		return ms, nil
	}
	return nil, fmt.Errorf("unknown provider %q", cfg.Provider)
}

// getJSON GETs url and decodes its JSON answer into out.
func getJSON(ctx context.Context, url string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
		return fmt.Errorf("%s: %s", resp.Status, msg)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("not a list of models: %w", err)
	}
	return nil
}
