package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudCommandRefuses(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "usage: conch cloud agent"},
		{[]string{"nope"}, `unknown cloud subcommand "nope"`},
		{[]string{"agent", "extra"}, "takes no arguments"},
		{[]string{"agent", "-user", "no-such-user-here"}, "the machine's user no-such-user-here"},
	} {
		err := runCloud(c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
}

// TestCloudAgentBadBootExits: a boot.json others can read stops the agent
// before it says anything to anybody.
func TestCloudAgentBadBootExits(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	boot := filepath.Join(dir, "boot.json")
	os.WriteFile(boot, []byte(`{"machine_id":"m_0123456789abcdef","api":"http://127.0.0.1:1","machine_token":"t"}`), 0o644)
	os.Chmod(boot, 0o644)
	err := runCloud([]string{"agent", "-boot", boot, "-state", filepath.Join(dir, "state")})
	if err == nil || !strings.Contains(err.Error(), "readable by others") {
		t.Fatalf("%v", err)
	}
}
