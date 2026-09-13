package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// ---- Claude Code CLI ----

// claudeCLI runs `claude -p` headless. It uses the user's Claude login, so
// conch handles no credentials, and it runs without tools, MCP servers,
// settings or session history: a plain model call.
type claudeCLI struct {
	cfg config.BrainCfg
}

func newClaudeCLI(cfg config.BrainCfg) *claudeCLI { return &claudeCLI{cfg: cfg} }

func (c *claudeCLI) Name() string { return "claude" }

func (c *claudeCLI) binary() (string, error) {
	if c.cfg.Command != "" {
		return exec.LookPath(c.cfg.Command)
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		if p := filepath.Join(home, ".local", "bin", "claude"); isExecutable(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: Claude Code is not installed (conch agent install claude), or pick another provider in Settings → Brain", ErrNotConfigured)
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0
}

func (c *claudeCLI) Check() error {
	_, err := c.binary()
	return err
}

func (c *claudeCLI) Complete(ctx context.Context, req Request) (Response, error) {
	bin, err := c.binary()
	if err != nil {
		return Response{}, err
	}
	model := c.cfg.Model
	if req.Small {
		model = firstNonEmpty(c.cfg.SummaryModel, "haiku")
	} else if model == "" {
		model = "sonnet"
	}
	args := []string{"-p", "--output-format", "json", "--model", model,
		"--tools", "", "--strict-mcp-config", "--setting-sources", "",
		"--no-session-persistence", "--disable-slash-commands"}
	if req.System != "" {
		args = append(args, "--system-prompt", req.System)
	}
	if req.Schema != nil {
		args = append(args, "--json-schema", schemaJSON(req.Schema))
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(req.Prompt)
	cmd.Dir = os.TempDir() // no project context
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	var out struct {
		Result           string          `json:"result"`
		IsError          bool            `json:"is_error"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		TotalCostUSD     float64         `json:"total_cost_usd"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		msg := firstLine(stderr.Bytes())
		if msg == "" {
			msg = firstLine(stdout.Bytes())
		}
		if runErr != nil && msg == "" {
			msg = runErr.Error()
		}
		return Response{}, fmt.Errorf("claude: %s", msg)
	}
	if out.IsError {
		return Response{}, fmt.Errorf("claude: %s", firstLine([]byte(out.Result)))
	}
	res := Response{Text: out.Result, CostUSD: out.TotalCostUSD}
	if req.Schema != nil {
		if len(out.StructuredOutput) > 0 && string(out.StructuredOutput) != "null" {
			res.JSON = out.StructuredOutput
		} else if res.JSON, err = decodeJSON(out.Result); err != nil {
			return Response{}, err
		}
	}
	return res, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---- Anthropic API ----

type anthropic struct {
	cfg config.BrainCfg
}

func newAnthropic(cfg config.BrainCfg) *anthropic { return &anthropic{cfg: cfg} }

func (a *anthropic) Name() string { return "anthropic" }

func (a *anthropic) Check() error {
	if env, key := apiKey(a.cfg, "ANTHROPIC_API_KEY"); key == "" {
		return fmt.Errorf("%w: set $%s", ErrNotConfigured, env)
	}
	return nil
}

func (a *anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	if err := a.Check(); err != nil {
		return Response{}, err
	}
	_, key := apiKey(a.cfg, "ANTHROPIC_API_KEY")
	model := firstNonEmpty(a.cfg.Model, "claude-sonnet-5")
	if req.Small {
		model = firstNonEmpty(a.cfg.SummaryModel, "claude-haiku-4-5")
	}
	body := map[string]any{
		"model":      model,
		"max_tokens": 4096,
		"messages":   []map[string]any{{"role": "user", "content": req.Prompt}},
	}
	if req.System != "" {
		body["system"] = req.System
	}
	if req.Schema != nil {
		// A forced tool call is the API's structured output.
		body["tools"] = []map[string]any{{"name": "respond", "description": "Return the answer.", "input_schema": req.Schema}}
		body["tool_choice"] = map[string]any{"type": "tool", "name": "respond"}
	}
	base := strings.TrimRight(firstNonEmpty(a.cfg.BaseURL, "https://api.anthropic.com"), "/")
	var out struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	headers := map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"}
	if err := postJSON(ctx, base+"/v1/messages", headers, body, &out); err != nil {
		return Response{}, fmt.Errorf("anthropic: %w", err)
	}
	var res Response
	for _, c := range out.Content {
		switch c.Type {
		case "text":
			res.Text += c.Text
		case "tool_use":
			res.JSON = c.Input
		}
	}
	if req.Schema != nil && res.JSON == nil {
		var err error
		if res.JSON, err = decodeJSON(res.Text); err != nil {
			return Response{}, err
		}
	}
	return res, nil
}

// ---- OpenAI-compatible ----

type openAI struct {
	cfg config.BrainCfg
}

func newOpenAI(cfg config.BrainCfg) *openAI { return &openAI{cfg: cfg} }

func (o *openAI) Name() string { return "openai" }

func (o *openAI) local() bool {
	return strings.Contains(o.cfg.BaseURL, "localhost") || strings.Contains(o.cfg.BaseURL, "127.0.0.1")
}

func (o *openAI) Check() error {
	if o.cfg.Model == "" {
		return fmt.Errorf("%w: set [brain] model in config.toml", ErrNotConfigured)
	}
	if env, key := apiKey(o.cfg, "OPENAI_API_KEY"); key == "" && !o.local() {
		return fmt.Errorf("%w: set $%s (not needed for Ollama)", ErrNotConfigured, env)
	}
	return nil
}

func (o *openAI) Complete(ctx context.Context, req Request) (Response, error) {
	if err := o.Check(); err != nil {
		return Response{}, err
	}
	_, key := apiKey(o.cfg, "OPENAI_API_KEY")
	model := o.cfg.Model
	if req.Small {
		model = firstNonEmpty(o.cfg.SummaryModel, o.cfg.Model)
	}
	msgs := []map[string]any{}
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	msgs = append(msgs, map[string]any{"role": "user", "content": req.Prompt})
	body := map[string]any{"model": model, "messages": msgs}
	if req.Schema != nil {
		body["response_format"] = map[string]any{"type": "json_schema",
			"json_schema": map[string]any{"name": "respond", "schema": req.Schema}}
	}
	base := strings.TrimRight(firstNonEmpty(o.cfg.BaseURL, "https://api.openai.com/v1"), "/")
	headers := map[string]string{}
	if key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := postJSON(ctx, base+"/chat/completions", headers, body, &out); err != nil {
		return Response{}, fmt.Errorf("openai: %w", err)
	}
	if len(out.Choices) == 0 {
		return Response{}, fmt.Errorf("openai: empty response")
	}
	res := Response{Text: out.Choices[0].Message.Content}
	if req.Schema != nil {
		var err error
		if res.JSON, err = decodeJSON(res.Text); err != nil {
			return Response{}, err
		}
	}
	return res, nil
}

func postJSON(ctx context.Context, url string, headers map[string]string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: %s", resp.Status, firstLine(buf.Bytes()))
	}
	return json.Unmarshal(buf.Bytes(), out)
}
