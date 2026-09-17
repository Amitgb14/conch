package ghx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// argsGH records gh's arguments, one per line, and prints out.
func argsGH(t *testing.T, out string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "args")
	fakeGH(t, "for a in \"$@\"; do printf '%s\\n' \"$a\"; done > "+log+"\nprintf '%s' '"+out+"'\n")
	return log
}

func TestCreate(t *testing.T) {
	log := argsGH(t, "Warning: 2 uncommitted changes\nhttps://github.com/o/r/pull/12\n")
	url, err := Create(context.Background(), t.TempDir(), "main", "feat/x", "Add x", "Body with\nlines", true)
	if err != nil || url != "https://github.com/o/r/pull/12" {
		t.Fatalf("create: %q %v", url, err)
	}
	b, _ := os.ReadFile(log)
	want := "pr\ncreate\n--base\nmain\n--head\nfeat/x\n--title\nAdd x\n--body\nBody with\nlines\n--draft\n"
	if string(b) != want {
		t.Fatalf("args:\n%s", b)
	}

	// No title: gh fills it from the commits.
	log = argsGH(t, "https://github.com/o/r/pull/13")
	if url, err := Create(context.Background(), t.TempDir(), "main", "feat", "  ", "ignored", false); err != nil || !strings.HasSuffix(url, "/13") {
		t.Fatalf("fill: %q %v", url, err)
	}
	if b, _ := os.ReadFile(log); string(b) != "pr\ncreate\n--base\nmain\n--head\nfeat\n--fill\n" {
		t.Fatalf("fill args:\n%s", b)
	}
}

func TestCreateFailures(t *testing.T) {
	cases := []struct{ name, script, want string }{
		{"exists", "echo 'a pull request for branch \"feat\" into branch \"main\" already exists:' >&2\necho https://x/pull/1 >&2\nexit 1\n", "gh pr create: a pull request for branch"},
		{"not pushed", "echo 'aborted: you must first push the current branch to a remote' >&2; exit 1\n", "must first push"},
		{"no url", "echo done\n", "no pull request URL"},
		{"empty output", "exit 0\n", "no pull request URL"},
		{"silent failure", "exit 3\n", "exit status 3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeGH(t, c.script)
			_, err := Create(context.Background(), t.TempDir(), "main", "feat", "t", "", false)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}

	t.Setenv("CONCH_GH", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := Create(context.Background(), t.TempDir(), "main", "feat", "t", "", false); !errors.Is(err, ErrNoGH) {
		t.Fatalf("no gh: %v", err)
	}
}

func TestParseHead(t *testing.T) {
	prs, err := Parse([]byte(`[{"number":1,"headRefName":"feat","headRefOid":"0123abcd"},{"number":2}]`))
	if err != nil || prs[0].Head != "0123abcd" || prs[1].Head != "" {
		t.Fatalf("head: %+v %v", prs, err)
	}
}
