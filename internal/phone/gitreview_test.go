package phone

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// twenty is a.txt as committed: lines far enough apart that an edit near
// the top and one near the bottom are two hunks.
func twenty() string {
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

// reviewRepo is a repository on main pushed to a bare remote, with a
// worktree on feature one commit ahead, two separate edits to a.txt and an
// untracked file, and a gh that opens a pull request (number 7) and lists
// it from then on. It returns the main checkout, the worktree, the bare
// remote and gh's log of what it was asked.
func reviewRepo(t *testing.T, dir string) (repo, feature, bare, ghLog string) {
	t.Helper()
	state, ghLog := filepath.Join(dir, "gh-prs.json"), filepath.Join(dir, "gh.log")
	pr := `[{"number":7,"title":"T","state":"OPEN","isDraft":true,"url":"https://github.com/o/api/pull/7",` +
		`"headRefName":"feature","headRefOid":"abc","isCrossRepository":false,"baseRefName":"main",` +
		`"reviewDecision":"","mergeable":"MERGEABLE","updatedAt":"2026-11-14T00:00:00Z","statusCheckRollup":[` +
		`{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE"}]}]`
	gh := filepath.Join(dir, "fake-gh-review")
	script := "#!/bin/sh\necho \"$@\" >> " + ghLog + "\ncase \"$1 $2\" in\n" +
		"'pr list') if [ -f " + state + " ]; then cat " + state + "; else echo '[]'; fi ;;\n" +
		"'pr create') echo '" + pr + "' > " + state + "; echo 'Creating pull request'; echo https://github.com/o/api/pull/7 ;;\n" +
		"*) echo '[]' ;;\nesac\n"
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_GH", gh)

	repo, feature, bare = filepath.Join(dir, "api"), filepath.Join(dir, "api-feature"), filepath.Join(dir, "remote.git")
	os.MkdirAll(repo, 0o755)
	gitIn(t, dir, "init", "-q", "--bare", "-b", "main", bare)
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "config", "user.name", "t")
	gitIn(t, repo, "config", "user.email", "t@example.com")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte(twenty()), 0o644)
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("bee\n"), 0o644)
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "init")
	gitIn(t, repo, "remote", "add", "origin", bare)
	gitIn(t, repo, "push", "-q", "origin", "main")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "feature", feature)
	os.WriteFile(filepath.Join(feature, "b.txt"), []byte("bee\nsting\n"), 0o644)
	gitIn(t, feature, "commit", "-q", "-am", "feature work")
	a := strings.Replace(strings.Replace(twenty(), "line 2\n", "line two\n", 1), "line 18\n", "line eighteen\n", 1)
	os.WriteFile(filepath.Join(feature, "a.txt"), []byte(a), 0o644)
	os.WriteFile(filepath.Join(feature, "new.txt"), []byte("hello\nworld\n"), 0o644)
	return repo, feature, bare, ghLog
}

