package gitx

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A branch merged back and one still open, a tag, and HEAD: every commit
// once, children before parents, with parents and refs as git has them.
func TestLogDrawsTheGraph(t *testing.T) {
	root := newRepo(t) // "initial" on main
	git(t, root, "tag", "v1")
	git(t, root, "checkout", "-q", "-b", "feat")
	commit(t, root, "a.txt", "a\n", "feat one")
	commit(t, root, "a.txt", "aa\n", "feat two")
	git(t, root, "checkout", "-q", "main")
	commit(t, root, "b.txt", "b\n", "main one")
	git(t, root, "merge", "-q", "--no-ff", "-m", "merge feat", "feat")
	git(t, root, "checkout", "-q", "-b", "open")
	commit(t, root, "c.txt", "c\n", "open one")
	git(t, root, "checkout", "-q", "main")

	commits, more, err := Log(ctx, root, 100)
	if err != nil || more {
		t.Fatalf("Log: %v, more %v", err, more)
	}
	var subjects []string
	at := map[string]int{}
	for i, c := range commits {
		subjects = append(subjects, c.Subject)
		at[c.Hash] = i
	}
	if len(commits) != 6 {
		t.Fatalf("subjects %q", subjects)
	}
	for i, c := range commits {
		for _, p := range c.Parents {
			if j, ok := at[p]; ok && j <= i {
				t.Errorf("%q comes after its parent", c.Subject)
			}
		}
		if c.Author != "Tester" || c.Time.IsZero() || len(c.Hash) != 40 {
			t.Errorf("commit %+v", c)
		}
	}
	find := func(s string) LogCommit {
		for _, c := range commits {
			if c.Subject == s {
				return c
			}
		}
		t.Fatalf("no %q in %q", s, subjects)
		return LogCommit{}
	}
	if m := find("merge feat"); len(m.Parents) != 2 || !slices.Contains(m.Refs, "HEAD -> main") {
		t.Errorf("merge: %+v", m)
	}
	if o := find("open one"); !slices.Contains(o.Refs, "open") || len(o.Parents) != 1 {
		t.Errorf("open: %+v", o)
	}
	if i := find("initial"); len(i.Parents) != 0 || !slices.Contains(i.Refs, "tag: v1") {
		t.Errorf("root: %+v", i)
	}
	if f := find("feat two"); !slices.Contains(f.Refs, "feat") {
		t.Errorf("feat: %+v", f)
	}

	// A limit keeps the newest and says there is more; exactly enough
	// says there is not.
	if cs, more, _ := Log(ctx, root, 2); len(cs) != 2 || !more || cs[0].Hash != commits[0].Hash {
		t.Errorf("limit 2: %d commits, more %v", len(cs), more)
	}
	if cs, more, _ := Log(ctx, root, 6); len(cs) != 6 || more {
		t.Errorf("limit 6: %d commits, more %v", len(cs), more)
	}
	if cs, more, err := Log(ctx, root, 0); cs != nil || more || err != nil {
		t.Errorf("limit 0: %v %v %v", cs, more, err)
	}
}

// A subject with the separators a log could be confused by, and a
// commit with a body, still come back whole and one line.
func TestLogKeepsOddSubjects(t *testing.T) {
	root := newRepo(t)
	write(t, root, "x", "x\n")
	git(t, root, "add", "x")
	git(t, root, "commit", "-q", "-m", "a, b -> c | d", "-m", "a body\nwith lines")
	commits, _, err := Log(ctx, root, 10)
	if err != nil || len(commits) != 2 || commits[0].Subject != "a, b -> c | d" {
		t.Fatalf("%v %+v", err, commits)
	}
}

// A repository with nothing committed has no history, and a folder that
// is not a repository is an error.
func TestLogEmptyAndNotARepo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty")
	os.MkdirAll(dir, 0o755)
	git(t, dir, "init", "-q", "-b", "main")
	if cs, more, err := Log(ctx, dir, 10); cs != nil || more || err != nil {
		t.Errorf("empty repo: %v %v %v", cs, more, err)
	}
	// An unborn HEAD beside a branch that has commits still shows them.
	root := newRepo(t)
	git(t, root, "checkout", "-q", "--orphan", "fresh")
	if cs, _, err := Log(ctx, root, 10); err != nil || len(cs) != 1 || cs[0].Subject != "initial" {
		t.Errorf("unborn HEAD: %v %+v", err, cs)
	}
	if _, _, err := Log(ctx, t.TempDir(), 10); err == nil {
		t.Error("not a repository: no error")
	}
}

// A subject holding the field or record separators keeps its commit, and
// the commit's child keeps its parent: they once split the record, the
// commit was lost and the graph's lane ran off the bottom.
func TestLogSubjectWithSeparators(t *testing.T) {
	root := newRepo(t) // "initial"
	commit(t, root, "a", "a\n", "two \x1f with US")
	commit(t, root, "b", "b\n", "three \x1e with RS")
	commit(t, root, "c", "c\n", "four")
	commits, _, err := Log(ctx, root, 10)
	if err != nil || len(commits) != 4 {
		t.Fatalf("%v: %d commits %+v", err, len(commits), commits)
	}
	// The separators themselves are drawn as dots: they are controls.
	if commits[1].Subject != "three · with RS" || commits[2].Subject != "two · with US" {
		t.Errorf("subjects %q / %q", commits[1].Subject, commits[2].Subject)
	}
	for i := 0; i < 3; i++ {
		if len(commits[i].Parents) != 1 || commits[i].Parents[0] != commits[i+1].Hash {
			t.Errorf("commit %d's parent is not commit %d", i, i+1)
		}
	}
}

// A shallow clone's boundary is decorated "grafted", which is no ref; and
// a repository's remotes are named, whatever they are called.
func TestLogShallowAndRemotes(t *testing.T) {
	src := newRepo(t)
	commit(t, src, "a", "a\n", "two")
	commit(t, src, "b", "b\n", "three")
	dst := filepath.Join(t.TempDir(), "shallow")
	git(t, filepath.Dir(dst), "clone", "-q", "--depth", "2", "file://"+src, dst)
	git(t, dst, "remote", "rename", "origin", "fork")
	commits, _, err := Log(ctx, dst, 10)
	if err != nil || len(commits) != 2 {
		t.Fatalf("%v %+v", err, commits)
	}
	for _, c := range commits {
		if slices.Contains(c.Refs, "grafted") {
			t.Errorf("%s: refs %q", c.Subject, c.Refs)
		}
	}
	if r := Remotes(ctx, dst); !slices.Equal(r, []string{"fork"}) {
		t.Errorf("remotes %q", r)
	}
	if r := Remotes(ctx, src); len(r) != 0 {
		t.Errorf("no remotes: %q", r)
	}
}
