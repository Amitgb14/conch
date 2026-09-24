package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func mvGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func mvWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// moveFix is two servers standing in for two machines: src has project api
// with worktree feat (two commits past main); dst has a clone of api made
// before feat existed.
type moveFix struct {
	src, dst         *Server
	srcProj, dstProj *project
	root, wt, other  string
}

func moveFixture(t *testing.T) moveFix {
	t.Helper()
	a5IsolateEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	var f moveFix
	var sdir, ddir string
	f.src, sdir = a5Server(t)
	f.dst, ddir = a5Server(t)
	// No file watches: stopping them takes seconds, and nothing here needs them.
	f.src.watcher, f.dst.watcher = nil, nil
	f.root = filepath.Join(sdir, "api")
	a5GitRepo(t, f.root)
	mvWrite(t, f.root, "README.md", "hello\n")
	mvWrite(t, f.root, ".gitignore", ".env\n*.log\n")
	mvGit(t, f.root, "add", ".")
	mvGit(t, f.root, "commit", "-q", "-m", "readme")

	f.other = filepath.Join(ddir, "api")
	mvGit(t, ddir, "clone", "-q", f.root, f.other)

	f.wt = filepath.Join(sdir, "api.worktrees", "feat")
	mvGit(t, f.root, "worktree", "add", "-q", "-b", "feat", f.wt)
	for _, n := range []string{"one", "two"} {
		mvWrite(t, f.wt, n+".txt", n+"\n")
		mvGit(t, f.wt, "add", ".")
		mvGit(t, f.wt, "commit", "-q", "-m", n)
	}
	f.srcProj = mvProject(t, f.src, f.root)
	f.dstProj = mvProject(t, f.dst, f.other)
	return f
}