// reviewFixture is the repository above as a project with an agent at work
// in the feature worktree; it returns the agent as the phone names it.
func reviewFixture(t *testing.T, f *fixture) (pane, projectID, feature, bare, ghLog string) {
	t.Helper()
	repo, feature, bare, ghLog := reviewRepo(t, f.dir)
	var proj proto.ProjectInfo
	f.call(proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj)
	var agent proto.PaneInfo
	f.call(proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude",
		Command: []string{"/bin/sh", "-c", "exec sleep 60"}, Cwd: feature, Cols: 80, Rows: 24}, &agent)
	waitFor(t, "the agent on feature", func() bool {
		var list proto.PaneList
		f.call(proto.MethodPaneList, nil, &list)
		for _, pn := range list.Panes {
			if pn.ID == agent.ID && pn.Agent != nil && pn.Branch == "feature" && pn.ProjectID == proj.ID {
				return true
			}
		}
		return false
	})
	return phoneID(agent.ID), proj.ID, feature, bare, ghLog
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func diffOf(t *testing.T, p *fakePhone, pane, path, commit string) FileDiff {
	t.Helper()
	q := url.Values{"pane": {pane}, "path": {path}}
	if commit != "" {
		q.Set("commit", commit)
	}
	var d FileDiff
	if status, b := p.do("GET", "/api/diff?"+q.Encode(), nil); status != 200 {
		t.Fatalf("diff of %s: %d %s", path, status, b)
	} else if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func stagedNames(t *testing.T, dir string) string {
	t.Helper()
	return strings.Join(strings.Fields(gitOut(t, dir, "diff", "--cached", "--name-only")), ",")
}

func TestReviewChanges(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermView)
	pane, _, feature, _, _ := reviewFixture(t, f)

	var got BranchChanges
	waitFor(t, "the branch one commit ahead", func() bool {
		got = BranchChanges{}
		return p.get("/api/changes?pane="+url.QueryEscape(pane), &got) == 200 && got.Ahead == 1
	})
	if got.Pane != pane || got.Branch != "feature" || got.Base != "main" || got.Behind != 0 || got.PR != nil {
		t.Errorf("the branch %+v", got)
	}
	want := []ChangedFile{
		{Path: "a.txt", Status: FileModified, Added: 2, Removed: 2, Staged: StagedNone},
		{Path: "new.txt", Status: FileUntracked, Added: 2, Staged: StagedNone},
	}
	if !reflect.DeepEqual(got.Files, want) {
		t.Errorf("files\n got %+v\nwant %+v", got.Files, want)
	}
	head := gitOut(t, feature, "rev-parse", "HEAD")
	if len(got.Commits) != 1 || got.Commits[0].SHA != head || got.Commits[0].Short != head[:7] ||
		got.Commits[0].Subject != "feature work" || got.Commits[0].Author != "t" || got.Commits[0].At.IsZero() ||
		got.Commits[0].At.Location().String() != "UTC" {
		t.Errorf("commits %+v", got.Commits)
	}

	// Staged and part-staged, as git has them.
	gitIn(t, feature, "add", "new.txt")
	os.WriteFile(filepath.Join(feature, "new.txt"), []byte("hello\nworld\nagain\n"), 0o644)
	gitIn(t, feature, "mv", "b.txt", "c.txt")
	p.get("/api/changes?pane="+pane, &got)
	byPath := map[string]ChangedFile{}
	for _, f := range got.Files {
		byPath[f.Path] = f
	}
	if n := byPath["new.txt"]; n.Status != FileAdded || n.Staged != StagedPartial {
		t.Errorf("new.txt staged then changed: %+v", n)
	}
	if c := byPath["c.txt"]; c.Status != FileRenamed || c.OldPath != "b.txt" || c.Staged != StagedAll {
		t.Errorf("a rename: %+v", c)
	}

	// Nothing is null, and what is not there is left out.
	gitIn(t, feature, "reset", "-q", "--hard")
	gitIn(t, feature, "clean", "-qfd")
	_, raw := p.do("GET", "/api/changes?pane="+pane, nil)
	if !strings.Contains(string(raw), `"files":[]`) || strings.Contains(string(raw), "null") || strings.Contains(string(raw), `"pr"`) {
		t.Errorf("a clean branch: %s", raw)
	}

	// Asking wrongly, or about a pane on no branch.
	plain := filepath.Join(f.dir, "plain")
	os.MkdirAll(plain, 0o755)
	term := phoneID(f.termIn(plain))
	for q, code := range map[string]int{
		"/api/changes":                 400,
		"/api/changes?pane=local:p999": 404,
		"/api/changes?pane=Bad:p1":     400,
		"/api/changes?pane=" + term:    400,
	} {
		if status, b := p.do("GET", q, nil); status != code {
			t.Errorf("%s: %d %s, want %d", q, status, b, code)
		}
	}
}

