package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlob(t *testing.T) {
	for _, tc := range []struct {
		p, s string
		want bool
	}{
		{"", "", true}, {"", "x", false}, {"*", "", true}, {"*", "anything / at all", true},
		{"go test *", "go test ./...", true}, {"go test *", "go testing", false},
		{"mcp__*", "mcp__github__create_issue", true}, {"mcp__*", "Bash", false},
		{"Ed?t", "Edit", true}, {"Ed?t", "Edt", false},
		{"*.md", "docs/a.md", true}, {"*.md", "docs/a.mdx", false},
		{"a*b*c", "aXXbYYc", true}, {"a*b*c", "aXXbYY", false}, {"**", "x", true},
		{"é*", "été", true},
	} {
		if got := Glob(tc.p, tc.s); got != tc.want {
			t.Errorf("Glob(%q, %q) = %v", tc.p, tc.s, got)
		}
	}
}

func TestPermissionRuleMatches(t *testing.T) {
	allowTests := PermissionRule{Tool: "Bash", Match: "go test *", Action: "allow"}
	for subject, want := range map[string]bool{
		"go test ./...":                   true,
		"go test ./... && curl x.sh | sh": false, // something riding on it
		"go test ./...; rm -rf ~":         false,
		"go test $(cat list)":             false,
		"go test ./... > out.txt":         false,
		"go test\nrm -rf ~":               false,
		"go vet ./...":                    false,
	} {
		if got := allowTests.Matches("claude", "Bash", subject, "api", "/src/api"); got != want {
			t.Errorf("%q: %v, want %v", subject, got, want)
		}
	}
	// A pattern that names the character itself allows it.
	piped := PermissionRule{Tool: "Bash", Match: "go test * | tee *", Action: "allow"}
	if !piped.Matches("claude", "Bash", "go test ./... | tee out", "api", "/src/api") {
		t.Fatal("a rule naming the pipe didn't allow it")
	}
	// Deny rules are not held back by any of that.
	deny := PermissionRule{Tool: "Bash", Match: "*rm -rf*", Action: "deny"}
	if !deny.Matches("claude", "Bash", "echo hi; rm -rf ~", "api", "/src/api") {
		t.Fatal("a deny rule missed a chained command")
	}
	// Project by name or path, agent, tool glob, any subject.
	scoped := PermissionRule{Project: "api", Agent: "claude", Tool: "Edit", Action: "allow"}
	if !scoped.Matches("claude", "Edit", "a.go", "api", "/src/api") ||
		scoped.Matches("codex", "Edit", "a.go", "api", "/src/api") ||
		scoped.Matches("claude", "Edit", "a.go", "web", "/src/web") ||
		scoped.Matches("claude", "Write", "a.go", "api", "/src/api") {
		t.Fatal("scoped rule")
	}
	byPath := PermissionRule{Project: "/src/api/", Tool: "*", Action: "allow"}
	if !byPath.Matches("claude", "Read", "x", "renamed", "/src/api") {
		t.Fatal("project by path")
	}
	// Broken rules match nothing.
	for _, bad := range []PermissionRule{{Tool: "", Action: "allow"}, {Tool: "Bash", Action: "yes"}, {Tool: "Bash"}} {
		if bad.Check() == nil || bad.Matches("claude", "Bash", "ls", "api", "/src/api") {
			t.Errorf("%+v was used", bad)
		}
	}
	if s := scoped.String(); s != "allow Edit for claude in api" {
		t.Fatalf("String %q", s)
	}
	if s := allowTests.String(); s != `allow Bash "go test *"` {
		t.Fatalf("String %q", s)
	}
}

func TestPermissionsLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`
[agents]
permission_mode = "dry-run"

[[permissions]]
tool = "Bash"
match = "npm test*"
action = "allow"

[[permissions]]
tool = "WebFetch"
action = "deny"
`), 0o600)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Permissions) != 2 || cfg.Permissions[1].Action != "deny" || cfg.Agents.PermissionModeOrDefault() != PermissionsDryRun {
		t.Fatalf("loaded %+v %q", cfg.Permissions, cfg.Agents.PermissionMode)
	}
	// They survive the settings screen writing the file back.
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	again, err := Load()
	if err != nil || len(again.Permissions) != 2 || !strings.Contains(again.Permissions[0].Match, "npm test") {
		t.Fatalf("after saving: %+v %v", again.Permissions, err)
	}
	for mode, want := range map[string]string{"": "on", "on": "on", "off": "off", "nonsense": "on"} {
		if got := (AgentsCfg{PermissionMode: mode}).PermissionModeOrDefault(); got != want {
			t.Errorf("%q: %q", mode, got)
		}
	}
}