// mvProject adds a project and loads it, as the server's ticker would.
func mvProject(t *testing.T, s *Server, root string) *project {
	t.Helper()
	p, err := s.projects.add(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.common != "" { // a plain folder isn't refreshed like a repository
		s.projects.refresh(p, "")
	}
	return p
}

// mvDirty leaves uncommitted work in feat: a staged edit, an unstaged one
// on top, an untracked file in a folder, an untracked symlink, a local file
// git ignores and a log git ignores that isn't a local file.
func mvDirty(t *testing.T, wt string) {
	t.Helper()
	mvWrite(t, wt, "one.txt", "one\nstaged\n")
	mvGit(t, wt, "add", "one.txt")
	mvWrite(t, wt, "one.txt", "one\nstaged\nunstaged\n")
	mvWrite(t, wt, "notes/todo.md", "- move\n")
	os.Symlink("notes/todo.md", filepath.Join(wt, "todo-link"))
	mvWrite(t, wt, ".env", "SECRET=1\n")
	mvWrite(t, wt, "debug.log", "noise\n")
	os.Chmod(filepath.Join(wt, "notes", "todo.md"), 0o755)
}

// mvPack packs feat for a machine that has commit have and reads it back.
func mvPack(t *testing.T, f moveFix, have string) []byte {
	t.Helper()
	pk, perr := f.src.packWorktree(proto.WorktreePackParams{ProjectID: f.srcProj.id, Path: f.wt, Have: have})
	if perr != nil {
		t.Fatal(perr)
	}
	var data []byte
	for {
		c, perr := f.src.packs.read(proto.WorktreePackReadParams{ID: pk.ID, Offset: int64(len(data))})
		if perr != nil {
			t.Fatal(perr)
		}
		data = append(data, c.Data...)
		if c.EOF {
			break
		}
	}
	if int64(len(data)) != pk.Size || pk.Name != "feat.tar.gz" {
		t.Fatalf("read %d of %d bytes (%s)", len(data), pk.Size, pk.Name)
	}
	return data
}

// mvUpload stores data where fs.upload would on s.
func mvUpload(t *testing.T, s *Server, data []byte) string {
	t.Helper()
	dir := filepath.Join(s.uploads.dir, "today", randomHex(4))
	os.MkdirAll(dir, 0o700)
	p := filepath.Join(dir, "feat.tar.gz")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMoveWorktreeRoundTrip(t *testing.T) {
	f := moveFixture(t)
	mvDirty(t, f.wt)

	info, perr := f.src.describeWorktree(proto.WorktreeRef{ProjectID: f.srcProj.id, Path: f.wt})
	if perr != nil {
		t.Fatal(perr)
	}
	head := mvGit(t, f.wt, "rev-parse", "HEAD")
	if info.Branch != "feat" || info.Head != head || info.Base != "main" || info.Remote != "" || len(info.History) != 4 ||
		info.Staged != 1 || info.Unstaged != 1 || info.Other != 2 || strings.Join(info.Local, ",") != ".env" {
		t.Fatalf("describe: %+v", info)
	}

	// The other machine has main's commit, the newest of feat's history it knows.
	have, perr := f.dst.haveCommits(proto.WorktreeHaveParams{ProjectID: f.dstProj.id, Commits: info.History})
	main := mvGit(t, f.root, "rev-parse", "main")
	if perr != nil || len(have.Have) != 2 || have.Have[0] != main {
		t.Fatalf("have: %+v %v", have, perr)
	}

	data := mvPack(t, f, have.Have[0])
	// The pack is gone from the source once read, and so is its folder's content.
	if entries, _ := os.ReadDir(f.src.packs.dir); len(entries) != 0 {
		t.Fatalf("left in %s: %v", f.src.packs.dir, entries)
	}
	pack := mvUpload(t, f.dst, data)
	res, perr := f.dst.unpackWorktree(proto.WorktreeUnpackParams{ProjectID: f.dstProj.id, Pack: pack})
	if perr != nil {
		t.Fatal(perr)
	}
	if res.Branch != "feat" || !strings.HasPrefix(res.Path, f.other) || strings.Join(res.Files, ",") != "notes/todo.md,todo-link,.env" {
		t.Fatalf("unpack: %+v", res)
	}
	if _, err := os.Stat(pack); !os.IsNotExist(err) {
		t.Fatal("the uploaded pack was kept")
	}

	// Same commits, same work in the same state.
	if got := mvGit(t, res.Path, "rev-parse", "HEAD"); got != head {
		t.Fatalf("head %s, want %s", got, head)
	}
	if got := mvGit(t, res.Path, "diff", "--cached", "--name-only"); got != "one.txt" {
		t.Fatalf("staged: %q", got)
	}
	if got := mvGit(t, res.Path, "diff"); !strings.Contains(got, "+unstaged") || strings.Contains(got, "+staged") {
		t.Fatalf("unstaged: %s", got)
	}
	for name, want := range map[string]string{"one.txt": "one\nstaged\nunstaged\n", "notes/todo.md": "- move\n", ".env": "SECRET=1\n"} {
		if b, err := os.ReadFile(filepath.Join(res.Path, name)); err != nil || string(b) != want {
			t.Errorf("%s: %q %v", name, b, err)
		}
	}
	if st, _ := os.Stat(filepath.Join(res.Path, "notes", "todo.md")); st == nil || st.Mode().Perm() != 0o755 {
		t.Errorf("mode not kept: %v", st)
	}
	if l, err := os.Readlink(filepath.Join(res.Path, "todo-link")); err != nil || l != "notes/todo.md" {
		t.Errorf("symlink: %q %v", l, err)
	}
	if _, err := os.Stat(filepath.Join(res.Path, "debug.log")); !os.IsNotExist(err) {
		t.Error("an ignored file that isn't a local file moved")
	}
	// The incoming ref is cleaned up; the source is untouched.
	if out, _ := exec.Command("git", "-C", f.other, "for-each-ref", "refs/conch").Output(); len(out) != 0 {
		t.Errorf("left refs: %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(f.wt, "one.txt")); string(b) != "one\nstaged\nunstaged\n" {
		t.Error("the source worktree changed")
	}
	// The project here knows its new worktree.
	f.dst.projects.refresh(f.dstProj, "")
	if wt, ok := f.dstProj.branchWorktree("feat"); !ok || wt.Path != res.Path {
		t.Fatalf("worktrees: %+v", f.dstProj.snapshot().Worktrees)
	}

	// Moving it in again: the branch is checked out there now.
	pack = mvUpload(t, f.dst, mvPack(t, f, main))
	if _, perr := f.dst.unpackWorktree(proto.WorktreeUnpackParams{ProjectID: f.dstProj.id, Pack: pack}); perr == nil ||
		!strings.Contains(perr.Message, "already checked out") {
		t.Fatalf("second move: %v", perr)
	}
}

func TestMoveWorktreeHistoryCases(t *testing.T) {
	// The other machine already has every commit (the branch was pushed):
	// no bundle, just the branch and work.
	f := moveFixture(t)
	mvGit(t, f.other, "fetch", "-q", "origin", "feat:refs/remotes/origin/feat")
	head := mvGit(t, f.wt, "rev-parse", "HEAD")
	res, perr := f.dst.unpackWorktree(proto.WorktreeUnpackParams{ProjectID: f.dstProj.id, Pack: mvUpload(t, f.dst, mvPack(t, f, head))})
	if perr != nil || mvGit(t, res.Path, "rev-parse", "HEAD") != head {
		t.Fatalf("nothing to bundle: %+v %v", res, perr)
	}

	// None of the history in common: the whole branch travels.
	f = moveFixture(t)
	stranger := filepath.Join(t.TempDir(), "stranger")
	a5GitRepo(t, stranger)
	sp := mvProject(t, f.dst, stranger)
	if have, _ := f.dst.haveCommits(proto.WorktreeHaveParams{ProjectID: sp.id, Commits: []string{head}}); len(have.Have) != 0 || have.Have == nil {
		t.Fatalf("stranger has: %+v", have)
	}
	res, perr = f.dst.unpackWorktree(proto.WorktreeUnpackParams{ProjectID: sp.id, Pack: mvUpload(t, f.dst, mvPack(t, f, ""))})
	if perr != nil || mvGit(t, res.Path, "rev-parse", "HEAD") != mvGit(t, f.wt, "rev-parse", "HEAD") {
		t.Fatalf("full history: %+v %v", res, perr)
	}

	// A claim of a commit the other machine turns out not to have.
	f = moveFixture(t)
	if _, perr := f.dst.unpackWorktree(proto.WorktreeUnpackParams{ProjectID: f.dstProj.id,
		Pack: mvUpload(t, f.dst, mvPack(t, f, mvGit(t, f.wt, "rev-parse", "HEAD~1")))}); perr == nil ||
		!strings.Contains(perr.Message, "take the branch's commits") {
		t.Fatalf("missing prerequisite: %v", perr)
	}
	if out, _ := exec.Command("git", "-C", f.other, "branch", "--list", "feat").Output(); len(out) != 0 {
		t.Fatal("a failed move left the branch")
	}
}

func TestMoveWorktreeExistingBranch(t *testing.T) {
	f := moveFixture(t)
	main := mvGit(t, f.root, "rev-parse", "main")
	// Behind the moved one: fast-forwarded.
	mvGit(t, f.other, "branch", "feat", main)
	res, perr := f.dst.unpackWorktree(proto.WorktreeUnpackParams{ProjectID: f.dstProj.id, Pack: mvUpload(t, f.dst, mvPack(t, f, main))})
	if perr != nil || mvGit(t, res.Path, "rev-parse", "HEAD") != mvGit(t, f.wt, "rev-parse", "HEAD") {
		t.Fatalf("behind: %+v %v", res, perr)
	}

	// Diverged: refused, and the branch there is left as it was.
	f = moveFixture(t)
	main = mvGit(t, f.root, "rev-parse", "main")
	mvGit(t, f.other, "switch", "-q", "-c", "feat")
	mvWrite(t, f.other, "theirs.txt", "x")
	mvGit(t, f.other, "add", ".")
	mvGit(t, f.other, "commit", "-q", "-m", "theirs")
	theirs := mvGit(t, f.other, "rev-parse", "feat")
	mvGit(t, f.other, "switch", "-q", "main")
	if _, perr := f.dst.unpackWorktree(proto.WorktreeUnpackParams{ProjectID: f.dstProj.id, Pack: mvUpload(t, f.dst, mvPack(t, f, main))}); perr == nil ||
		!strings.Contains(perr.Message, "has commits there that the moved one doesn't") {
		t.Fatalf("diverged: %v", perr)
	}
	if got := mvGit(t, f.other, "rev-parse", "feat"); got != theirs {
		t.Fatal("the diverged branch was moved")
	}

	// Checked out in the main checkout there, even before a refresh noticed.
	f = moveFixture(t)
	main = mvGit(t, f.root, "rev-parse", "main")
	mvGit(t, f.other, "switch", "-q", "-c", "feat")
	if _, perr := f.dst.unpackWorktree(proto.WorktreeUnpackParams{ProjectID: f.dstProj.id, Pack: mvUpload(t, f.dst, mvPack(t, f, main))}); perr == nil ||
		!strings.Contains(perr.Message, "already checked out at "+f.other) {
		t.Fatalf("checked out: %v", perr)
	}
	if got := mvGit(t, f.other, "rev-parse", "feat"); got != main {
		t.Fatal("a checked-out branch's ref was moved")
	}
}

// mvTar builds a pack by hand.
func mvTar(t *testing.T, entries ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, h := range entries {
		body := h.Linkname
		if h.Typeflag == tar.TypeSymlink {
			body = ""
		} else {
			h.Linkname = ""
		}
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		h.Size = int64(len(body))
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

// mvEntry is a file entry whose content rides in Linkname for mvTar.
func mvEntry(name, content string) *tar.Header { return &tar.Header{Name: name, Linkname: content} }

func mvManifest(t *testing.T, head string) *tar.Header {
	b, _ := json.Marshal(packManifest{Version: packVersion, Branch: "feat", Head: head})
	return mvEntry("manifest.json", string(b))
}

func TestUnpackRefusesBadPacks(t *testing.T) {
	f := moveFixture(t)
	main := mvGit(t, f.root, "rev-parse", "main")
	outside := t.TempDir()
	unpack := func(data []byte) *proto.Error {
		_, perr := f.dst.unpackWorktree(proto.WorktreeUnpackParams{ProjectID: f.dstProj.id, Pack: mvUpload(t, f.dst, data)})
		return perr
	}
	junk := make([]byte, 100)
	rand.Read(junk)
	for name, data := range map[string][]byte{
		"not gzip":       junk,
		"empty":          mvTar(t),
		"no manifest":    mvTar(t, mvEntry("staged.patch", "x")),
		"bad manifest":   mvTar(t, mvEntry("manifest.json", "{")),
		"newer version":  mvTar(t, mvEntry("manifest.json", `{"version":99,"branch":"feat","head":"`+main+`"}`)),
		"no branch":      mvTar(t, mvEntry("manifest.json", `{"version":1,"head":"`+main+`"}`)),
		"escaping name":  mvTar(t, mvManifest(t, main), mvEntry("files/../../evil", "x")),
		"into .git":      mvTar(t, mvManifest(t, main), mvEntry("files/.git/config", "x")),
		"absolute":       mvTar(t, mvManifest(t, main), mvEntry("files//etc/x", "x")),
		"through a link": mvTar(t, mvManifest(t, main), &tar.Header{Name: "files/out", Typeflag: tar.TypeSymlink, Linkname: outside}, mvEntry("files/out/evil", "x")),
		"patch a link":   mvTar(t, mvManifest(t, main), &tar.Header{Name: "staged.patch", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}),
		"duplicate":      mvTar(t, mvManifest(t, main), mvEntry("files/a", "1"), mvEntry("files/a", "2")),
		"a folder entry": mvTar(t, mvManifest(t, main), &tar.Header{Name: "files/d/", Typeflag: tar.TypeDir}),
	} {
		if perr := unpack(data); perr == nil || perr.Code != proto.ErrBadRequest || !strings.Contains(perr.Message, "read the pack") {
			t.Errorf("%s: %v", name, perr)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside: %v", entries)
	}
	if entries, _ := os.ReadDir(f.dst.packs.dir); len(entries) != 0 {
		t.Fatalf("left temporary files: %v", entries)
	}

	// Entries a later build adds are skipped.
	res, perr := f.dst.unpackWorktree(proto.WorktreeUnpackParams{ProjectID: f.dstProj.id,
		Pack: mvUpload(t, f.dst, mvTar(t, mvManifest(t, main), mvEntry("future.bin", "x"), mvEntry("files/ok.txt", "ok")))})
	if perr != nil || strings.Join(res.Files, ",") != "ok.txt" {
		t.Fatalf("future entry: %+v %v", res, perr)
	}

	// A patch that doesn't apply: the worktree and the new branch are undone.
	f = moveFixture(t)
	main = mvGit(t, f.root, "rev-parse", "main")
	bad := "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-not there\n+x\n"
	perr = unpack(mvTar(t, mvManifest(t, main), mvEntry("staged.patch", bad)))
	if perr == nil || !strings.Contains(perr.Message, "apply the uncommitted changes") {
		t.Fatalf("bad patch: %v", perr)
	}
	if out := mvGit(t, f.other, "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Fatalf("worktree left: %s", out)
	}
	if out, _ := exec.Command("git", "-C", f.other, "branch", "--list", "feat").Output(); len(out) != 0 {
		t.Fatal("branch left")
	}
	if _, ok := f.dstProj.branchWorktree("feat"); ok {
		t.Fatal("the project still lists the undone worktree")
	}

	// A file whose place is taken by a folder in the checkout.
	perr = unpack(mvTar(t, mvManifest(t, main), mvEntry("files/README.md/x", "x")))
	if perr == nil || !strings.Contains(perr.Message, "write README.md/x") {
		t.Fatalf("in the way: %v", perr)
	}

	// Not an upload; unknown project; a plain folder.
	for _, c := range []struct {
		p    proto.WorktreeUnpackParams
		code string
	}{
		{proto.WorktreeUnpackParams{ProjectID: f.dstProj.id, Pack: filepath.Join(outside, "x")}, proto.ErrBadRequest},
		{proto.WorktreeUnpackParams{ProjectID: f.dstProj.id, Pack: f.dst.uploads.dir}, proto.ErrBadRequest},
		{proto.WorktreeUnpackParams{ProjectID: f.dstProj.id, Pack: f.dst.uploads.dir + "/../x"}, proto.ErrBadRequest},
		{proto.WorktreeUnpackParams{ProjectID: "nope", Pack: "x"}, proto.ErrNotFound},
	} {
		if _, perr := f.dst.unpackWorktree(c.p); perr == nil || perr.Code != c.code {
			t.Errorf("%+v: %v", c.p, perr)
		}
	}
	plain := mvProject(t, f.dst, t.TempDir())
	if _, perr := f.dst.unpackWorktree(proto.WorktreeUnpackParams{ProjectID: plain.id, Pack: "x"}); perr == nil || !strings.Contains(perr.Message, "not a git repository") {
		t.Errorf("plain folder: %v", perr)
	}
}

func TestDescribeAndPackErrors(t *testing.T) {
	f := moveFixture(t)
	for _, c := range []struct {
		ref  proto.WorktreeRef
		code string
	}{
		{proto.WorktreeRef{ProjectID: "nope", Path: f.wt}, proto.ErrNotFound},
		{proto.WorktreeRef{ProjectID: f.srcProj.id, Path: f.wt + "-x"}, proto.ErrNotFound},
	} {
		if _, perr := f.src.describeWorktree(c.ref); perr == nil || perr.Code != c.code {
			t.Errorf("describe %+v: %v", c.ref, perr)
		}
		if _, perr := f.src.packWorktree(proto.WorktreePackParams{ProjectID: c.ref.ProjectID, Path: c.ref.Path}); perr == nil || perr.Code != c.code {
			t.Errorf("pack %+v: %v", c.ref, perr)
		}
	}
	plain := mvProject(t, f.src, t.TempDir())
	if _, perr := f.src.describeWorktree(proto.WorktreeRef{ProjectID: plain.id, Path: plain.root}); perr == nil || !strings.Contains(perr.Message, "not a git") {
		t.Errorf("plain: %v", perr)
	}
	if _, perr := f.src.haveCommits(proto.WorktreeHaveParams{ProjectID: plain.id}); perr == nil || !strings.Contains(perr.Message, "not a git") {
		t.Errorf("have in a plain folder: %v", perr)
	}
	if _, perr := f.src.haveCommits(proto.WorktreeHaveParams{ProjectID: "nope"}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Errorf("have, unknown project: %v", perr)
	}

	// Detached HEAD: nothing to name the branch after.
	mvGit(t, f.wt, "switch", "-q", "--detach")
	f.src.projects.refresh(f.srcProj, "")
	if _, perr := f.src.describeWorktree(proto.WorktreeRef{ProjectID: f.srcProj.id, Path: f.wt}); perr == nil || !strings.Contains(perr.Message, "isn't on a branch") {
		t.Errorf("detached: %v", perr)
	}
	mvGit(t, f.wt, "switch", "-q", "feat")

	// A merge in progress.
	mvGit(t, f.root, "switch", "-q", "main")
	mvWrite(t, f.root, "one.txt", "main's\n")
	mvGit(t, f.root, "add", ".")
	mvGit(t, f.root, "commit", "-q", "-m", "main one")
	cmd := exec.Command("git", "-C", f.wt, "merge", "main")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	cmd.Run() // conflicts
	f.src.projects.refresh(f.srcProj, "")
	if _, perr := f.src.describeWorktree(proto.WorktreeRef{ProjectID: f.srcProj.id, Path: f.wt}); perr == nil || !strings.Contains(perr.Message, "conflicts") {
		t.Errorf("conflicts: %v", perr)
	}

	// Only the first 1000 candidates are looked up.
	many := make([]string, 1500)
	for i := range many {
		many[i] = strings.Repeat("0", 40)
	}
	many[1200] = mvGit(t, f.root, "rev-parse", "main")
	if have, perr := f.src.haveCommits(proto.WorktreeHaveParams{ProjectID: f.srcProj.id, Commits: many}); perr != nil || len(have.Have) != 0 {
		t.Errorf("past the limit: %+v %v", have, perr)
	}
}

func TestPackReading(t *testing.T) {
	f := moveFixture(t)
	// Past one chunk: an untracked file of 1.5 chunks that doesn't compress.
	big := make([]byte, proto.UploadChunkSize*3/2)
	rand.Read(big)
	os.WriteFile(filepath.Join(f.wt, "big.bin"), big, 0o644)
	pk, perr := f.src.packWorktree(proto.WorktreePackParams{ProjectID: f.srcProj.id, Path: f.wt})
	if perr != nil || pk.Size <= int64(proto.UploadChunkSize) {
		t.Fatalf("pack: %+v %v", pk, perr)
	}
	for _, off := range []int64{-1, pk.Size + 1} {
		if _, perr := f.src.packs.read(proto.WorktreePackReadParams{ID: pk.ID, Offset: off}); perr == nil || perr.Code != proto.ErrBadRequest {
			t.Errorf("offset %d: %v", off, perr)
		}
	}
	c, perr := f.src.packs.read(proto.WorktreePackReadParams{ID: pk.ID})
	if perr != nil || len(c.Data) != proto.UploadChunkSize || c.EOF {
		t.Fatalf("first chunk: %d %v %v", len(c.Data), c.EOF, perr)
	}
	// Reading again from anywhere works until the end has been read.
	c, perr = f.src.packs.read(proto.WorktreePackReadParams{ID: pk.ID, Offset: int64(proto.UploadChunkSize)})
	if perr != nil || !c.EOF || int64(len(c.Data)) != pk.Size-int64(proto.UploadChunkSize) {
		t.Fatalf("last chunk: %d %v %v", len(c.Data), c.EOF, perr)
	}
	if _, perr := f.src.packs.read(proto.WorktreePackReadParams{ID: pk.ID, Offset: 0}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("after the end: %v", perr)
	}

	// A pack nobody reads is swept; one being read is kept.
	now := time.Now()
	f.src.packs.now = func() time.Time { return now }
	idle, _ := f.src.packWorktree(proto.WorktreePackParams{ProjectID: f.srcProj.id, Path: f.wt})
	busy, _ := f.src.packWorktree(proto.WorktreePackParams{ProjectID: f.srcProj.id, Path: f.wt})
	now = now.Add(packIdle - time.Second)
	f.src.packs.read(proto.WorktreePackReadParams{ID: busy.ID})
	now = now.Add(2 * time.Second)
	f.src.packs.sweep()
	if _, perr := f.src.packs.read(proto.WorktreePackReadParams{ID: idle.ID}); perr == nil {
		t.Error("idle pack kept")
	}
	if _, perr := f.src.packs.read(proto.WorktreePackReadParams{ID: busy.ID}); perr != nil {
		t.Errorf("busy pack swept: %v", perr)
	}

	// Leftovers from an earlier run are cleared on start.
	os.WriteFile(filepath.Join(f.src.packs.dir, "old.tar.gz"), []byte("x"), 0o600)
	os.MkdirAll(filepath.Join(f.src.packs.dir, "unpack-1"), 0o700)
	quit := make(chan struct{})
	close(quit)
	f.src.packs.run(quit)
	if entries, _ := os.ReadDir(f.src.packs.dir); len(entries) != 0 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestPackTooBig(t *testing.T) {
	f := moveFixture(t)
	defer func(old int64) { packLimit = old }(packLimit)
	packLimit = 4 << 10
	noise := make([]byte, 8<<10) // doesn't compress
	rand.Read(noise)
	os.WriteFile(filepath.Join(f.wt, "noise.bin"), noise, 0o644)
	if _, perr := f.src.packWorktree(proto.WorktreePackParams{ProjectID: f.srcProj.id, Path: f.wt}); perr == nil ||
		!strings.Contains(perr.Message, "the limit is 4 KB") {
		t.Fatalf("too big: %v", perr)
	}
	if entries, _ := os.ReadDir(f.src.packs.dir); len(entries) != 0 {
		t.Fatalf("left: %v", entries)
	}
}

func TestCloneProject(t *testing.T) {
	f := moveFixture(t)
	home := os.Getenv("HOME")
	info, perr := f.dst.cloneProject(proto.ProjectCloneParams{URL: f.root, Path: "~/work/api"})
	if perr != nil {
		t.Fatal(perr)
	}
	if info.Path != filepath.Join(home, "work", "api") || !info.Git {
		t.Fatalf("clone: %+v", info)
	}
	p, _ := f.dst.projects.get(info.ID)
	f.dst.projects.refresh(p, "")
	if p.snapshot().Remote != f.root {
		t.Fatalf("remote: %q", p.snapshot().Remote)
	}
	// An empty folder is fine; one with something in it is not.
	os.MkdirAll(filepath.Join(home, "empty"), 0o755)
	if _, perr := f.dst.cloneProject(proto.ProjectCloneParams{URL: f.root, Path: "~/empty"}); perr != nil {
		t.Fatalf("into an empty folder: %v", perr)
	}
	for _, c := range []proto.ProjectCloneParams{
		{URL: f.root, Path: "~/work/api"},
		{URL: "  ", Path: "~/x"},
		{URL: filepath.Join(t.TempDir(), "nowhere"), Path: "~/y"},
	} {
		if _, perr := f.dst.cloneProject(c); perr == nil || perr.Code != proto.ErrBadRequest {
			t.Errorf("%+v: %v", c, perr)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "y")); !os.IsNotExist(err) {
		t.Error("a failed clone left its folder")
	}
}

func TestMoveMethodsDispatch(t *testing.T) {
	f := moveFixture(t)
	call := func(s *Server, method string, params any) (any, *proto.Error) {
		b, _ := json.Marshal(params)
		return s.dispatch(nil, proto.Message{ID: "1", Method: method, Params: b})
	}
	out, perr := call(f.src, proto.MethodWorktreeDescribe, proto.WorktreeRef{ProjectID: f.srcProj.id, Path: f.wt})
	info, ok := out.(proto.WorktreeMoveInfo)
	if perr != nil || !ok || info.Branch != "feat" {
		t.Fatalf("describe: %#v %v", out, perr)
	}
	out, perr = call(f.dst, proto.MethodWorktreeHave, proto.WorktreeHaveParams{ProjectID: f.dstProj.id, Commits: info.History})
	have, ok := out.(proto.WorktreeHaveResult)
	if perr != nil || !ok || len(have.Have) == 0 {
		t.Fatalf("have: %#v %v", out, perr)
	}
	out, perr = call(f.src, proto.MethodWorktreePack, proto.WorktreePackParams{ProjectID: f.srcProj.id, Path: f.wt, Have: have.Have[0]})
	pk, ok := out.(proto.WorktreePack)
	if perr != nil || !ok {
		t.Fatalf("pack: %#v %v", out, perr)
	}
	out, perr = call(f.src, proto.MethodWorktreePackRead, proto.WorktreePackReadParams{ID: pk.ID})
	chunk, ok := out.(proto.WorktreePackChunk)
	if perr != nil || !ok || !chunk.EOF {
		t.Fatalf("read: %#v %v", out, perr)
	}
	out, perr = call(f.dst, proto.MethodWorktreeUnpack, proto.WorktreeUnpackParams{ProjectID: f.dstProj.id, Pack: mvUpload(t, f.dst, chunk.Data)})
	if res, ok := out.(proto.WorktreeUnpackResult); perr != nil || !ok || res.Branch != "feat" {
		t.Fatalf("unpack: %#v %v", out, perr)
	}
	if _, perr := call(f.dst, proto.MethodProjectClone, proto.ProjectCloneParams{URL: ""}); perr == nil {
		t.Fatal("clone without a URL")
	}
	for _, m := range []string{proto.MethodWorktreeDescribe, proto.MethodWorktreeHave, proto.MethodWorktreePack,
		proto.MethodWorktreePackRead, proto.MethodWorktreeUnpack, proto.MethodProjectClone} {
		if !slowMethods[m] {
			t.Errorf("%s runs git: it must not hold up the connection", m)
		}
		if _, perr := call(f.src, m, "not an object"); perr == nil || perr.Code != proto.ErrBadRequest {
			t.Errorf("%s with bad params: %v", m, perr)
		}
	}
}
