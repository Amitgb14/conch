package gitx

import "testing"

func TestRepoName(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/Amitgb14/conch.git":   "conch",
		"https://github.com/Amitgb14/conch":       "conch",
		"https://github.com/Amitgb14/conch/":      "conch",
		"https://github.com/Amitgb14/conch.git/":  "conch",
		"  https://gitlab.com/a/b/deep-repo.git ": "deep-repo",
		"git@github.com:Amitgb14/conch.git":       "conch",
		"github.com:owner/my.tool.git":            "my.tool",
		"ssh://git@host:2222/org/repo.git":        "repo",
		"file:///srv/git/api.git":                 "api",
		"/srv/git/api.git":                        "api",
		"../repo":                                 "repo",
		"repo.git":                                "repo",
		"":                                        "",
		"https://github.com/":                     "",
		"https://github.com":                      "",
		"git@github.com:":                         "",
		".git":                                    "",
		"http://[::1":                             "", // not a URL at all
	} {
		if got := RepoName(in); got != want {
			t.Errorf("RepoName(%q) = %q, want %q", in, got, want)
		}
	}
}
