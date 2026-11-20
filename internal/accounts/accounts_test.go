package accounts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupports(t *testing.T) {
	for agent, want := range map[string]string{"claude": "CLAUDE_CONFIG_DIR", "codex": "CODEX_HOME", "gemini": "", "": "", "opencode": ""} {
		if got := Var(agent); got != want {
			t.Errorf("Var(%q) = %q, want %q", agent, got, want)
		}
		if Supports(agent) != (want != "") {
			t.Errorf("Supports(%q) = %v", agent, Supports(agent))
		}
	}
	if got := Agents(); strings.Join(got, ",") != "claude,codex" {
		t.Errorf("Agents() = %v", got)
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	b, err := Load(filepath.Join(t.TempDir(), "nope", "accounts.json"))
	if err != nil || len(b.Accounts) != 0 || len(b.Pins) != 0 || b.FailoverAt != 0 {
		t.Fatalf("Load(missing) = %+v, %v", b, err)
	}
}

func TestLoadMalformed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "accounts.json")
	for _, body := range []string{"{", "[]", `{"accounts": 3}`} {
		os.WriteFile(p, []byte(body), 0o600)
		if _, err := Load(p); err == nil {
			t.Errorf("Load(%q) gave no error", body)
		}
	}
	os.WriteFile(p, nil, 0o600)
	if b, err := Load(p); err != nil || len(b.Accounts) != 0 {
		t.Errorf("Load(empty file) = %+v, %v", b, err)
	}
}

func TestAddSaveLoad(t *testing.T) {
	home := t.TempDir()
	var b Book
	a, err := b.Add("claude", "work", "", home)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "accounts", "claude", "work")
	if a.Dir != want {
		t.Errorf("dir = %q, want %q", a.Dir, want)
	}
	if st, err := os.Stat(want); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Errorf("account folder not made 0700: %v %v", st, err)
	}
	own := filepath.Join(home, "elsewhere")
	if _, err := b.Add("codex", "work", own, home); err != nil {
		t.Fatal(err)
	}
	b.FailoverAt = 90
	path := filepath.Join(home, "accounts.json")
	if err := b.Save(path); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("accounts.json mode %v", st.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Accounts) != 2 || got.FailoverAt != 90 || got.Accounts[1].Dir != own {
		t.Errorf("round trip = %+v", got)
	}
	if acc, ok := got.Get("codex", "work"); !ok || acc.Dir != own {
		t.Errorf("Get = %+v %v", acc, ok)
	}
	if _, ok := got.Get("claude", "home"); ok {
		t.Error("Get found an account never added")
	}
}

func TestAddRefuses(t *testing.T) {
	home := t.TempDir()
	var b Book
	if _, err := b.Add("claude", "work", "", home); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ agent, name, dir, why string }{
		{"gemini", "work", "", "one account"},
		{"claude", "work", "", "already"},
		{"claude", "default", "", "default"},
		{"claude", "", "", "name"},
		{"claude", "-x", "", "name"},
		{"claude", "a b", "", "name"},
		{"claude", "a/b", "", "name"},
		{"claude", strings.Repeat("a", 33), "", "name"},
		{"claude", "rel", "relative/dir", "absolute"},
		{"claude", "same", filepath.Join(home, "accounts", "claude", "work"), "already uses"},
	}
	for _, c := range cases {
		_, err := b.Add(c.agent, c.name, c.dir, home)
		if err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("Add(%q, %q, %q) = %v, want an error saying %q", c.agent, c.name, c.dir, err, c.why)
		}
	}
	if len(b.Accounts) != 1 {
		t.Errorf("a refused add was kept: %+v", b.Accounts)
	}
}

func TestAddExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var b Book
	a, err := b.Add("claude", "work", "~/.claude-work", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if a.Dir != filepath.Join(home, ".claude-work") {
		t.Errorf("dir = %q", a.Dir)
	}
}

func TestRemove(t *testing.T) {
	home := t.TempDir()
	var b Book
	b.Add("claude", "work", "", home)
	b.Add("codex", "work", "", home)
	b.Add("claude", "home", "", home)
	b.Pin("/src/app", "work")
	b.Pin("/src/blog", "home")
	if err := b.Remove("claude", "nope"); err == nil {
		t.Error("removing an unknown account said nothing")
	}
	if err := b.Remove("claude", "work"); err != nil {
		t.Fatal(err)
	}
	// codex still has a "work", so the pin naming it stays.
	if len(b.Pins) != 2 {
		t.Errorf("pins after removing claude work = %+v", b.Pins)
	}
	b.Remove("claude", "home")
	if len(b.Pins) != 1 || b.Pins[0].Account != "work" {
		t.Errorf("a pin naming an account nobody has was kept: %+v", b.Pins)
	}
	// The folder holds the sign-in and conversations: never deleted.
	if _, err := os.Stat(filepath.Join(home, "accounts", "claude", "work")); err != nil {
		t.Errorf("Remove deleted the account's folder: %v", err)
	}
}

