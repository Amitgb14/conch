package agentsetup

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestNeutralAndRefFor(t *testing.T) {
	for in, want := range map[string]string{
		"":                      "",
		"plain":                 "plain",
		"${A}":                  "${A}",
		"$A":                    "${A}",
		"${A:-fallback}":        "${A}",
		"{env:A}":               "${A}",
		"${env:A}":              "${A}",
		"Bearer {env:TOKEN}":    "Bearer ${TOKEN}",
		"--url=$HOST:${PORT}/x": "--url=${HOST}:${PORT}/x",
		"${1BAD} and $ alone":   "${1BAD} and $ alone",
		"${env:A}-{env:B}-${C}": "${A}-${B}-${C}",
	} {
		if got := neutral(in); got != want {
			t.Errorf("neutral(%q) = %q, want %q", in, got, want)
		}
	}
	for _, c := range []struct{ agent, in, want string }{
		{"claude", "Bearer ${T}", "Bearer ${T}"},
		{"gemini", "${T}", "${T}"},
		{"opencode", "Bearer ${T}", "Bearer {env:T}"},
		{"devin", "${A}:${B}", "${env:A}:${env:B}"},
		{"devin", "no reference", "no reference"},
	} {
		if got := refFor(c.agent, c.in); got != c.want {
			t.Errorf("refFor(%s, %q) = %q, want %q", c.agent, c.in, got, c.want)
		}
	}
	// Written in an agent's form and read back, it is the same reference.
	for _, a := range []string{"claude", "gemini", "opencode", "devin"} {
		if got := neutral(refFor(a, "Bearer ${T}")); got != "Bearer ${T}" {
			t.Errorf("%s round trip: %q", a, got)
		}
	}
	if refsFor("devin", nil) != nil {
		t.Error("refsFor(nil)")
	}
}

func TestSafeValue(t *testing.T) {
	for v, want := range map[string]bool{
		"":                  true,
		"   ":               true,
		"${TOKEN}":          true,
		"$TOKEN":            true,
		"{env:TOKEN}":       true,
		"${env:TOKEN}":      true,
		"Bearer ${TOKEN}":   true,
		" Bearer {env:T} ":  true,
		"Bearer abc":        false,
		"sk-live-123":       false,
		"sk-${TOKEN}":       false,
		"${A}${B}":          false,
		"Bearer ${A} extra": false,
	} {
		if got := safeValue(v); got != want {
			t.Errorf("safeValue(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestCodexVars(t *testing.T) {
	cv, why := codexVarsOf(mcpServer{Command: "x", Env: map[string]string{"B": "${B}", "A": "${A}", "EMPTY": ""}})
	if why != "" || strings.Join(cv.envVars, ",") != "A,B" || len(cv.literalEnv) != 1 {
		t.Fatalf("env: %+v %q", cv, why)
	}
	cv, why = codexVarsOf(mcpServer{URL: "https://x", Headers: map[string]string{"Authorization": "Bearer ${T}", "X-Team": "${TEAM}"}})
	if why != "" || cv.bearer != "T" || cv.headerVars["X-Team"] != "TEAM" {
		t.Fatalf("headers: %+v %q", cv, why)
	}
	for _, c := range []struct {
		s    mcpServer
		want string
	}{
		{mcpServer{Command: "x", Env: map[string]string{"TOKEN": "${GH_TOKEN}"}}, "own name"},
		{mcpServer{Command: "x", Env: map[string]string{"TOKEN": "Bearer ${TOKEN}"}}, "own name"},
		{mcpServer{URL: "https://x", Headers: map[string]string{"X-Key": "Key ${K}"}}, "X-Key"},
		{mcpServer{Command: "x", Args: []string{"--token", "${T}"}}, "does not expand"},
		{mcpServer{URL: "https://${HOST}/mcp"}, "does not expand"},
		{mcpServer{URL: "https://x", Transport: "sse"}, "SSE"},
	} {
		if _, why := codexVarsOf(c.s); !strings.Contains(why, c.want) {
			t.Errorf("%+v: %q, want %q", c.s, why, c.want)
		}
	}
	// A second Bearer header after the first is a plain header variable.
	if why := unwritable("claude", mcpServer{Command: "x", Args: []string{"${T}"}}); why != "" {
		t.Errorf("claude expands args: %q", why)
	}
}

// Codex's way of naming variables reads back as references, and travels
// to an agent that expands them in that agent's own form.
func TestSyncFromCodexVariables(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/master\n")
	write(t, filepath.Join(root, ".codex", "config.toml"), `
[mcp_servers.gh]
command = "npx"
env_vars = ["GITHUB_TOKEN", { name = "REMOTE", source = "remote" }]

[mcp_servers.api]
url = "https://api.example/mcp"
bearer_token_env_var = "API_TOKEN"

[mcp_servers.api.env_http_headers]
X-Team = "TEAM"

[mcp_servers.leaky]
url = "https://leaky.example/mcp"
http_headers = { Authorization = "Bearer abc" }
`)
	res, err := Sync(root, "codex", []string{"opencode", "devin", "claude"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := find(t, res, "devin", SyncMCP, "leaky"); c.Action != ActionSkip {
		t.Fatalf("a literal header travelled: %+v", c)
	}
	var devin map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(root, ".devin", "mcp_config.json"))), &devin); err != nil {
		t.Fatal(err)
	}
	gh := obj(obj(devin, "mcpServers"), "gh")
	if env := obj(gh, "env"); str(env, "GITHUB_TOKEN") != "${env:GITHUB_TOKEN}" || str(env, "REMOTE") != "${env:REMOTE}" {
		t.Fatalf("devin gh %v", gh)
	}
	api := obj(obj(devin, "mcpServers"), "api")
	if str(api, "transport") != "http" || api["type"] != nil || str(obj(api, "headers"), "Authorization") != "Bearer ${env:API_TOKEN}" ||
		str(obj(api, "headers"), "X-Team") != "${env:TEAM}" {
		t.Fatalf("devin api %v", api)
	}
	var oc map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(root, "opencode.json"))), &oc); err != nil {
		t.Fatal(err)
	}
	if h := obj(obj(obj(oc, "mcp"), "api"), "headers"); str(h, "Authorization") != "Bearer {env:API_TOKEN}" {
		t.Fatalf("opencode api %v", h)
	}
	var cl map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(root, ".mcp.json"))), &cl); err != nil {
		t.Fatal(err)
	}
	if h := obj(obj(obj(cl, "mcpServers"), "api"), "headers"); str(h, "Authorization") != "Bearer ${API_TOKEN}" {
		t.Fatalf("claude api %v", h)
	}
	// And back again: what was written for Codex reads as it began.
	back := serverOf("codex", "api", obj(obj(readTOML(filepath.Join(root, ".codex", "config.toml")), "mcp_servers"), "api"))
	if back.Headers["Authorization"] != "Bearer ${API_TOKEN}" || back.Headers["X-Team"] != "${TEAM}" {
		t.Fatalf("read back %+v", back)
	}
}