func TestReviewDiff(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermView)
	pane, _, feature, _, _ := reviewFixture(t, f)

	d := diffOf(t, p, pane, "a.txt", "")
	if d.Path != "a.txt" || d.Binary || d.Truncated || len(d.Hunks) != 2 {
		t.Fatalf("a.txt %+v", d)
	}
	h0, h1 := d.Hunks[0], d.Hunks[1]
	if h0.ID == "" || h0.ID == h1.ID || h0.Staged || h1.Staged || !strings.HasPrefix(h0.Header, "@@ -1,5 +1,5 @@") {
		t.Fatalf("hunks %+v %+v", h0, h1)
	}
	wantLines := []DiffLine{{K: " ", Old: 1, New: 1, T: "line 1"}, {K: "-", Old: 2, T: "line 2"}, {K: "+", New: 2, T: "line two"},
		{K: " ", Old: 3, New: 3, T: "line 3"}, {K: " ", Old: 4, New: 4, T: "line 4"}, {K: " ", Old: 5, New: 5, T: "line 5"}}
	if !reflect.DeepEqual(h0.Lines, wantLines) {
		t.Errorf("the first hunk's lines\n got %+v\nwant %+v", h0.Lines, wantLines)
	}
	// The same worktree, the same names.
	if again := diffOf(t, p, pane, "a.txt", ""); again.Hunks[0].ID != h0.ID || again.Hunks[1].ID != h1.ID {
		t.Error("hunk ids moved while nothing changed")
	}

	// An untracked file is one hunk of what it holds.
	n := diffOf(t, p, pane, "new.txt", "")
	if len(n.Hunks) != 1 || !reflect.DeepEqual(n.Hunks[0].Lines, []DiffLine{{K: "+", New: 1, T: "hello"}, {K: "+", New: 2, T: "world"}}) {
		t.Errorf("new.txt %+v", n)
	}
	// A commit's diff, by its SHA.
	head := gitOut(t, feature, "rev-parse", "HEAD")
	c := diffOf(t, p, pane, "b.txt", head)
	if len(c.Hunks) != 1 || !reflect.DeepEqual(c.Hunks[0].Lines, []DiffLine{{K: " ", Old: 1, New: 1, T: "bee"}, {K: "+", New: 2, T: "sting"}}) {
		t.Errorf("the commit's b.txt %+v", c)
	}
	// A file with nothing uncommitted is no hunks.
	if b := diffOf(t, p, pane, "b.txt", ""); len(b.Hunks) != 0 || b.Hunks == nil {
		t.Errorf("b.txt uncommitted %+v", b)
	}
	// A binary file says so.
	os.WriteFile(filepath.Join(feature, "logo.bin"), []byte("PNG\x00\x01\x02"), 0o644)
	gitIn(t, feature, "add", "logo.bin")
	if b := diffOf(t, p, pane, "logo.bin", ""); !b.Binary || len(b.Hunks) != 0 {
		t.Errorf("a binary file %+v", b)
	}

	// A file longer than the phone takes is cut at maxDiffLines.
	var big strings.Builder
	for i := range maxDiffLines + 500 {
		fmt.Fprintf(&big, "%d\n", i)
	}
	os.WriteFile(filepath.Join(feature, "big.txt"), []byte(big.String()), 0o644)
	if b := diffOf(t, p, pane, "big.txt", ""); !b.Truncated || len(b.Hunks) != 1 || len(b.Hunks[0].Lines) != maxDiffLines {
		t.Errorf("big.txt: truncated %v, %d hunks", b.Truncated, len(b.Hunks))
	}

	for q, code := range map[string]int{
		"/api/diff?pane=" + pane:                                    400,
		"/api/diff?pane=" + pane + "&path=-x":                       400,
		"/api/diff?pane=" + pane + "&path=" + url.QueryEscape(":/"): 400,
		"/api/diff?pane=" + pane + "&path=../etc/passwd":            400,
		"/api/diff?pane=" + pane + "&path=a.txt&commit=HEAD~1":      400,
		"/api/diff?pane=local:p999&path=a.txt":                      404,
	} {
		if status, b := p.do("GET", q, nil); status != code {
			t.Errorf("%s: %d %s, want %d", q, status, b, code)
		}
	}
}

