package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// launchDirs makes a home folder and a repository beside it, symlinks
// resolved, with HOME pointing at the home.
func launchDirs(t *testing.T) (home, repo string) {
	t.Helper()
	base := resolvePath(t.TempDir())
	home, repo = filepath.Join(base, "home"), filepath.Join(base, "work", "api")
	for _, d := range []string{home, filepath.Join(repo, ".git"), filepath.Join(repo, "cmd", "tool")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv(updateRemotesEnv, "")
	return home, repo
}

// launchUpdate runs msg through Update as the program would.
func launchUpdate(m *Model, msg tea.Msg) tea.Cmd {
	next, cmd := m.Update(msg)
	*m = next.(Model)
	return cmd
}

// launchProjects delivers the local machine's first list of projects.
func launchProjects(m *Model, projects ...proto.ProjectInfo) tea.Cmd {
	return launchUpdate(m, projectsMsg{machine: localMachine, gen: m.machines[0].gen, projects: projects})
}

func TestLaunchProjectFindsTheClosest(t *testing.T) {
	api := proto.ProjectInfo{ID: "api", Path: "/src/api", Worktrees: []proto.WorktreeInfo{
		{Path: "/src/api", Main: true}, {Path: "/wt/api-feat", Branch: "feat"}}}
	tools := proto.ProjectInfo{ID: "tools", Path: "/src/api/tools"} // a project inside another's folder
	for _, tc := range []struct {
		dir, want string
	}{
		{"/src/api", "api"},
		{"/src/api/cmd/x", "api"},
		{"/wt/api-feat/internal", "api"}, // a worktree of it
		{"/src/api/tools", "tools"},      // the closer project wins
		{"/src/api/tools/bin", "tools"},
		{"/src/api2", ""}, // a name that only starts the same
		{"/src", ""},
		{"/", ""},
	} {
		p, ok := launchProject(tc.dir, []proto.ProjectInfo{api, tools})
		if got := map[bool]string{true: p.ID}[ok]; got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.dir, got, tc.want)
		}
	}
	if _, ok := launchProject("/src/api", nil); ok {
		t.Error("no projects matched")
	}
	if _, ok := launchProject("/src/api", []proto.ProjectInfo{{ID: "x"}}); ok {
		t.Error("a project without a path matched")
	}
}

