package cloudagent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// Git (PROTOCOL §6): github.com is reached through the git proxy, which
// knows the machine by its token and mints a GitHub token per request, so
// the machine never holds a GitHub credential. Git is told so in the
// system's configuration — every user, every repository, a clone an agent
// starts included — and asks this agent for the token through a
// credential helper scoped to the proxy alone, which is the only host git
// will hand it to.

// githubForms are the ways a repository on github.com is named, each sent
// to the proxy.
var githubForms = []string{"https://github.com/", "git@github.com:", "ssh://git@github.com/"}

// configureGit points github.com at proxy, with the agent as the proxy's
// credential helper. It replaces what an earlier agent set, for this
// proxy or another.
func (a *Agent) configureGit(ctx context.Context, proxy string) error {
	set := a.opts.GitConfig
	if set == nil {
		set = systemGitConfig
	}
	base := strings.TrimRight(proxy, "/") + "/github.com/"
	// Rewrites an earlier agent left, for any proxy.
	if old, err := gitGetRegexp(ctx, `^url\..*\.insteadof$`); err == nil {
		for key, vals := range old {
			for _, v := range vals {
				if isGithubForm(v) {
					if err := set(ctx, "--unset-all", key); err != nil {
						return err
					}
					break
				}
			}
		}
	}
	key := "url." + base + ".insteadOf"
	for _, form := range githubForms {
		if err := set(ctx, "--add", key, form); err != nil {
			return err
		}
	}
	helper := "credential." + strings.TrimRight(proxy, "/") + ".helper"
	_ = set(ctx, "--unset-all", helper)
	// An empty helper first clears any the system or user configured for
	// this URL, so the proxy is asked only with the machine's token.
	if err := set(ctx, "--add", helper, ""); err != nil {
		return err
	}
	cmd := "!" + shellQuote(a.opts.Executable) + " cloud git-credential -socket " + shellQuote(a.opts.AgentSocket)
	if err := set(ctx, "--add", helper, cmd); err != nil {
		return err
	}
	a.logf("cloud: git reaches github.com through %s", proxy)
	return nil
}

func isGithubForm(v string) bool {
	for _, f := range githubForms {
		if v == f {
			return true
		}
	}
	return false
}

// systemGitConfig is git config --system: /etc/gitconfig, or where
// GIT_CONFIG_SYSTEM says.
func systemGitConfig(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"config", "--system"}, args...)...).CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		// 5: --unset-all of a key that is not there.
		if errors.As(err, &ee) && ee.ExitCode() == 5 && len(args) > 0 && args[0] == "--unset-all" {
			return nil
		}
		return fmt.Errorf("git config --system %s: %v: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}

// gitGetRegexp is the system configuration's keys matching re, with their
// values.
func gitGetRegexp(ctx context.Context, re string) (map[string][]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "config", "--system", "--get-regexp", re).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil // none
		}
		return nil, err
	}
	keys := map[string][]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		keys[k] = append(keys[k], v)
	}
	return keys, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// GitCredential is git's credential helper on a cloud machine (`conch
// cloud git-credential`; git's credential.helper protocol: get reads the
// request on stdin and answers username and password). The password is
// the machine token, asked of the agent over its socket, from memory.
func GitCredential(args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("cloud git-credential", flag.ContinueOnError)
	sock := fs.String("socket", DefaultAgentSocket, "the cloud agent's socket")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch cloud git-credential [-socket PATH] get|store|erase")
	}
	// What git sends, to its blank line, whatever the operation.
	sc := bufio.NewScanner(in)
	for sc.Scan() && strings.TrimSpace(sc.Text()) != "" {
	}
	if fs.Arg(0) != "get" {
		return nil // store and erase: nothing is kept to change
	}
	ans, err := Ask(*sock, proto.CloudAgentAsk{Op: proto.CloudAskGitCredential})
	if err != nil {
		return fmt.Errorf("the cloud agent: %w", err)
	}
	_, err = fmt.Fprintf(out, "username=%s\npassword=%s\n", ans.Username, ans.Password)
	return err
}