// A diff whose last hunk is cut short drops it; the server's own mark of
// a cut diff is taken; a removed line that reads like a header is a line.
func TestParseDiff(t *testing.T) {
	d := parseDiff("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n--- a/y\n+b\n x\n\\ No newline at end of file\n@@ -9,3 +9,3 @@\n a\n")
	if !d.truncated || len(d.hunks) != 1 {
		t.Fatalf("cut short: %+v", d)
	}
	h := d.hunks[0]
	if !reflect.DeepEqual(h.Lines, []DiffLine{{K: "-", Old: 1, T: "-- a/y"}, {K: "+", New: 1, T: "b"}, {K: " ", Old: 2, New: 2, T: "x"}}) ||
		!strings.HasSuffix(h.text, "\\ No newline at end of file\n") || h.preamble != "diff --git a/x b/x\n--- a/x\n+++ b/x\n" {
		t.Fatalf("the hunk %+v", h)
	}
	if d := parseDiff("diff --git a/x b/x\n@@ -1 +1 @@\n-a\n+b\n\n… diff truncated\n"); !d.truncated || len(d.hunks) != 1 {
		t.Fatalf("the server's cut: %+v", d)
	}
	if d := parseDiff("diff --git a/o b/n\nsimilarity index 90%\nrename from o\nrename to n\n"); d.oldPath != "o" || len(d.hunks) != 0 {
		t.Fatalf("a rename %+v", d)
	}
	if d := parseDiff(""); d.truncated || d.hunks != nil {
		t.Fatalf("nothing %+v", d)
	}
	for up, want := range map[string]string{"": "origin", "fork/feature": "fork", "a/b/feature": "a/b"} {
		if got := pushRemote(up, []string{"origin", "fork", "a", "a/b"}); got != want {
			t.Errorf("upstream %q: %q, want %q", up, got, want)
		}
	}
	if pushRemote("", []string{"mine"}) != "mine" || pushRemote("up/x", nil) != "up" {
		t.Error("the only remote, or the upstream's when none are known")
	}
}

func TestReviewStage(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermFull)
	pane, _, feature, _, _ := reviewFixture(t, f)
	stage := func(req StageRequest) StageResponse {
		t.Helper()
		req.Pane = pane
		var res StageResponse
		if status, e := p.post("/api/stage", req, &res); status != 200 {
			t.Fatalf("stage %+v: %d %+v", req, status, e)
		}
		return res
	}

	// One hunk: the file is part staged, and its diff is what is staged
	// and what is not.
	d := diffOf(t, p, pane, "a.txt", "")
	if res := stage(StageRequest{Path: "a.txt", Hunk: d.Hunks[0].ID}); res != (StageResponse{Pane: pane, Path: "a.txt", Staged: StagedPartial}) {
		t.Fatalf("a hunk staged: %+v", res)
	}
	if got := gitOut(t, feature, "diff", "--cached", "--", "a.txt"); !strings.Contains(got, "+line two") || strings.Contains(got, "eighteen") {
		t.Fatalf("the index: %s", got)
	}
	part := diffOf(t, p, pane, "a.txt", "")
	if len(part.Hunks) != 2 || !part.Hunks[0].Staged || part.Hunks[1].Staged || part.Hunks[1].Lines[3].T != "line 18" {
		t.Fatalf("part staged %+v", part.Hunks)
	}
	// Staging what is staged changes nothing; a hunk read before is gone.
	if res := stage(StageRequest{Path: "a.txt", Hunk: part.Hunks[0].ID}); res.Staged != StagedPartial {
		t.Fatalf("again: %+v", res)
	}
	if status, e := p.post("/api/stage", StageRequest{Pane: pane, Path: "a.txt", Hunk: d.Hunks[1].ID + "x"}, nil); status != 409 || e.Code != CodeHunkChanged {
		t.Fatalf("a stale hunk: %d %+v", status, e)
	}
	// Taken back out by its own id.
	if res := stage(StageRequest{Path: "a.txt", Hunk: part.Hunks[0].ID, Unstage: true}); res.Staged != StagedNone || stagedNames(t, feature) != "" {
		t.Fatalf("unstaged: %+v, index %q", res, stagedNames(t, feature))
	}

	// The whole file, then one hunk of it back out.
	if res := stage(StageRequest{Path: "a.txt"}); res.Staged != StagedAll {
		t.Fatalf("the file: %+v", res)
	}
	all := diffOf(t, p, pane, "a.txt", "")
	if len(all.Hunks) != 2 || !all.Hunks[0].Staged || !all.Hunks[1].Staged {
		t.Fatalf("all staged %+v", all.Hunks)
	}
	if res := stage(StageRequest{Path: "a.txt", Hunk: all.Hunks[1].ID, Unstage: true}); res.Staged != StagedPartial {
		t.Fatalf("one hunk out: %+v", res)
	}
	if got := gitOut(t, feature, "diff", "--", "a.txt"); !strings.Contains(got, "+line eighteen") || strings.Contains(got, "line two") {
		t.Fatalf("what is left unstaged: %s", got)
	}
	if res := stage(StageRequest{Path: "a.txt", Unstage: true}); res.Staged != StagedNone {
		t.Fatalf("the file out: %+v", res)
	}

	// An untracked file staged by its one hunk is the file.
	n := diffOf(t, p, pane, "new.txt", "")
	if res := stage(StageRequest{Path: "new.txt", Hunk: n.Hunks[0].ID}); res.Staged != StagedAll || stagedNames(t, feature) != "new.txt" {
		t.Fatalf("new.txt: %+v, index %q", res, stagedNames(t, feature))
	}

	for _, c := range []struct {
		req    StageRequest
		status int
		code   string
	}{
		{StageRequest{Pane: pane, Path: "b.txt"}, 404, CodeNotFound}, // nothing uncommitted
		{StageRequest{Pane: pane}, 400, CodeBadRequest},
		{StageRequest{Pane: pane, Path: ":/"}, 400, CodeBadRequest},
		{StageRequest{Path: "a.txt"}, 400, CodeBadRequest},
		{StageRequest{Pane: "local:p999", Path: "a.txt"}, 404, CodeNotFound},
	} {
		if status, e := p.post("/api/stage", c.req, nil); status != c.status || e.Code != c.code {
			t.Errorf("%+v: %d %+v", c.req, status, e)
		}
	}
	if status, _ := p.do("POST", "/api/stage", "not an object"); status != 400 {
		t.Errorf("bad JSON: %d", status)
	}
}

