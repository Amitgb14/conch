package gitx

import (
	"net/url"
	"path"
	"strings"
)

// RepoName is the folder a clone of rawURL is given by default — what
// `git clone` itself names it: the last part of the repository's path,
// without .git. It reads https and ssh URLs, scp-like ones
// (git@github.com:owner/repo.git) and local paths, and is "" when the URL
// names no repository (a host alone, or nothing).
func RepoName(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	p := s
	if i := strings.Index(s, "://"); i >= 0 {
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		p = u.Path
	} else if c := strings.Index(s, ":"); c > 0 && !strings.ContainsAny(s[:c], "/\\") {
		// scp-like: [user@]host:path — a colon before any slash.
		p = s[c+1:]
	}
	p = strings.TrimRight(p, "/")
	p = strings.TrimSuffix(p, ".git")
	p = strings.TrimRight(p, "/")
	name := path.Base(p)
	switch name {
	case "", ".", "..", "/":
		return ""
	}
	return name
}
