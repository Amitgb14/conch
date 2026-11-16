package tracker

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Trackers conch has code for.
const (
	Linear = "linear"
	Jira   = "jira"
)

// Names are the trackers, in the order a choice between them is offered.
var Names = []string{Linear, Jira}

var keyRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[0-9]+$`)

// ValidKey says whether key is shaped like a ticket's key, ENG-123.
func ValidKey(key string) bool { return keyRE.MatchString(key) }

// ParseRef reads a ticket as somebody would give it: its key (ENG-123),
// its key after the tracker's name (linear:ENG-123), or its address in the
// tracker. tracker is "" when nothing in ref says which — Linear's keys
// and Jira's look alike — and key is upper case.
func ParseRef(ref string) (tracker, key string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", fmt.Errorf("no ticket given")
	}
	if u, perr := url.Parse(ref); perr == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		return fromURL(ref, u)
	}
	if name, rest, ok := strings.Cut(ref, ":"); ok {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != Linear && name != Jira {
			return "", "", fmt.Errorf("%q: conch knows the trackers %s", ref, strings.Join(Names, " and "))
		}
		tracker, ref = name, rest
	}
	key = strings.ToUpper(strings.TrimSpace(ref))
	if !ValidKey(key) {
		return "", "", fmt.Errorf("%q is not a ticket's key, which looks like ENG-123", strings.TrimSpace(ref))
	}
	return tracker, key, nil
}

func fromURL(ref string, u *url.URL) (string, string, error) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case u.Host == "linear.app" || strings.HasSuffix(u.Host, ".linear.app"):
		for i, p := range parts {
			if p == "issue" && i+1 < len(parts) {
				if k := strings.ToUpper(parts[i+1]); ValidKey(k) {
					return Linear, k, nil
				}
			}
		}
	case strings.HasSuffix(u.Host, ".atlassian.net"):
		if k := strings.ToUpper(u.Query().Get("selectedIssue")); ValidKey(k) {
			return Jira, k, nil
		}
		for i, p := range parts {
			if p == "browse" && i+1 < len(parts) {
				if k := strings.ToUpper(parts[i+1]); ValidKey(k) {
					return Jira, k, nil
				}
			}
		}
	}
	return "", "", fmt.Errorf("%s is not a Linear or Jira ticket's address", ref)
}

var (
	fileNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	bareKeyRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
)

// CheckRef is ParseRef for a command that hands the ticket to a server
// which may know more trackers than conch has code for: a tickets
// integration's NAME:KEY (shortcut:123), or a key alone (123) for a
// project tied to one, is taken as given and left to the server to make
// sense of. What is no ticket of anybody's is still refused here.
func CheckRef(ref string) (tracker, key string, err error) {
	tracker, key, err = ParseRef(ref)
	if err == nil {
		return tracker, key, nil
	}
	ref = strings.TrimSpace(ref)
	if name, k, ok := strings.Cut(ref, ":"); ok {
		if k = strings.TrimSpace(k); fileNameRE.MatchString(name) && name != Linear && name != Jira && k != "" && !strings.HasPrefix(k, "//") && !strings.ContainsAny(k, " \t") {
			return name, k, nil
		}
		return "", "", err
	}
	if bareKeyRE.MatchString(ref) {
		return "", ref, nil
	}
	return "", "", err
}