func TestReviewNote(t *testing.T) {
	f := newFixture(t)
	view, p := f.pair(PermView), f.pair(PermReply)
	pane, proj, _, _, _ := reviewFixture(t, f)

	var note ReviewNote
	if status, e := p.post("/api/review/note", NoteRequest{Pane: pane, Path: "a.txt", Line: 2, Note: " rename it "}, &note); status != 200 {
		t.Fatalf("a note: %d %+v", status, e)
	}
	if note.ID == "" || note.Where != "a.txt:2" || note.Note != "rename it" || note.By != "" {
		t.Fatalf("the note %+v", note)
	}
	// The server keeps it with the hunk, for the agent.
	var list proto.ReviewList
	f.call(proto.MethodReviewList, proto.ReviewRef{ProjectID: proj, Branch: "feature"}, &list)
	if len(list.Comments) != 1 || list.Comments[0].Line != 2 || !strings.HasPrefix(list.Comments[0].Hunk, "@@ -1,5 +1,5 @@") {
		t.Fatalf("the server's %+v", list.Comments)
	}
	// On the old side: the hunk, and the line named.
	var old ReviewNote
	if status, e := p.post("/api/review/note", NoteRequest{Pane: pane, Path: "a.txt", Line: 18, Side: "old", Note: "why?"}, &old); status != 200 {
		t.Fatalf("an old line: %d %+v", status, e)
	}
	if !strings.HasPrefix(old.Where, "a.txt @@ -15,6 +15,6 @@") || old.Note != "(line 18 before the change) why?" {
		t.Fatalf("the old line's note %+v", old)
	}
	// The agent's list has both, ready for /api/review/send.
	var mine *Agent
	for _, a := range view.agents() {
		if a.Pane == pane {
			mine = &a
		}
	}
	if mine == nil || len(mine.Review) != 2 || mine.Review[0].ID != note.ID || mine.Review[1].ID != old.ID {
		t.Fatalf("the agent's review %+v", mine)
	}

	for _, c := range []struct {
		req    NoteRequest
		status int
	}{
		{NoteRequest{Pane: pane, Path: "a.txt", Line: 0, Note: "x"}, 400},
		{NoteRequest{Pane: pane, Path: "a.txt", Line: 2, Note: "  "}, 400},
		{NoteRequest{Pane: pane, Path: "a.txt", Line: 2, Side: "left", Note: "x"}, 400},
		{NoteRequest{Pane: pane, Line: 2, Note: "x"}, 400},
		{NoteRequest{Pane: "local:p999", Path: "a.txt", Line: 2, Note: "x"}, 404},
	} {
		if status, e := p.post("/api/review/note", c.req, nil); status != c.status {
			t.Errorf("%+v: %d %+v", c.req, status, e)
		}
	}
}