// What goes to Codex is what Codex can say: variables by name, a bearer
// token by its variable, and a server it cannot express left out.
func TestSyncToCodexVariables(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/master\n")
	write(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{
	  "api": {"type":"http","url":"https://api.example/mcp","headers":{"Authorization":"Bearer ${API_TOKEN}","X-Team":"${TEAM}"}},
	  "renamed": {"command":"npx","env":{"GITHUB_PERSONAL_ACCESS_TOKEN":"${GITHUB_TOKEN}"}},
	  "stream": {"type":"sse","url":"https://sse.example/mcp"}
	}}`)
	res, err := Sync(root, "claude", []string{"codex", "gemini", "devin"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := find(t, res, "codex", SyncMCP, "renamed"); c.Action != ActionSkip || !strings.Contains(c.Detail, "own name") {
		t.Fatalf("renamed: %+v", c)
	}
	if c := find(t, res, "codex", SyncMCP, "stream"); c.Action != ActionSkip || !strings.Contains(c.Detail, "SSE") {
		t.Fatalf("stream: %+v", c)
	}
	codex := read(t, filepath.Join(root, ".codex", "config.toml"))
	for _, want := range []string{`bearer_token_env_var = "API_TOKEN"`, "[mcp_servers.api.env_http_headers]", `X-Team = "TEAM"`} {
		if !strings.Contains(codex, want) {
			t.Fatalf("config.toml lacks %q:\n%s", want, codex)
		}
	}
	if strings.Contains(codex, "${") || readTOML(filepath.Join(root, ".codex", "config.toml")) == nil {
		t.Fatalf("config.toml:\n%s", codex)
	}
	// Gemini: an SSE server under url, and the rest under httpUrl.
	var gs map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(root, ".gemini", "settings.json"))), &gs); err != nil {
		t.Fatal(err)
	}
	if got := obj(obj(gs, "mcpServers"), "stream"); str(got, "url") != "https://sse.example/mcp" || got["httpUrl"] != nil {
		t.Fatalf("gemini stream %v", got)
	}
	// Devin reads Claude's .mcp.json itself, so nothing is written for it.
	if c := find(t, res, "devin", SyncMCP, "api"); c.Action != ActionSame || !strings.Contains(c.Detail, "reads it there itself") {
		t.Fatalf("devin api: %+v", c)
	}
	// A Gemini server under url reads back as SSE.
	if s := serverOf("gemini", "stream", obj(obj(gs, "mcpServers"), "stream")); s.Transport != "sse" {
		t.Fatalf("gemini url read as %+v", s)
	}
}
