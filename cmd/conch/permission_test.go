package main

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

func TestPermissionSubject(t *testing.T) {
	for _, tc := range []struct {
		tool, input, cwd, want string
	}{
		{"Bash", `{"command": "go test ./..."}`, "/w", "go test ./..."},
		{"Edit", `{"file_path": "/w/internal/a.go"}`, "/w", "internal/a.go"},
		{"Write", `{"file_path": "/etc/hosts"}`, "/w", "/etc/hosts"},
		{"NotebookEdit", `{"notebook_path": "/w/n.ipynb"}`, "/w", "n.ipynb"},
		{"Read", `{"file_path": "/w/a"}`, "", "/w/a"},
		{"WebFetch", `{"url": "https://example.com"}`, "", "https://example.com"},
		{"WebSearch", `{"query": "go 1.30"}`, "", "go 1.30"},
		{"mcp__x__y", `{"a": 1}`, "", `{"a": 1}`},
		{"Bash", `not json`, "", ""},
	} {
		if got := permissionSubject(tc.tool, json.RawMessage(tc.input), tc.cwd); got != tc.want {
			t.Errorf("%s %s: %q, want %q", tc.tool, tc.input, got, tc.want)
		}
	}
	long := `{"x": "` + strings.Repeat("a", 500) + `"}`
	if got := permissionSubject("Other", json.RawMessage(long), ""); len(got) != 300 {
		t.Fatalf("long input kept %d bytes", len(got))
	}
}

func TestAnswerPermission(t *testing.T) {
	a4Env(t)
	sock := config.SocketPath()
	srv := startA4Server(t, sock)
	answer := proto.PermissionAnswer{Decision: "allow", Rule: `allow Bash "go test *"`}
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodAgentPermission {
			return answer, nil
		}
		return nil, nil
	})
	var out bytes.Buffer
	if !answerPermission(sock, "p1", "Bash", "go test ./...", &out) {
		t.Fatal("an allow was not answered")
	}
	var got struct {
		H struct {
			Event    string         `json:"hookEventName"`
			Decision map[string]any `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.H.Event != "PermissionRequest" || got.H.Decision["behavior"] != "allow" {
		t.Fatalf("output %s %v", out.String(), err)
	}
	var ask proto.PermissionAsk
	srv.params(t, proto.MethodAgentPermission, &ask)
	if ask.ID != "p1" || ask.Tool != "Bash" || ask.Subject != "go test ./..." || ask.Agent != "claude" {
		t.Fatalf("asked %+v", ask)
	}

	answer = proto.PermissionAnswer{Decision: "deny", Rule: "deny WebFetch"}
	out.Reset()
	if !answerPermission(sock, "p1", "WebFetch", "https://x", &out) || !strings.Contains(out.String(), `"behavior":"deny"`) || !strings.Contains(out.String(), "deny WebFetch") {
		t.Fatalf("deny: %s", out.String())
	}
	// Nothing to say, a dry run, a server error: the agent asks as usual.
	for _, a := range []proto.PermissionAnswer{{}, {Rule: "x", DryRun: true}} {
		answer = a
		out.Reset()
		if answerPermission(sock, "p1", "Bash", "ls", &out) || out.Len() != 0 {
			t.Fatalf("%+v answered: %s", a, out.String())
		}
	}
	srv.setHello(func(int) proto.HelloResult {
		h := currentHello(0)
		h.Capabilities = slices.DeleteFunc(slices.Clone(h.Capabilities), func(c string) bool { return c == proto.CapPermissionRules })
		return h
	})
	answer = proto.PermissionAnswer{Decision: "allow"}
	if answerPermission(sock, "p1", "Bash", "ls", &out) {
		t.Fatal("an old server was asked")
	}
	if answerPermission(t.TempDir()+"/none.sock", "p1", "Bash", "ls", &out) {
		t.Fatal("answered with no server")
	}
}
