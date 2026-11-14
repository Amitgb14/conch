package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// checkFakes stands up a Linear and a Jira that answer a check and a list,
// and a gh signed in as amit with three open issues, one of them his.
func checkFakes(t *testing.T) (linearURL, jiraURL string) {
	t.Helper()
	lin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "lin_api_ok" {
			http.Error(w, `{"errors":[{"message":"Authentication required"}]}`, http.StatusUnauthorized)
			return
		}
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		if strings.Contains(req.Query, "viewer") {
			w.Write([]byte(`{"data":{"viewer":{"name":"Amit","email":"amit@acme.dev","organization":{"name":"Acme"}},"teams":{"nodes":[{"key":"ENG","name":"Engineering"}]}}}`))
			return
		}
		f, _ := req.Variables["filter"].(map[string]any)
		if team, _ := f["team"].(map[string]any); team != nil {
			if eq := team["key"].(map[string]any)["eq"]; eq == "NOPE" {
				w.Write([]byte(`{"errors":[{"message":"Team not found"}]}`))
				return
			}
		}
		issue := `{"id":"1","identifier":"ENG-1","title":"a"}`
		if f["assignee"] != nil {
			w.Write([]byte(`{"data":{"issues":{"nodes":[` + issue + `]}}}`))
			return
		}
		w.Write([]byte(`{"data":{"issues":{"nodes":[` + issue + `,` + issue + `]}}}`))
	}))
	t.Cleanup(lin.Close)
	jira := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "", http.StatusUnauthorized) // every token is wrong here
	}))
	t.Cleanup(jira.Close)

	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"api user") echo amit ;;
"issue list") case "$*" in *@me*) echo '[{"number":1}]' ;; *) echo '[{"number":1},{"number":2},{"number":3}]' ;; esac ;;
"pr list") echo '[]' ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("JIRA_API_TOKEN", "")
	return lin.URL, jira.URL
}

func writeConfig(t *testing.T, s *Server, toml string) {
	t.Helper()
	t.Setenv("CONCH_HOME", s.configDir)
	if err := os.WriteFile(filepath.Join(s.configDir, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTicketCheck(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	linURL, jiraURL := checkFakes(t)
	writeConfig(t, s, `[trackers.linear]
api_key = "lin_api_ok"
api_url = "`+linURL+`"
team = "ENG"
[trackers.jira]
url = "`+jiraURL+`"
email = "amit@acme.dev"
api_token = "wrong"
[[trackers.projects]]
project = "api"
tracker = "linear"
key = "ENG"
[[trackers.projects]]
project = "web"
tracker = "linear"
key = "NOPE"
[[trackers.projects]]
project = "cloud"
tracker = "jira"
key = "CLOUD"
`)
	res, perr := s.ticketCheck(proto.TicketCheckParams{ProjectID: p.id})
	if perr != nil {
		t.Fatal(perr)
	}
	if len(res.Trackers) != 3 {
		t.Fatalf("%+v", res)
	}
	gh, lin, jira := res.Trackers[0], res.Trackers[1], res.Trackers[2]
	if gh.Tracker != "github" || gh.State != proto.TrackerConnected || gh.Who != "amit" || gh.Open != 3 || gh.Mine != 1 || gh.KeyFrom != "gh" {
		t.Fatalf("github %+v", gh)
	}
	if lin.State != proto.TrackerConnected || lin.Who != "Amit" || lin.Workspace != "Acme" || lin.Area != "ENG" ||
		lin.Open != 2 || lin.Mine != 1 || len(lin.Areas) != 1 || lin.KeyFrom != "config.toml" || lin.Error != "" {
		t.Fatalf("linear %+v", lin)
	}
	if jira.State != proto.TrackerError || !strings.Contains(jira.Error, "refused the key") || jira.Who != "" {
		t.Fatalf("jira %+v", jira)
	}
	// Never the key itself, anywhere in the answer.
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "lin_api_ok") || strings.Contains(string(raw), `"wrong"`) {
		t.Fatalf("a key in the answer: %s", raw)
	}
	if len(res.Projects) != 3 || res.Projects[0].Open != 2 || res.Projects[0].Error != "" ||
		res.Projects[1].Error != "no team NOPE in Linear, or the key may not see it" || !strings.Contains(res.Projects[2].Error, "refused the key") {
		t.Fatalf("projects %+v", res.Projects)
	}
}

func TestTicketCheckNothingSetUp(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	checkFakes(t)
	writeConfig(t, s, "")
	res, perr := s.ticketCheck(proto.TicketCheckParams{})
	if perr != nil {
		t.Fatal(perr)
	}
	if res.Trackers[1].State != proto.TrackerOff || res.Trackers[2].State != proto.TrackerOff || len(res.Projects) != 0 {
		t.Fatalf("%+v", res)
	}
	// No project: GitHub is the login alone, no counts.
	if gh := res.Trackers[0]; gh.State != proto.TrackerConnected || gh.Open != 0 || gh.Area != "" {
		t.Fatalf("github %+v", gh)
	}

	// Jira half set up: a site, no email — said, not off.
	writeConfig(t, s, "[trackers.jira]\nurl = \"https://acme.atlassian.net\"\n")
	res, _ = s.ticketCheck(proto.TicketCheckParams{})
	if j := res.Trackers[2]; j.State != proto.TrackerError || !strings.Contains(j.Error, "email") {
		t.Fatalf("half-set jira %+v", j)
	}

	// No gh at all, and gh not signed in.
	t.Setenv("CONCH_GH", filepath.Join(t.TempDir(), "absent"))
	res, _ = s.ticketCheck(proto.TicketCheckParams{})
	if gh := res.Trackers[0]; gh.State != proto.TrackerOff || !strings.Contains(gh.Error, "not installed") {
		t.Fatalf("no gh %+v", gh)
	}
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte("#!/bin/sh\necho 'To get started with GitHub CLI, please run:  gh auth login' >&2\nexit 4\n"), 0o755)
	t.Setenv("CONCH_GH", gh)
	res, _ = s.ticketCheck(proto.TicketCheckParams{})
	if g := res.Trackers[0]; g.State != proto.TrackerError || g.Error != "gh is not signed in: run gh auth login" {
		t.Fatalf("signed out %+v", g)
	}

	// A project that is not there is refused.
	if _, perr := s.ticketCheck(proto.TicketCheckParams{ProjectID: "nope"}); perr == nil {
		t.Fatal("checked a project that is not there")
	}
	// A config that does not read says so.
	writeConfig(t, s, "[trackers\nbroken")
	if _, perr := s.ticketCheck(proto.TicketCheckParams{}); perr == nil || !strings.Contains(perr.Message, "config.toml") {
		t.Fatalf("broken config: %v", perr)
	}
}

// A tracker that does not answer costs the others nothing: the check comes
// back by its deadline with that one in error.
func TestTicketCheckDeadline(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	checkFakes(t)
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	// Cleanups run last first: the handler is let go, then the server closes.
	t.Cleanup(hang.Close)
	t.Cleanup(func() { close(release) })
	writeConfig(t, s, "[trackers.linear]\napi_key = \"k\"\napi_url = \""+hang.URL+"\"\n")
	was := checkTimeout
	checkTimeout = 300 * time.Millisecond
	t.Cleanup(func() { checkTimeout = was })
	start := time.Now()
	res, perr := s.ticketCheck(proto.TicketCheckParams{})
	if perr != nil {
		t.Fatal(perr)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
	if res.Trackers[1].State != proto.TrackerError || res.Trackers[0].State != proto.TrackerConnected {
		t.Fatalf("%+v", res.Trackers)
	}
}
