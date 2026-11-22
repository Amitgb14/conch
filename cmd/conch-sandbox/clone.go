package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"
)

// A tool that makes a sandbox for a branch wants that branch checked out
// in it, at the commit it was on: the clone is made over ssh, with git in
// the sandbox, from the repository's HTTPS address — nothing of this
// computer's is copied in, so the commit has to be pushed.

// repo is what to check out.
type repo struct {
	URL, Branch, Ref string
}

var (
	branchChars = regexp.MustCompile(`^[A-Za-z0-9._/+-]+$`)
	refChars    = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`) // shorter is too often ambiguous, found out only in the sandbox
)

// check refuses what cannot be checked out, or could not be said safely
// in a shell line.
func (r repo) check() error {
	if r.URL == "" {
		if r.Branch != "" || r.Ref != "" {
			return errUsage{"--branch and --ref need --repo"}
		}
		return nil
	}
	u, err := url.Parse(r.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || strings.ContainsAny(r.URL, " \t\n\r'\"\\`$") {
		return errUsage{fmt.Sprintf("--repo %q: give the repository's https:// address", r.URL)}
	}
	if u.User != nil {
		return errUsage{"--repo: no credentials in the address; a private repository reads GH_TOKEN in the sandbox"}
	}
	if b := r.Branch; b != "" && (!branchChars.MatchString(b) || strings.HasPrefix(b, "-") || strings.HasPrefix(b, "/") ||
		strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".lock") || strings.Contains(b, "..") || strings.Contains(b, "//")) {
		return errUsage{fmt.Sprintf("--branch %q is not a branch name", b)}
	}
	if r.Ref != "" && !refChars.MatchString(r.Ref) {
		return errUsage{fmt.Sprintf("--ref %q: give a commit's hash, 7 characters or more", r.Ref)}
	}
	return nil
}

// dirName is the checkout's folder in the sandbox's home: the name it
// was given, else the repository's.
func (r repo) dirName(name string) string {
	if d := slug(name); d != "" {
		return d
	}
	u, _ := url.Parse(r.URL)
	if u != nil {
		if d := slug(strings.TrimSuffix(path.Base(u.Path), ".git")); d != "" && d != "." {
			return d
		}
	}
	return "project"
}

var slugChars = regexp.MustCompile(`[^a-z0-9._-]+`)

func slug(s string) string {
	s = strings.Trim(slugChars.ReplaceAllString(strings.ToLower(s), "-"), "-.")
	if len(s) > 64 {
		s = strings.Trim(s[:64], "-.")
	}
	return s
}

// shq is s single-quoted for sh.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

const rootMark = "conch-sandbox-root="

// cloneScript checks r out into ~/dir and prints where. A token in GH_TOKEN
// inside the sandbox — there only when config.toml's env passes it in —
// reads a private repository, for this clone alone: the helpers the
// sandbox's own git config names are cleared first, or git would hand the
// token to one that keeps it (credential.helper store).
//
// With a commit, the whole repository is cloned and the branch made there
// at that commit: the branch is often not pushed yet (an Orca workspace's
// new branch, the branch pick finds checked out here) while the commit it
// starts from is. A branch alone has to be on the remote, and is said to
// be missing in so many words rather than in git's.
func (r repo) cloneScript(dir string) string {
	var b strings.Builder
	url := shq(r.URL)
	b.WriteString("set -e\n")
	b.WriteString(`command -v git >/dev/null 2>&1 || { echo "git is not installed in the sandbox" >&2; exit 3; }` + "\n")
	fmt.Fprintf(&b, "dir=\"$HOME\"/%s\n", shq(dir))
	b.WriteString(`if [ -e "$dir" ]; then echo "$dir is there already" >&2; exit 4; fi` + "\n")
	b.WriteString(`if [ -n "${GH_TOKEN:-}" ]; then set -- -c credential.helper= -c 'credential.helper=!f() { echo username=x-access-token; echo "password=$GH_TOKEN"; }; f'; else set --; fi` + "\n")
	switch {
	case r.Ref != "":
		b.WriteString(`git "$@" clone --quiet -- ` + url + ` "$dir"` + "\n")
		b.WriteString(`cd "$dir"` + "\n")
		fmt.Fprintf(&b, "git cat-file -e %s 2>/dev/null || { echo %s >&2; exit 6; }\n",
			shq(r.Ref+"^{commit}"), shq("commit "+r.Ref+" is not in "+r.URL+": push it first"))
		if r.Branch != "" {
			// The branch, at the commit asked for: the tool's own idea of
			// where it is, which a push since may have moved past.
			fmt.Fprintf(&b, "git checkout --quiet -B %s %s\n", shq(r.Branch), shq(r.Ref))
		} else {
			fmt.Fprintf(&b, "git checkout --quiet --detach %s\n", shq(r.Ref))
		}
	case r.Branch != "":
		// ls-remote --exit-code says 2 for a branch that is not there; any
		// other failure is left to the clone to say.
		fmt.Fprintf(&b, "rc=0; git \"$@\" ls-remote --exit-code --heads -- %s %s >/dev/null 2>&1 || rc=$?\n", url, shq("refs/heads/"+r.Branch))
		fmt.Fprintf(&b, "if [ \"$rc\" = 2 ]; then echo %s >&2; exit 5; fi\n",
			shq("branch "+r.Branch+" is not on "+r.URL+": push it first, or give --ref with the commit it starts from"))
		b.WriteString(`git "$@" clone --quiet --branch ` + shq(r.Branch) + ` -- ` + url + ` "$dir"` + "\n")
		b.WriteString(`cd "$dir"` + "\n")
	default:
		b.WriteString(`git "$@" clone --quiet -- ` + url + ` "$dir"` + "\n")
		b.WriteString(`cd "$dir"` + "\n")
	}
	b.WriteString(`printf '` + rootMark + `%s\n' "$(pwd -P)"` + "\n")
	return b.String()
}