func TestRepoRoot(t *testing.T) {
	_, repo := launchDirs(t)
	if got := repoRoot(filepath.Join(repo, "cmd", "tool")); got != repo {
		t.Fatalf("from inside: %q", got)
	}
	if got := repoRoot(repo); got != repo {
		t.Fatalf("at the top: %q", got)
	}
	// A linked worktree has a .git file rather than a directory.
	wt := filepath.Join(filepath.Dir(repo), "api-feat")
	if err := os.MkdirAll(filepath.Join(wt, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+repo+"/.git/worktrees/feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := repoRoot(filepath.Join(wt, "sub")); got != wt {
		t.Fatalf("worktree: %q", got)
	}
	// Beside the repository, not in it: whatever holds the temp folder may
	// be in git itself, but the repository beside it is never found.
	if got := repoRoot(filepath.Dir(repo)); got == repo {
		t.Fatal("outside: found the repository beside it")
	}
	if got := repoRoot(""); got != "" {
		t.Fatalf("empty: %q", got)
	}
}

func TestResolvePath(t *testing.T) {
	dir := resolvePath(t.TempDir())
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if got := resolvePath(link); got != dir {
		t.Fatalf("symlink: %q, want %q", got, dir)
	}
	if got := resolvePath(filepath.Join(dir, "missing", "..", "x")); got != filepath.Join(dir, "x") {
		t.Fatalf("missing: %q", got)
	}
	if resolvePath("") != "" {
		t.Fatal("empty")
	}
}

// Started in a project, or deep in one, the tree opens on it — once: a
// later list leaves the cursor where you put it.
func TestLaunchSelectsItsProject(t *testing.T) {
	_, repo := launchDirs(t)
	m, _ := a1Fixture(t, false)
	link := filepath.Join(filepath.Dir(repo), "api-link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	*m = m.LaunchedIn(filepath.Join(link, "cmd")) // reached through a symlink
	proj := proto.ProjectInfo{ID: "r9", Name: "api", Path: repo, Git: true}
	launchProjects(m, m.machines[0].projects[0], proj)

	if m.cursor != projectNodeID(localMachine, "r9") || m.launchDir != "" || m.overlay != nil {
		t.Fatalf("cursor %q launchDir %q overlay %v", m.cursor, m.launchDir, m.overlay)
	}
	if indexOfRow(m.rows, m.cursor) < 0 {
		t.Fatalf("project row not in the tree: %s", render(m.rows))
	}
	// The status bar now offers c for an agent in this project.
	if got := a1HintText(m); !strings.Contains(got, "c agent") {
		t.Fatalf("hints: %s", got)
	}

	m.cursor = cliID(localMachine)
	launchProjects(m, m.machines[0].projects...)
	if m.cursor != cliID(localMachine) {
		t.Fatalf("a later list moved the cursor to %q", m.cursor)
	}
}

func TestLaunchOffersTheRepository(t *testing.T) {
	_, repo := launchDirs(t)
	m, peer := a1Fixture(t, true)
	*m = m.LaunchedIn(filepath.Join(repo, "cmd", "tool"))
	launchProjects(m, m.machines[0].projects...)

	d, ok := m.overlay.(*dialog)
	if !ok || !d.confirm || !strings.Contains(strings.Join(d.text, " "), "work/api as a project?") {
		t.Fatalf("no offer: %#v", m.overlay)
	}
	// Its box fits small terminals too.
	for _, w := range []int{120, 40} { // under 30 columns: TestDialogFitsTinyTerminal
		m.width = w
		for _, l := range d.render(*m).lines {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: line %d wide", w, ansi.StringWidth(l))
			}
		}
	}
	m.width = 160

	info := proto.ProjectInfo{ID: "r9", Name: "api", Path: repo, Git: true}
	peer.setResult(proto.MethodProjectAdd, info)
	cmd := a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m.overlay != nil || cmd == nil {
		t.Fatalf("yes: overlay %v cmd %v", m.overlay, cmd)
	}
	msg := cmd()
	got, ok := msg.(launchProjectMsg)
	if !ok || got.info.ID != "r9" {
		t.Fatalf("add answered %#v", msg)
	}
	sent := peer.waitFor(t, "project.add", func(m proto.Message) bool { return m.Method == proto.MethodProjectAdd })
	var params proto.ProjectAddParams
	if err := json.Unmarshal(sent.Params, &params); err != nil || params.Path != repo {
		t.Fatalf("project.add params %+v (%v)", params, err)
	}

	launchUpdate(m, msg)
	if m.cursor != projectNodeID(localMachine, "r9") || indexOfRow(m.rows, m.cursor) < 0 {
		t.Fatalf("new project not selected: %q in %s", m.cursor, render(m.rows))
	}
	if !strings.Contains(m.flash, "added project api") {
		t.Fatalf("flash %q", m.flash)
	}
	// The server's event for it arriving after changes nothing.
	launchUpdate(m, projectsMsg{machine: localMachine, gen: m.machines[0].gen, projects: m.machines[0].projects})
	n := 0
	for _, p := range m.machines[0].projects {
		if p.ID == "r9" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("project listed %d times", n)
	}
	// Received twice — the event first, then the answer — still once.
	launchUpdate(m, launchProjectMsg{info})
	if c := len(slices.DeleteFunc(slices.Clone(m.machines[0].projects), func(p proto.ProjectInfo) bool { return p.ID != "r9" })); c != 1 {
		t.Fatalf("project listed %d times after a second answer", c)
	}
}

func TestLaunchOfferAddFails(t *testing.T) {
	_, repo := launchDirs(t)
	m, peer := a1Fixture(t, true)
	*m = m.LaunchedIn(repo)
	launchProjects(m, m.machines[0].projects...)
	peer.setError(proto.MethodProjectAdd, "not allowed")
	cmd := a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if _, ok := cmd().(errMsg); !ok {
		t.Fatal("a failed add is an error")
	}
	if m.cursor == projectNodeID(localMachine, "r9") {
		t.Fatal("selected a project that was never added")
	}
}

// No is remembered for the folder, saved and loaded again; esc only puts
// the question off.
func TestLaunchOfferNoAndEsc(t *testing.T) {
	_, repo := launchDirs(t)
	for _, answer := range []string{"esc", "n", "enter on No", "click No"} {
		m, _ := a1Fixture(t, false)
		m.statePath = filepath.Join(t.TempDir(), "ui.json")
		*m = m.LaunchedIn(repo)
		launchProjects(m, m.machines[0].projects...)
		d, ok := m.overlay.(*dialog)
		if !ok {
			t.Fatalf("%s: no offer", answer)
		}
		var cmd tea.Cmd
		switch answer {
		case "esc":
			cmd = a1Key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		case "n":
			cmd = a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
		case "enter on No":
			a1Key(t, m, tea.KeyMsg{Type: tea.KeyRight})
			cmd = a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		case "click No":
			b := d.render(*m)
			cmd = d.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
				X: b.x + 1 + d.buttons.no0, Y: b.y + 1 + d.buttons.line}, b)
		}
		if m.overlay != nil {
			t.Fatalf("%s: offer still open", answer)
		}
		if answer == "esc" {
			if len(m.noProjectOffer) != 0 {
				t.Fatalf("esc remembered %v", m.noProjectOffer)
			}
			continue
		}
		if !slices.Equal(m.noProjectOffer, []string{repo}) || cmd == nil {
			t.Fatalf("%s: remembered %v", answer, m.noProjectOffer)
		}
		cmd() // writes ui.json
		st := loadUIState(m.statePath)
		if !slices.Equal(st.NoProjectOffer, []string{repo}) {
			t.Fatalf("%s: saved %v", answer, st.NoProjectOffer)
		}
		// Next start in the same repository: no question.
		again, _ := a1Fixture(t, false)
		again.noProjectOffer = st.NoProjectOffer
		*again = again.LaunchedIn(filepath.Join(repo, "cmd"))
		launchProjects(again, again.machines[0].projects...)
		if again.overlay != nil {
			t.Fatalf("%s: asked again", answer)
		}
		// Answering No twice keeps one entry.
		m.overlay = newProjectOffer(*m, repo)
		a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
		if len(m.noProjectOffer) != 1 {
			t.Fatalf("%s: %v", answer, m.noProjectOffer)
		}
	}
}

