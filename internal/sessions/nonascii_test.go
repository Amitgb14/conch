package sessions

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSQLiteStoresUnderANonASCIIFolder: SQLite's substr counts characters
// and the prefix was measured in bytes, so a project under /Users/josé
// listed no session from a folder inside it — OpenCode's or Goose's.
func TestSQLiteStoresUnderANonASCIIFolder(t *testing.T) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	home := t.TempDir()
	env := Env{Home: home, Getenv: func(string) string { return "" }}
	oc := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	os.MkdirAll(filepath.Dir(oc), 0o755)
	schema := `CREATE TABLE session (id text primary key, parent_id text, directory text, title text, time_created integer, time_updated integer, time_archived integer);
INSERT INTO session VALUES ('ses_top',NULL,'/Users/josé/app','At the top',1000,3000,NULL);
INSERT INTO session VALUES ('ses_web',NULL,'/Users/josé/app/web','In web',1000,2000,NULL);
INSERT INTO session VALUES ('ses_deep',NULL,'/Users/josé/app/wéb/ñ','Deeper',1000,1500,NULL);
INSERT INTO session VALUES ('ses_sib',NULL,'/Users/josé/apple','A sibling',1000,2500,NULL);`
	if out, err := exec.Command(bin, oc, schema).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	gdb := filepath.Join(home, ".local", "share", "goose", "sessions", "sessions.db")
	os.MkdirAll(filepath.Dir(gdb), 0o755)
	gschema := `CREATE TABLE sessions (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', session_type TEXT NOT NULL DEFAULT 'user', working_dir TEXT NOT NULL, created_at TIMESTAMP, updated_at TIMESTAMP);
CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, role TEXT NOT NULL, content_json TEXT NOT NULL, created_timestamp INTEGER NOT NULL);
INSERT INTO sessions VALUES ('g_web', 'Goose in web', 'user', '/Users/josé/app/web', '2026-11-01 10:00:00', '2026-11-01 11:00:00');
INSERT INTO sessions VALUES ('g_sib', 'Goose next door', 'user', '/Users/josé/apple', '2026-11-01 10:00:00', '2026-11-01 12:00:00');
INSERT INTO messages (session_id, role, content_json, created_timestamp) VALUES ('g_web', 'user', '[{"type":"text","text":"hi"}]', 1), ('g_sib', 'user', '[{"type":"text","text":"hi"}]', 1);`
	if out, err := exec.Command(bin, gdb, gschema).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	listed := func(dir string) map[string]bool {
		ids := map[string]bool{}
		for _, s := range List(env, []string{dir}, 0) {
			ids[s.Agent+":"+s.ID] = true
		}
		return ids
	}
	// With a trailing slash or without, the same.
	for _, dir := range []string{"/Users/josé/app", "/Users/josé/app/"} {
		ids := listed(dir)
		for _, want := range []string{"opencode:ses_web", "opencode:ses_deep", "goose:g_web"} {
			if !ids[want] {
				t.Errorf("%s not listed under %s: %v", want, dir, ids)
			}
		}
		if ids["opencode:ses_sib"] || ids["goose:g_sib"] {
			t.Errorf("a sibling folder's sessions listed under %s: %v", dir, ids)
		}
	}
	if ids := listed("/Users/josé/app"); !ids["opencode:ses_top"] {
		t.Errorf("the folder's own session: %v", ids)
	}
	if got := sqlUnder("dir", "/a/é/"); got[1] != "substr(dir, 1, 5) = '/a/é/'" {
		t.Errorf("sqlUnder: %v", got)
	}
}