func TestReviewCommitPushAndPR(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermFull)
	pane, _, feature, bare, ghLog := reviewFixture(t, f)

	if status, e := p.post("/api/commit", CommitRequest{Pane: pane, Message: "x"}, nil); status != 400 || !strings.Contains(e.Message, "nothing to commit") {
		t.Fatalf("nothing staged: %d %+v", status, e)
	}
	if status, e := p.post("/api/commit", CommitRequest{Pane: pane, Message: " \n", All: true}, nil); status != 400 || e.Code != CodeBadRequest {
		t.Fatalf("no message: %d %+v", status, e)
	}
	// What is staged, and only that.
	gitIn(t, feature, "add", "a.txt")
	var c CommitResponse
	if status, e := p.post("/api/commit", CommitRequest{Pane: pane, Message: "Rename the lines\n\nSo they read."}, &c); status != 200 {
		t.Fatalf("commit: %d %+v", status, e)
	}
	head := gitOut(t, feature, "rev-parse", "HEAD")
	if c != (CommitResponse{Pane: pane, SHA: head, Short: head[:7], Subject: "Rename the lines"}) {
		t.Fatalf("the commit %+v (HEAD %s)", c, head)
	}
	if got := gitOut(t, feature, "status", "--porcelain"); got != "?? new.txt" {
		t.Fatalf("after the commit: %q", got)
	}
	// Everything, the untracked file too.
	if status, e := p.post("/api/commit", CommitRequest{Pane: pane, Message: "Add new", All: true}, &c); status != 200 || c.Subject != "Add new" {
		t.Fatalf("commit all: %d %+v %+v", status, e, c)
	}
	if got := gitOut(t, feature, "status", "--porcelain"); got != "" {
		t.Fatalf("after committing all: %q", got)
	}

	var pushed PushResponse
	if status, e := p.post("/api/push", PushRequest{Pane: pane}, &pushed); status != 200 {
		t.Fatalf("push: %d %+v", status, e)
	}
	if pushed != (PushResponse{Pane: pane, Branch: "feature", Remote: "origin"}) {
		t.Fatalf("pushed %+v", pushed)
	}
	if got := gitOut(t, bare, "rev-parse", "refs/heads/feature"); got != gitOut(t, feature, "rev-parse", "HEAD") {
		t.Fatalf("the remote has %s", got)
	}

	var pr PRResponse
	if status, e := p.post("/api/pr", PRRequest{Pane: pane, Title: "T", Body: "b", Draft: true}, &pr); status != 200 {
		t.Fatalf("pr: %d %+v", status, e)
	}
	if pr != (PRResponse{Pane: pane, Number: 7, URL: "https://github.com/o/api/pull/7"}) {
		t.Fatalf("the pull request %+v", pr)
	}
	// Once conch has read it, it is the branch's, and asking again returns
	// it rather than opening another.
	var ch BranchChanges
	waitFor(t, "the branch's pull request", func() bool {
		ch = BranchChanges{}
		return p.get("/api/changes?pane="+pane, &ch) == 200 && ch.PR != nil
	})
	if want := (&BranchPR{Number: 7, URL: "https://github.com/o/api/pull/7", State: PRDraft,
		Checks: []PRCheck{{Name: "test", State: "fail"}}}); !reflect.DeepEqual(ch.PR, want) {
		t.Fatalf("its pull request %+v", ch.PR)
	}
	var again PRResponse
	if status, e := p.post("/api/pr", PRRequest{Pane: pane}, &again); status != 200 || again != pr {
		t.Fatalf("again: %d %+v %+v", status, e, again)
	}
	log, _ := os.ReadFile(ghLog)
	if n := strings.Count(string(log), "pr create"); n != 1 || !strings.Contains(string(log), "--title T --body b --draft") {
		t.Fatalf("gh was asked:\n%s", log)
	}
}

