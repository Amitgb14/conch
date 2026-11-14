package tracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

func TestLinearCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "lin_api_ok" {
			http.Error(w, `{"errors":[{"message":"Authentication required"}]}`, http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":{"viewer":{"name":"Amit Ghadge","email":"amit@acme.dev","organization":{"name":"Acme"}},
			"teams":{"nodes":[{"key":"ENG","name":"Engineering"},{"key":"MOB","name":"Mobile"}]}}}`))
	}))
	defer srv.Close()

	a, err := NewLinear(srv.URL, "lin_api_ok").(Checker).Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a.Who != "Amit Ghadge" || a.Email != "amit@acme.dev" || a.Workspace != "Acme" || len(a.Scopes) != 2 || a.Scopes[1] != (Area{Key: "MOB", Name: "Mobile"}) {
		t.Fatalf("%+v", a)
	}
	// A wrong key says it was refused, which is what Settings shows.
	_, err = NewLinear(srv.URL, "lin_api_wrong").(Checker).Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refused the key") {
		t.Fatalf("wrong key: %v", err)
	}
	// A server that is not there says so rather than hang.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewLinear(srv.URL, "lin_api_ok").(Checker).Check(ctx); err == nil {
		t.Fatal("a cancelled check succeeded")
	}
}

func TestJiraCheck(t *testing.T) {
	projects := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "amit@acme.dev" || p != "tok" {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/rest/api/3/myself":
			w.Write([]byte(`{"displayName":"Amit G","emailAddress":"amit@acme.dev"}`))
		case "/rest/api/3/project/search":
			if !projects {
				http.Error(w, "", http.StatusForbidden)
				return
			}
			w.Write([]byte(`{"values":[{"key":"CLOUD","name":"Conch Cloud"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a, err := NewJira(srv.URL+"/", "amit@acme.dev", "tok").(Checker).Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(srv.URL, "http://")
	if a.Who != "Amit G" || a.Email != "amit@acme.dev" || a.Workspace != host || len(a.Scopes) != 1 || a.Scopes[0].Key != "CLOUD" {
		t.Fatalf("%+v", a)
	}
	// Projects it may not list: still connected, just without them.
	projects = false
	if a, err := NewJira(srv.URL, "amit@acme.dev", "tok").(Checker).Check(context.Background()); err != nil || len(a.Scopes) != 0 || a.Who != "Amit G" {
		t.Fatalf("no projects: %+v %v", a, err)
	}
	if _, err := NewJira(srv.URL, "amit@acme.dev", "nope").(Checker).Check(context.Background()); err == nil || !strings.Contains(err.Error(), "refused the key") {
		t.Fatalf("wrong token: %v", err)
	}
}

// Where a key comes from is said, never the key.
func TestKeyFrom(t *testing.T) {
	none := func(string) bool { return false }
	set := func(name string) func(string) bool { return func(n string) bool { return n == name } }
	cases := []struct {
		name  string
		cfg   config.TrackersCfg
		inEnv func(string) bool
		want  string
	}{
		{Linear, config.TrackersCfg{Linear: config.LinearCfg{APIKey: "lin_api_secret"}}, none, "config.toml"},
		{Linear, config.TrackersCfg{}, set("LINEAR_API_KEY"), "$LINEAR_API_KEY"},
		{Linear, config.TrackersCfg{Linear: config.LinearCfg{APIKeyEnv: "MY_LIN"}}, set("MY_LIN"), "$MY_LIN"},
		{Linear, config.TrackersCfg{}, none, "Keychain (LINEAR_API_KEY)"},
		{Jira, config.TrackersCfg{Jira: config.JiraCfg{APIToken: "t"}}, none, "config.toml"},
		{Jira, config.TrackersCfg{}, set("JIRA_API_TOKEN"), "$JIRA_API_TOKEN"},
		{"github", config.TrackersCfg{}, none, ""},
	}
	for _, c := range cases {
		got := KeyFrom(c.name, c.cfg, c.inEnv)
		if got != c.want {
			t.Errorf("%s %+v: %q, want %q", c.name, c.cfg, got, c.want)
		}
		if strings.Contains(got, "secret") {
			t.Errorf("the key itself was said: %q", got)
		}
	}
}
