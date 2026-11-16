package server

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
)

// installFile puts an integration file where the server reads installed
// ones, and forgets what was read before.
func installFile(t *testing.T, s *Server, name, src string) {
	t.Helper()
	dir := filepath.Join(s.configDir, "integrations")
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	integration.Forget()
	t.Cleanup(integration.Forget)
}

// shippedAt is a shipped file's text with its api moved to a test server.
func shippedAt(t *testing.T, name, api string) string {
	t.Helper()
	data, _, err := integration.Source(name)
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	i := strings.Index(src, "\napi = \"")
	j := strings.Index(src[i+1:], "\n")
	return src[:i+1] + `api = "` + api + `"` + src[i+1+j:]
}

// fakeShortcutAPI answers as Shortcut's REST API v3 documents, for the
// server's tests (internal/integration has the full fake).
func fakeShortcutAPI(t *testing.T) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Shortcut-Token") != "sc-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+" "+string(b))
		mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v3/workflows":
			io.WriteString(w, `[{"id": 1, "states": [{"id": 10, "name": "Unstarted"}, {"id": 11, "name": "In Review"}]}]`)
		case "GET /api/v3/search/stories":
			io.WriteString(w, `{"data": [{"id": 123, "name": "Retry uploads", "app_url": "https://app.shortcut.com/acme/story/123", "workflow_state_id": 10}], "next": null, "total": 1}`)
		case "GET /api/v3/stories/123":
			io.WriteString(w, `{"id": 123, "name": "Retry uploads", "description": "Flaky wifi.", "app_url": "https://app.shortcut.com/acme/story/123", "workflow_state_id": 11,
			  "comments": [{"text": "Seen on 3G.", "deleted": false}]}`)
		case "POST /api/v3/stories/123/comments", "PUT /api/v3/stories/123":
			io.WriteString(w, `{"id": 123}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestTicketOverviewWithIntegrations(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	t.Setenv("LINEAR_API_KEY", "")
	gh := filepath.Join(t.TempDir(), "gh")
	os.WriteFile(gh, []byte("#!/bin/sh\ncase \"$1 $2\" in\n\"api user\") echo amit ;;\n\"issue list\") echo '[]' ;;\n\"pr list\") echo '[]' ;;\n*) exit 7 ;;\nesac\n"), 0o755)
	t.Setenv("CONCH_GH", gh)
	sc, _ := fakeShortcutAPI(t)
	writeConfig(t, s, "")
	installFile(t, s, "shortcut", shippedAt(t, "shortcut", sc.URL+"/api/v3"))

	// No key: Shortcut is not set up, and nothing says otherwise.
	t.Setenv("SHORTCUT_API_TOKEN", "")
	ov, perr := s.ticketOverview(proto.TicketOverviewParams{})
	if perr != nil || len(ov.Projects) != 1 {
		t.Fatalf("no key: %+v %v", ov, perr)
	}

	// Its key set and no project tied to it: a group of its own.
	t.Setenv("SHORTCUT_API_TOKEN", "sc-key")
	ov, _ = s.ticketOverview(proto.TicketOverviewParams{})
	if len(ov.Projects) != 2 {
		t.Fatalf("own group: %+v", ov)
	}
	g := ov.Projects[1]
	if g.Name != "Shortcut" || g.ProjectID != "" || len(g.Errors) != 0 || len(g.Tickets) != 1 {
		t.Fatalf("group %+v", g)
	}
	if r := g.Tickets[0]; r.Source != "shortcut" || r.Key != "shortcut:123" || r.Title != "Retry uploads" || r.Status != "Unstarted" {
		t.Fatalf("row %+v", r)
	}

	// Tied to the project: its tickets are the project's, with what conch
	// is doing about them, and no group of its own.
	writeConfig(t, s, "[[trackers.projects]]\nproject = \"api\"\ntracker = \"shortcut\"\n")
	if _, perr := s.scheduleAdd(proto.ScheduledItem{At: time.Now().Add(time.Hour), Queue: true,
		Task: &proto.TaskCreateParams{ProjectID: p.id, Ticket: "shortcut:123"}}); perr != nil {
		t.Fatal(perr)
	}
	ov, _ = s.ticketOverview(proto.TicketOverviewParams{})
	if len(ov.Projects) != 1 {
		t.Fatalf("tied: %+v", ov)
	}
	g = ov.Projects[0]
	if strings.Join(g.Sources, ",") != "github,shortcut" || len(g.Tickets) != 1 || g.Tickets[0].Doing.State != proto.DoingQueued {
		t.Fatalf("tied group %+v", g)
	}

	// A key Shortcut refuses is said in the group, without the key.
	t.Setenv("SHORTCUT_API_TOKEN", "wrong-key")
	ov, _ = s.ticketOverview(proto.TicketOverviewParams{})
	if errs := strings.Join(ov.Projects[0].Errors, ";"); !strings.Contains(errs, "SHORTCUT_API_TOKEN") || strings.Contains(errs, "wrong-key") {
		t.Fatalf("refused: %q", errs)
	}
}

// A tickets integration's tickets are read, commented on, moved and made
// into a task's prompt as Linear's are, named NAME:KEY — or by the key
// alone in a project tied to it.
func TestTicketMethodsThroughAnIntegration(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	sc, seen := fakeShortcutAPI(t)
	writeConfig(t, s, "")
	installFile(t, s, "shortcut", shippedAt(t, "shortcut", sc.URL+"/api/v3"))
	t.Setenv("SHORTCUT_API_TOKEN", "sc-key")

	got, perr := s.ticketGet(proto.TicketGetParams{ProjectID: p.id, Ticket: "shortcut:123"})
	if perr != nil || got.Tracker != "shortcut" || got.Key != "shortcut:123" || got.Status != "In Review" || len(got.Comments) != 1 {
		t.Fatalf("get %+v %v", got, perr)
	}
	if _, perr := s.ticketGet(proto.TicketGetParams{Ticket: "shortcut:999"}); perr == nil || !strings.Contains(perr.Message, "no such ticket") {
		t.Fatalf("missing: %v", perr)
	}
	// A bare key, with no project tied to Shortcut, is Linear's or Jira's
	// to make sense of — and they say what a key looks like.
	if _, perr := s.ticketGet(proto.TicketGetParams{ProjectID: p.id, Ticket: "123"}); perr == nil {
		t.Fatal("a bare key with nothing tied")
	}
	writeConfig(t, s, "[[trackers.projects]]\nproject = \"api\"\ntracker = \"shortcut\"\n")
	if got, perr := s.ticketGet(proto.TicketGetParams{ProjectID: p.id, Ticket: "123"}); perr != nil || got.Key != "shortcut:123" {
		t.Fatalf("tied, bare key: %+v %v", got, perr)
	}

	*seen = nil
	up, perr := s.ticketUpdate(proto.TicketUpdateParams{ProjectID: p.id, Ticket: "shortcut:123", Comment: "On it", Status: "in review",
		LinkURL: "https://github.com/acme/api/pull/7", LinkTitle: "PR #7"})
	if perr != nil || up.Key != "shortcut:123" {
		t.Fatalf("update %+v %v", up, perr)
	}
	all := strings.Join(*seen, "\n")
	for _, want := range []string{`{"text":"On it"}`, `{"workflow_state_id":11}`, `{"text":"[PR #7](https://github.com/acme/api/pull/7)"}`} {
		if !strings.Contains(all, want) {
			t.Errorf("sent no %s:\n%s", want, all)
		}
	}

	list, perr := s.tickets(proto.TicketsParams{ProjectID: p.id})
	if perr != nil || list.Tracker != "shortcut" || len(list.Tickets) != 1 || list.Tickets[0].Key != "shortcut:123" {
		t.Fatalf("list %+v %v", list, perr)
	}
	if list, perr := s.tickets(proto.TicketsParams{Tracker: "shortcut"}); perr != nil || len(list.Tickets) != 1 {
		t.Fatalf("list by name %+v %v", list, perr)
	}

	tp := proto.TaskCreateParams{ProjectID: p.id, Ticket: "shortcut:123"}
	if perr := s.ticketTask(p, &tp); perr != nil {
		t.Fatal(perr)
	}
	if !strings.HasPrefix(tp.Prompt, "Work on Shortcut ticket 123: Retry uploads\n") || !strings.Contains(tp.Prompt, "Seen on 3G.") ||
		!strings.Contains(tp.Branch, "123-retry-uploads") {
		t.Fatalf("task %q %q", tp.Branch, tp.Prompt)
	}

	// No key: said, with the variable to set.
	t.Setenv("SHORTCUT_API_TOKEN", "")
	if _, perr := s.ticketGet(proto.TicketGetParams{Ticket: "shortcut:123"}); perr == nil || !strings.Contains(perr.Message, "SHORTCUT_API_TOKEN") {
		t.Fatalf("no key: %v", perr)
	}
}

// GitLab's issues come with a GitLab remote once its key is set: the
// repository says which tracker it is, so nothing has to be tied.
func TestTicketOverviewDetectsGitLabIssues(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	gh := filepath.Join(t.TempDir(), "gh")
	os.WriteFile(gh, []byte("#!/bin/sh\necho 'none of the git remotes configured for this repository point to a known GitHub host' >&2\nexit 1\n"), 0o755)
	t.Setenv("CONCH_GH", gh)
	gl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "gl-key" || r.URL.EscapedPath() != "/api/v4/projects/grp%2Fapi/issues" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `[{"iid": 12, "title": "Crash on start", "state": "opened", "web_url": "https://gitlab.com/grp/api/-/issues/12"}]`)
	}))
	t.Cleanup(gl.Close)
	cmd := exec.Command("git", "-C", p.root, "remote", "add", "origin", "https://gitlab.com/grp/api.git")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	writeConfig(t, s, "")
	installFile(t, s, "gitlab-issues", shippedAt(t, "gitlab-issues", gl.URL+"/api/v4"))

	t.Setenv("GITLAB_TOKEN", "")
	ov, _ := s.ticketOverview(proto.TicketOverviewParams{})
	if len(ov.Projects) != 1 || len(ov.Projects[0].Tickets) != 0 || len(ov.Projects[0].Errors) != 0 {
		t.Fatalf("no key: %+v", ov)
	}
	t.Setenv("GITLAB_TOKEN", "gl-key")
	ov, _ = s.ticketOverview(proto.TicketOverviewParams{})
	g := ov.Projects[0]
	if len(ov.Projects) != 1 || len(g.Tickets) != 1 || g.Tickets[0].Key != "gitlab-issues:12" || g.Tickets[0].Source != "gitlab-issues" || len(g.Errors) != 0 {
		t.Fatalf("detected: %+v", ov)
	}
}

// hookRig is a Slack webhook conch's server sends to.
type hookRig struct {
	mu   sync.Mutex
	got  []string
	logs *bytes.Buffer
}

func (h *hookRig) bodies() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.got...)
}

func newHookRig(t *testing.T, s *Server, status int) *hookRig {
	h := &hookRig{logs: &bytes.Buffer{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.got = append(h.got, string(b))
		h.mu.Unlock()
		w.WriteHeader(status)
		io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	installFile(t, s, "slack", shippedAt(t, "slack", srv.URL))
	t.Setenv("SLACK_WEBHOOK_URL", srv.URL+"/services/T0/B0/HOOKSECRET")
	var mu sync.Mutex
	log.SetOutput(lockedWriter{&mu, h.logs})
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return h
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func notifyPane(state string) proto.PaneInfo {
	return proto.PaneInfo{ID: "p1", Name: "fix-uploads", Agent: &proto.AgentStatus{Name: "claude", State: state}}
}

func TestNotifyIntegrations(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	writeConfig(t, s, "")
	h := newHookRig(t, s, http.StatusOK)

	waiting := notifyPane(proto.AgentBlocked)
	waiting.Agent.Message = "Allow Bash(go test)?"
	waiting.ProjectID, waiting.Branch = p.id, "fix-uploads"
	s.notifyAgent(waiting, proto.AgentWorking)
	s.notifyAgent(notifyPane(proto.AgentDone), proto.AgentWorking)
	s.notifyMerged("api", "fix-uploads", 7, "https://github.com/acme/api/pull/7")
	s.notifyWG.Wait()
	got := h.bodies()
	if len(got) != 3 {
		t.Fatalf("sent %d: %q", len(got), got)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{`fix-uploads is waiting: Allow Bash(go test)? (api, fix-uploads)`, `conch · claude`,
		`fix-uploads finished`, `#7 fix-uploads merged in api`, `https://github.com/acme/api/pull/7`} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in %s", want, all)
		}
	}

	// No news: the same state, a pane adopted after a reload, working, a
	// reviewer.
	reviewer := notifyPane(proto.AgentDone)
	reviewer.Name = proto.ReviewerPrefix + "x"
	s.notifyAgent(notifyPane(proto.AgentBlocked), proto.AgentBlocked)
	s.notifyAgent(notifyPane(proto.AgentDone), "")
	s.notifyAgent(notifyPane(proto.AgentWorking), proto.AgentIdle)
	s.notifyAgent(reviewer, proto.AgentWorking)
	s.notifyAgent(proto.PaneInfo{ID: "p2"}, proto.AgentWorking)
	s.notifyWG.Wait()
	if n := len(h.bodies()); n != 3 {
		t.Fatalf("sent for no news: %d", n)
	}

	// [notify] says: waiting off, done on; all off.
	writeConfig(t, s, "[notify]\nenabled = true\nwaiting = false\ndone = true\n")
	s.notifyAgent(notifyPane(proto.AgentBlocked), proto.AgentWorking)
	s.notifyAgent(notifyPane(proto.AgentDone), proto.AgentWorking)
	writeConfig(t, s, "[notify]\nenabled = false\nwaiting = true\ndone = true\n")
	s.notifyAgent(notifyPane(proto.AgentDone), proto.AgentWorking)
	s.notifyMerged("api", "b", 8, "")
	s.notifyWG.Wait()
	if got := h.bodies(); len(got) != 4 || !strings.Contains(got[3], "finished") {
		t.Fatalf("as [notify] says: %q", got)
	}

	// No webhook set: nothing sent, nothing logged.
	writeConfig(t, s, "")
	t.Setenv("SLACK_WEBHOOK_URL", "")
	s.notifyAgent(notifyPane(proto.AgentDone), proto.AgentWorking)
	s.notifyWG.Wait()
	if n := len(h.bodies()); n != 4 || strings.Contains(h.logs.String(), "notify") {
		t.Fatalf("no webhook: %d sent, log %q", n, h.logs.String())
	}
}

