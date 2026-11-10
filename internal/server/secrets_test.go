package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/agentsetup"
	"github.com/Amitgb14/conch/internal/proto"
)

// fakeSecurity is a security that knows one token, CONCH_MCP_WIKI_TOKEN,
// under the service conch-mcp, and logs every account asked for.
func fakeSecurity(t *testing.T) (calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "security")
	os.WriteFile(bin, []byte(`#!/bin/sh
# find-generic-password -s SERVICE -a ACCOUNT -w
echo "$5" >> `+calls+`
if [ "$1" = find-generic-password ] && [ "$3" = conch-mcp ] && [ "$5" = CONCH_MCP_WIKI_TOKEN ]; then echo 'tok-123'; exit 0; fi
echo 'security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.' >&2
exit 44
`), 0o755)
	t.Setenv("CONCH_SECURITY", bin)
	return calls
}

// A pane is given each token the library refers to that the Keychain
// holds — not one somebody exported themselves, and nothing for a variable
// the Keychain lacks — and the Keychain is read again once the library is
// saved, not at every pane.
func TestPanesGetTheLibrarysTokens(t *testing.T) {
	s, p := afterFixture(t)
	calls := fakeSecurity(t)
	t.Setenv("OWN_TOKEN", "mine")
	lib := agentsetup.Library{Servers: []agentsetup.LibServer{
		{Name: "wiki", URL: "https://wiki.example/mcp", Headers: map[string]string{"Authorization": "Bearer ${CONCH_MCP_WIKI_TOKEN}"}, Agents: []string{"claude"}},
		{Name: "gh", Command: "npx", Args: []string{"server-github"}, Env: map[string]string{"GITHUB_TOKEN": "${OWN_TOKEN}", "MISSING": "${NOT_KEPT}"}, Agents: []string{"claude"}},
	}}
	if _, perr := s.agentLibrary(proto.AgentLibraryParams{Set: &lib}); perr != nil {
		t.Fatal(perr)
	}
	env := s.secretEnv()
	if len(env) != 1 || env[0] != "CONCH_MCP_WIKI_TOKEN=tok-123" {
		t.Fatalf("given %v", env)
	}
	asked := readCalls(calls)
	if strings.Contains(asked, "OWN_TOKEN") || !strings.Contains(asked, "NOT_KEPT") {
		t.Fatalf("asked the Keychain for %q", asked)
	}

	// A pane started now has it in its environment.
	out := filepath.Join(t.TempDir(), "env")
	info, perr := s.create(proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", "env > " + out + "; sleep 30"}, Cwd: p.root, Cols: 80, Rows: 24})
	if perr != nil {
		t.Fatal(perr)
	}
	t.Cleanup(func() { s.close(info.ID) })
	a5WaitFor(t, "the pane's environment", func() bool { b, _ := os.ReadFile(out); return strings.Contains(string(b), "PATH=") })
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), "CONCH_MCP_WIKI_TOKEN=tok-123\n") || !strings.Contains(string(b), "OWN_TOKEN=mine\n") {
		t.Fatalf("the pane's environment:\n%s", b)
	}

	// Read once, then from memory; saving the library reads it afresh.
	before := strings.Count(readCalls(calls), "CONCH_MCP_WIKI_TOKEN")
	s.secretEnv()
	if n := strings.Count(readCalls(calls), "CONCH_MCP_WIKI_TOKEN"); n != before {
		t.Fatalf("asked the Keychain again: %d times, not %d", n, before)
	}
	if _, perr := s.agentLibrary(proto.AgentLibraryParams{Set: &lib}); perr != nil {
		t.Fatal(perr)
	}
	s.secretEnv()
	if n := strings.Count(readCalls(calls), "CONCH_MCP_WIKI_TOKEN"); n != before+1 {
		t.Fatalf("not read afresh after a save: %d", n)
	}
}

// With no library, or a security that is not there, a pane is given
// nothing and starts as ever.
func TestNoTokensWithoutALibraryOrAKeychain(t *testing.T) {
	s, _ := afterFixture(t)
	calls := fakeSecurity(t)
	if env := s.secretEnv(); len(env) != 0 || readCalls(calls) != "" {
		t.Fatalf("no library: %v, asked %q", env, readCalls(calls))
	}
	lib := agentsetup.Library{Servers: []agentsetup.LibServer{
		{Name: "wiki", URL: "https://wiki.example/mcp", Headers: map[string]string{"Authorization": "Bearer ${CONCH_MCP_WIKI_TOKEN}"}, Agents: []string{"claude"}}}}
	if _, perr := s.agentLibrary(proto.AgentLibraryParams{Set: &lib}); perr != nil {
		t.Fatal(perr)
	}
	t.Setenv("CONCH_SECURITY", filepath.Join(t.TempDir(), "absent"))
	s.secrets.forget()
	if env := s.secretEnv(); len(env) != 0 {
		t.Fatalf("no security: %v", env)
	}
}