// homeScript prints the sandbox's home, the root of a sandbox with no
// checkout.
const homeScript = `printf '` + rootMark + `%s\n' "$HOME"` + "\n"

// sshBinary is ssh; $CONCH_SSH stands in for it, as for conch.
func sshBinary() string {
	if b := os.Getenv("CONCH_SSH"); b != "" {
		return b
	}
	return "ssh"
}

// remoteRoot runs script in the sandbox behind al's alias and returns the
// path it printed. ssh reads the alias file alone (-F), so neither the
// person's config nor its Include is needed. A sandbox just started may
// not answer ssh yet, so a connection that fails (ssh's 255) is tried
// again until ctx ends; the script failing is not.
func (a *app) remoteRoot(ctx context.Context, al alias, script string) (string, error) {
	args := []string{"-F", aliasFile(), "-o", "BatchMode=yes", "-o", "ConnectTimeout=15", al.name(), "sh -s"}
	last := "" // what ssh last said, which a deadline cutting the next try short would lose
	grace := a.grace
	if grace == 0 {
		grace = time.Minute
	}
	var refusedSince time.Time
	for {
		cmd := exec.CommandContext(ctx, sshBinary(), args...)
		cmd.Stdin = strings.NewReader(script)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		var ee *exec.ExitError
		if err == nil {
			for _, line := range strings.Split(out.String(), "\n") {
				if root, ok := strings.CutPrefix(line, rootMark); ok && strings.HasPrefix(root, "/") {
					return root, nil
				}
			}
			return "", errors.New("the sandbox did not say where the checkout is")
		}
		msg := strings.TrimSpace(errb.String())
		if al.Token && al.User != "" { // never in a message
			msg = strings.ReplaceAll(msg, al.User, struck)
		}
		if errors.As(err, &ee) && ee.ExitCode() == 255 && ctx.Err() == nil {
			if msg != "" {
				last = msg
			}
			// Refused rather than not answering yet: tried for a while,
			// not for the whole deadline.
			if refusedForGood(msg) {
				if refusedSince.IsZero() {
					refusedSince = time.Now()
				} else if time.Since(refusedSince) >= grace {
					return "", fmt.Errorf("ssh to the sandbox was refused: %s", msg)
				}
			} else {
				refusedSince = time.Time{}
			}
			select {
			case <-ctx.Done():
			case <-time.After(a.retry):
				continue
			}
		}
		if ctx.Err() != nil {
			msg = firstOf(last, msg, err.Error())
			return "", fmt.Errorf("the sandbox never answered ssh: %s", msg)
		}
		return "", errors.New(firstOf(msg, err.Error()))
	}
}

// refusedForGood says whether ssh's message is one more tries will not
// change, unlike a connection refused or timed out while the sandbox
// starts.
func refusedForGood(msg string) bool {
	for _, m := range []string{"Permission denied", "Host key verification failed", "Could not resolve hostname"} {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}