// A webhook that fails is logged by its host, never its address — the
// address is the key.
func TestNotifyFailureNeverLogsTheWebhook(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	writeConfig(t, s, "")
	h := newHookRig(t, s, http.StatusNotFound)
	s.notifyAgent(notifyPane(proto.AgentDone), proto.AgentWorking)
	s.notifyWG.Wait()
	logs := h.logs.String()
	if !strings.Contains(logs, "notify slack:") || !strings.Contains(logs, "404") {
		t.Fatalf("failure not logged: %q", logs)
	}
	if strings.Contains(logs, "HOOKSECRET") || strings.Contains(logs, "/services/") {
		t.Fatalf("the webhook is in the log: %q", logs)
	}

	// A webhook on another host is refused before anything is sent, and
	// that is logged without it too.
	t.Setenv("SLACK_WEBHOOK_URL", "https://elsewhere.example/services/T0/B0/HOOKSECRET")
	h.logs.Reset()
	s.notifyAgent(notifyPane(proto.AgentDone), proto.AgentWorking)
	s.notifyWG.Wait()
	if logs := h.logs.String(); !strings.Contains(logs, "notify slack:") || strings.Contains(logs, "HOOKSECRET") {
		t.Fatalf("refused: %q", logs)
	}
	if n := len(h.bodies()); n != 1 {
		t.Fatalf("sent to another host: %d", n)
	}
}