// Nothing is offered where nobody meant a project, or when it would get
// in the way.
func TestLaunchOffersNothing(t *testing.T) {
	home, repo := launchDirs(t)
	plain := filepath.Join(filepath.Dir(home), "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o755); err != nil { // dotfiles in git
		t.Fatal(err)
	}
	for name, setup := range map[string]func(m *Model){
		"not a repository": func(m *Model) { *m = m.LaunchedIn(plain) },
		"home in git":      func(m *Model) { *m = m.LaunchedIn(filepath.Join(home)) },
		"missing folder":   func(m *Model) { *m = m.LaunchedIn(filepath.Join(plain, "gone")) },
		"never told":       func(m *Model) {},
		"another overlay": func(m *Model) {
			*m = m.LaunchedIn(repo)
			m.overlay = newNotice(" Note ", []string{"first"})
		},
		"restarted onto a new build": func(m *Model) {
			t.Setenv(updateRemotesEnv, "1")
			*m = m.LaunchedIn(repo)
			t.Setenv(updateRemotesEnv, "")
		},
	} {
		m, _ := a1Fixture(t, false)
		before := m.cursor
		setup(m)
		launchProjects(m, m.machines[0].projects...)
		if d, ok := m.overlay.(*dialog); ok && d.confirm {
			t.Errorf("%s: offered %v", name, d.text)
		}
		if m.cursor != before && name != "missing folder" {
			t.Errorf("%s: cursor moved to %q", name, m.cursor)
		}
	}

	// A remote machine's projects don't place the tree; the local list
	// still does afterwards.
	m, _ := a1Fixture(t, false)
	*m = m.LaunchedIn(repo)
	remoteM := newMachine("vm1", "vm1", "vm1")
	m.machines = append(m.machines, remoteM)
	launchUpdate(m, projectsMsg{machine: "vm1", gen: remoteM.gen, projects: []proto.ProjectInfo{{ID: "x", Path: repo}}})
	if m.overlay != nil || m.launchDir == "" {
		t.Fatalf("remote list placed the tree: overlay %v launchDir %q", m.overlay, m.launchDir)
	}
	launchProjects(m, m.machines[0].projects...)
	if m.overlay == nil {
		t.Fatal("local list after a remote one offered nothing")
	}
}

// A confirm without a decline closes on No as it always did.
func TestConfirmWithoutDecline(t *testing.T) {
	m, _ := a1Fixture(t, false)
	yes := 0
	m.overlay = newConfirm("sure?", func(m *Model) tea.Cmd { yes++; return nil })
	if cmd := a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}); cmd != nil || m.overlay != nil || yes != 0 {
		t.Fatalf("n: cmd %v overlay %v yes %d", cmd, m.overlay, yes)
	}
}

// ui.json from before the offer existed still loads.
func TestUIStateWithoutNoProjectOffer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.json")
	if err := os.WriteFile(path, []byte(`{"expanded":{"m:local":true},"sidebar_width":31}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st := loadUIState(path)
	if st.SidebarWidth != 31 || st.NoProjectOffer != nil {
		t.Fatalf("loaded %+v", st)
	}
}

// A dialog has to fit the terminal it is drawn in. It did not: the
// width had a floor of 30 whatever the screen was, so under 32 columns
// every dialog spilled over what was behind it.
func TestDialogFitsTinyTerminal(t *testing.T) {
	m, _ := a1Fixture(t, false)
	for _, w := range []int{80, 40, 34, 32, 31, 20, 10, 5, 3} {
		m.width, m.height = w, 24
		for name, d := range map[string]overlay{
			"a question": newConfirm("Delete the branch and its worktree? This cannot be undone.", func(*Model) tea.Cmd { return nil }),
			"with fields": newDialog(*m, " Rename ", []string{"What it is called in the tree."},
				[]string{"Name"}, []string{"something long enough to need the room"}),
		} {
			for i, l := range d.render(*m).lines {
				if got := ansi.StringWidth(l); got > w {
					t.Errorf("%s at %d columns: line %d is %d wide", name, w, i, got)
				}
			}
		}
	}
	// And at a width where it fits, it is still the width it should be.
	m.width, m.height = 100, 24
	if got := m.dialogWidth(); got != 72 {
		t.Errorf("a roomy terminal gives %d columns", got)
	}
	m.width = 40
	if got := m.dialogWidth(); got != 36 {
		t.Errorf("40 columns gives %d", got)
	}
}