// What each permission may do: view reads, reply writes notes, and only
// full changes the repository. Every change needs the CSRF token.
func TestReviewPermissions(t *testing.T) {
	f := newFixture(t)
	pane, _, feature, _, _ := reviewFixture(t, f)
	changes := []struct {
		path string
		body any
	}{
		{"/api/stage", StageRequest{Pane: pane, Path: "a.txt"}},
		{"/api/commit", CommitRequest{Pane: pane, Message: "x", All: true}},
		{"/api/push", PushRequest{Pane: pane}},
		{"/api/pr", PRRequest{Pane: pane}},
	}
	for _, perm := range []string{PermView, PermReply} {
		p := f.pair(perm)
		if status := p.get("/api/changes?pane="+pane, nil); status != 200 {
			t.Errorf("%s reading the changes: %d", perm, status)
		}
		for _, c := range changes {
			if status, e := p.post(c.path, c.body, nil); status != 403 || e.Code != CodeForbidden {
				t.Errorf("%s %s: %d %+v", perm, c.path, status, e)
			}
		}
	}
	view := f.pair(PermView)
	if status, e := view.post("/api/review/note", NoteRequest{Pane: pane, Path: "a.txt", Line: 2, Note: "x"}, nil); status != 403 || e.Code != CodeForbidden {
		t.Errorf("view writing a note: %d %+v", status, e)
	}
	if status, _ := f.pair(PermReply).post("/api/review/note", NoteRequest{Pane: pane, Path: "a.txt", Line: 2, Note: "x"}, nil); status != 200 {
		t.Errorf("reply writing a note: %d", status)
	}
	full := f.pair(PermFull)
	noCSRF := *full
	noCSRF.csrf = ""
	for _, c := range changes {
		if status, e := noCSRF.post(c.path, c.body, nil); status != 403 || !strings.Contains(e.Message, CSRFHeader) {
			t.Errorf("%s without the CSRF token: %d %+v", c.path, status, e)
		}
	}
	if status, e := noCSRF.post("/api/review/note", NoteRequest{Pane: pane, Path: "a.txt", Line: 2, Note: "x"}, nil); status != 403 {
		t.Errorf("a note without the CSRF token: %d %+v", status, e)
	}
	for _, path := range []string{"/api/stage", "/api/commit", "/api/push", "/api/pr", "/api/review/note"} {
		if status, _ := full.do("GET", path+"?pane="+pane, nil); status != 405 {
			t.Errorf("GET %s: %d", path, status)
		}
	}
	if got := gitOut(t, feature, "log", "-1", "--format=%s"); got != "feature work" || stagedNames(t, feature) != "" {
		t.Fatalf("a refused change changed something: %q, index %q", got, stagedNames(t, feature))
	}
}

// A pane on another machine is reviewed through that machine's own conch,
// for a device given it; one whose conch cannot stage says so by name.
func TestReviewOnAnotherMachine(t *testing.T) {
	f := newFixture(t)
	other := addMachine(t, f, "busybox", true, runningAnOlderConch(proto.CapBranchStage))
	p := f.pair(PermFull)
	dir, _ := os.MkdirTemp(f.dir, "remote")
	repo, feature, _, _ := reviewRepo(t, dir)
	var proj proto.ProjectInfo
	callOn(t, other, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj)
	var info proto.PaneInfo
	callOn(t, other, proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", "exec sleep 60"}, Cwd: feature, Cols: 80, Rows: 24}, &info)
	pane := "busybox:" + info.ID

	if status, b := p.do("GET", "/api/changes?pane="+pane, nil); status != 403 {
		t.Fatalf("a machine the device wasn't given: %d %s", status, b)
	}
	f.grant(p, "busybox")
	waitMachine(t, f, p, "busybox", MachineOnline)
	var got BranchChanges
	waitFor(t, "busybox's terminal on feature", func() bool {
		got = BranchChanges{}
		return p.get("/api/changes?pane="+pane, &got) == 200
	})
	if got.Pane != pane || got.Branch != "feature" || len(got.Files) != 2 || len(got.Commits) != 1 {
		t.Fatalf("busybox's changes %+v", got)
	}
	if d := diffOf(t, p, pane, "a.txt", ""); len(d.Hunks) != 2 {
		t.Fatalf("busybox's diff %+v", d)
	}
	// The same id here is another pane, or none.
	if status, _ := p.do("GET", "/api/changes?pane=local:"+info.ID, nil); status != 404 {
		t.Fatalf("local:%s: %d", info.ID, status)
	}
	// Its conch predates staging: refused, by name, before anything is done.
	status, e := p.post("/api/stage", StageRequest{Pane: pane, Path: "a.txt"}, nil)
	if status != 503 || e.Code != CodeServerUnavailable || !strings.Contains(e.Message, "busybox") {
		t.Fatalf("staging on an older conch: %d %+v", status, e)
	}
	if status, e := p.post("/api/commit", CommitRequest{Pane: pane, Message: "x"}, nil); status != 503 || !strings.Contains(e.Message, "busybox") {
		t.Fatalf("committing what is staged on an older conch: %d %+v", status, e)
	}
	if stagedNames(t, feature) != "" {
		t.Fatal("something was staged")
	}
}