func TestPin(t *testing.T) {
	home := t.TempDir()
	var b Book
	b.Add("claude", "work", "", home)
	if err := b.Pin("/src/app", "nobody"); err == nil {
		t.Error("pinning to an account nobody has said nothing")
	}
	if err := b.Pin("relative", "work"); err == nil {
		t.Error("pinning a relative path said nothing")
	}
	if err := b.Pin("/src/app/", "work"); err != nil {
		t.Fatal(err)
	}
	if err := b.Pin("/src/app", "default"); err != nil {
		t.Fatal(err)
	}
	if len(b.Pins) != 1 || b.Pins[0].Path != "/src/app" || b.Pins[0].Account != "default" {
		t.Errorf("pinning again did not replace: %+v", b.Pins)
	}
	if err := b.Pin("/src/app", ""); err != nil {
		t.Fatal(err)
	}
	if len(b.Pins) != 0 {
		t.Errorf("unpin left %+v", b.Pins)
	}
	if err := b.Pin("/src/app", ""); err == nil {
		t.Error("unpinning what was not pinned said nothing")
	}
}

func TestPinFor(t *testing.T) {
	var b Book
	b.Pins = []Pin{{"/src/app", "work"}, {"/src/app/.worktrees/fix", "home"}, {"/src/apple", "other"}}
	for dir, want := range map[string]string{
		"/src/app":                     "work",
		"/src/app/cmd":                 "work",
		"/src/app/.worktrees/fix":      "home",
		"/src/app/.worktrees/fix/deep": "home",
		"/src/app/.worktrees/fixed":    "work",
		"/src/apple":                   "other",
		"/src":                         "",
		"":                             "",
	} {
		if got, _ := b.PinFor(dir); got != want {
			t.Errorf("PinFor(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestChoose(t *testing.T) {
	home := t.TempDir()
	var b Book
	b.Add("claude", "work", "", home)
	b.Add("claude", "home", "", home)
	b.Add("codex", "work", "", home)
	b.Pin("/src/app", "work")
	b.Pin("/src/blog", "home")
	none := func(agent, account string) float64 { return 0 }
	cases := []struct {
		agent, asked, dir string
		want              string
		pinned            bool
		err               string
	}{
		{"claude", "", "/elsewhere", "", false, ""},
		{"claude", "", "/src/app/x", "work", true, ""},
		{"codex", "", "/src/app", "work", true, ""},
		{"codex", "", "/src/blog", "", false, ""}, // codex has no "home"
		{"claude", "home", "/src/app", "home", false, ""},
		{"claude", "default", "/src/app", "", false, ""},
		{"claude", "nope", "", "", false, "no claude account"},
		{"gemini", "", "/src/app", "", false, ""},
		{"gemini", "work", "", "", false, "one account"},
		{"", "", "/src/app", "", false, ""},
	}
	for _, c := range cases {
		ch, err := b.Choose(c.agent, c.asked, c.dir, none)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("Choose(%q, %q, %q) error = %v, want %q", c.agent, c.asked, c.dir, err, c.err)
			}
			continue
		}
		if err != nil || ch.Account != c.want || ch.Pinned != c.pinned {
			t.Errorf("Choose(%q, %q, %q) = %+v, %v; want %q pinned %v", c.agent, c.asked, c.dir, ch, err, c.want, c.pinned)
		}
	}
	ch, _ := b.Choose("claude", "work", "", none)
	if env := ch.Env("claude"); len(env) != 1 || env[0] != "CLAUDE_CONFIG_DIR="+filepath.Join(home, "accounts", "claude", "work") {
		t.Errorf("Env = %v", env)
	}
	if ch, _ := b.Choose("claude", "", "", none); ch.Env("claude") != nil {
		t.Errorf("the default account set %v", ch.Env("claude"))
	}
}

func TestChooseFailover(t *testing.T) {
	home := t.TempDir()
	var b Book
	b.Add("claude", "work", "", home)
	b.Add("claude", "home", "", home)
	b.Pin("/src/app", "work")
	used := map[string]float64{}
	usage := func(agent, account string) float64 { return used[agent+"/"+account] }

	// Off: a full account is still chosen.
	used["claude/work"] = 100
	if ch, _ := b.Choose("claude", "", "/src/app", usage); ch.Account != "work" || ch.PassedOver != "" {
		t.Errorf("failover off moved to %+v", ch)
	}
	b.FailoverAt = 90
	// work is full: the next one after it is home.
	if ch, _ := b.Choose("claude", "", "/src/app", usage); ch.Account != "home" || ch.PassedOver != "work" {
		t.Errorf("failover from work = %+v", ch)
	}
	// home is full too: round to the default.
	used["claude/home"] = 95
	if ch, _ := b.Choose("claude", "", "/src/app", usage); ch.Account != "" || ch.PassedOver != "work" {
		t.Errorf("failover past home = %+v", ch)
	}
	// Every one is full: stay where it was pinned rather than pretend.
	used["claude/"] = 90
	if ch, _ := b.Choose("claude", "", "/src/app", usage); ch.Account != "work" || ch.PassedOver != "" {
		t.Errorf("all full = %+v", ch)
	}
	// The default fails over to the first added account.
	used["claude/work"] = 10
	if ch, _ := b.Choose("claude", "", "/elsewhere", usage); ch.Account != "work" || ch.PassedOver != "default" {
		t.Errorf("failover from default = %+v", ch)
	}
	// Asked for by name: what was asked for, full or not.
	if ch, _ := b.Choose("claude", "home", "", usage); ch.Account != "home" || ch.PassedOver != "" {
		t.Errorf("asked for home = %+v", ch)
	}
	// Just under the line stays.
	used["claude/"] = 89.9
	if ch, _ := b.Choose("claude", "", "/elsewhere", usage); ch.Account != "" {
		t.Errorf("89.9%% of 90 failed over: %+v", ch)
	}
	// An agent with only its own account has nowhere to go.
	used["codex/"] = 100
	if ch, _ := b.Choose("codex", "", "", usage); ch.Account != "" || ch.PassedOver != "" {
		t.Errorf("codex with one account = %+v", ch)
	}
}

func TestLabel(t *testing.T) {
	if Label("") != "default" || Label("work") != "work" {
		t.Errorf("Label = %q %q", Label(""), Label("work"))
	}
}

// TestFailoverPerAgent: an agent with a line of its own is held to it, the
// others to every agent's; taking it away puts the agent back on the
// shared line; and a book from before per-agent lines loads unchanged.
func TestFailoverPerAgent(t *testing.T) {
	home := t.TempDir()
	var b Book
	b.Add("claude", "work", "", home)
	b.Add("codex", "review", "", home)
	used := map[string]float64{"claude/": 85, "codex/": 85}
	usage := func(agent, account string) float64 { return used[agent+"/"+account] }

	b.FailoverAt = 80
	if b.LineFor("claude") != 80 || b.LineFor("codex") != 80 {
		t.Fatalf("shared line: %d %d", b.LineFor("claude"), b.LineFor("codex"))
	}
	if ch, _ := b.Choose("codex", "", "", usage); ch.Account != "review" {
		t.Errorf("codex past the shared 80: %+v", ch)
	}
	// Codex's own line, higher: 85% is under it now; Claude's is unchanged.
	if err := b.SetLine("codex", 90, true); err != nil {
		t.Fatal(err)
	}
	if ch, _ := b.Choose("codex", "", "", usage); ch.Account != "" || ch.PassedOver != "" {
		t.Errorf("codex under its own 90: %+v", ch)
	}
	if ch, _ := b.Choose("claude", "", "", usage); ch.Account != "work" {
		t.Errorf("claude still on the shared 80: %+v", ch)
	}
	// An agent's own line of 0 is off for that agent alone.
	if err := b.SetLine("claude", 0, true); err != nil {
		t.Fatal(err)
	}
	if ch, _ := b.Choose("claude", "", "", usage); ch.Account != "" {
		t.Errorf("claude's own line off: %+v", ch)
	}
	if b.LineFor("claude") != 0 || b.FailoverAt != 80 {
		t.Errorf("claude off touched the shared line: %d %d", b.LineFor("claude"), b.FailoverAt)
	}
	// Taken away: back on the shared line, and an empty map is not kept.
	b.SetLine("claude", 0, false)
	b.SetLine("codex", 0, false)
	if b.LineFor("claude") != 80 || b.FailoverFor != nil {
		t.Errorf("after taking both away: %d %v", b.LineFor("claude"), b.FailoverFor)
	}
	// Taking away a line an agent never had is nothing.
	if err := b.SetLine("codex", 0, false); err != nil {
		t.Errorf("taking away nothing: %v", err)
	}
	// An agent that keeps one account has no line to set.
	if err := b.SetLine("gemini", 90, true); err == nil {
		t.Error("gemini took a failover line")
	}

	// Saved and read back; and the file 0.2.1 wrote still loads.
	b.SetLine("codex", 95, true)
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := b.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.LineFor("codex") != 95 || got.LineFor("claude") != 80 {
		t.Errorf("read back: %v %v", got.FailoverFor, err)
	}
	old := filepath.Join(t.TempDir(), "accounts.json")
	os.WriteFile(old, []byte(`{"accounts":[{"agent":"claude","name":"work","dir":"/w"}],"failover_at":90}`), 0o600)
	o, err := Load(old)
	if err != nil || o.LineFor("claude") != 90 || o.LineFor("codex") != 90 || o.FailoverFor != nil {
		t.Errorf("0.2.1's book: %+v %v", o, err)
	}
}
