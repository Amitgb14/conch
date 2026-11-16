package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// hook is a webhook's receiver: the address is the secret, so the path
// carries it and nothing else does.
type hook struct {
	srv  *httptest.Server
	mu   sync.Mutex
	got  []*http.Request
	body []string
}

func newHook(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) *hook {
	h := &hook{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.got = append(h.got, r)
		h.body = append(h.body, string(b))
		h.mu.Unlock()
		answer(w, r)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

// notifyFile is a shipped notify file with its api at the hook.
func notifyFile(t *testing.T, name string, h *hook) *File {
	f := shipped(t, name)
	f.API = h.srv.URL
	return f
}

var msg = Message{Title: "conch · claude", Body: "fix-uploads is waiting for you: Allow Bash(go test)?", URL: "https://github.com/acme/app/pull/7"}

// Slack's incoming webhook takes {"text"} as JSON and answers "ok" as
// text (api.slack.com/messaging/webhooks).
func TestSlackWebhook(t *testing.T) {
	h := newHook(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/services/T000/B000/XXXX" || r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, "no_service")
			return
		}
		io.WriteString(w, "ok")
	})
	f := notifyFile(t, "slack", h)
	c, err := f.Connect(h.srv.URL+"/services/T000/B000/XXXX", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal([]byte(h.body[0]), &got)
	if got["text"] != "*conch · claude*\nfix-uploads is waiting for you: Allow Bash(go test)?\nhttps://github.com/acme/app/pull/7" {
		t.Fatalf("sent %q", h.body[0])
	}
	if a := h.got[0].Header.Get("Authorization"); a != "" {
		t.Fatalf("a webhook sends no Authorization: %q", a)
	}

	// A webhook that is gone: said, without its address.
	c, _ = f.Connect(h.srv.URL+"/services/T000/B000/GONE", "", "")
	err = c.Send(context.Background(), msg)
	if err == nil || strings.Contains(err.Error(), "GONE") || strings.Contains(err.Error(), "/services/") || !strings.Contains(err.Error(), "404") {
		t.Fatalf("gone webhook: %v", err)
	}
}

// Discord's webhook takes {"content", "allowed_mentions"} and answers 204
// (discord.com/developers/docs/resources/webhook#execute-webhook).
func TestDiscordWebhook(t *testing.T) {
	h := newHook(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	f := notifyFile(t, "discord", h)
	c, err := f.Connect(h.srv.URL+"/api/webhooks/123/tok3n", "", "")
	if err != nil {
		t.Fatal(err)
	}
	m := msg
	m.URL = "" // nothing to link to: no line for it
	m.Body = "@everyone " + strings.Repeat("x", 3000)
	if err := c.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Content         string `json:"content"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	if err := json.Unmarshal([]byte(h.body[0]), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Content, "**conch · claude**\n@everyone x") || len(got.Content) > 2000 {
		t.Fatalf("content %d: %.80q", len(got.Content), got.Content)
	}
	// parse: [] — an empty list, not null, which is what stops a mention.
	if !strings.Contains(h.body[0], `"parse":[]`) {
		t.Fatalf("mentions not turned off: %s", h.body[0])
	}
	if h.got[0].URL.Path != "/api/webhooks/123/tok3n" {
		t.Fatalf("posted to %s", h.got[0].URL.Path)
	}
}

// ntfy takes the message as the body and the rest as headers
// (docs.ntfy.sh/publish: Title, Click, Tags), on the topic's address.
func TestNtfy(t *testing.T) {
	h := newHook(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"sPs71M8A2T","time":1700000000,"event":"message","topic":"conch-x7q"}`)
	})
	f := notifyFile(t, "ntfy", h)
	c, err := f.Connect(h.srv.URL+"/conch-x7q", "", "")
	if err != nil {
		t.Fatal(err)
	}
	m := msg
	m.Title = "conch · claude\non two lines"
	if err := c.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	r := h.got[0]
	if r.URL.Path != "/conch-x7q" || h.body[0] != msg.Body || r.Header.Get("Title") != "conch · claude on two lines" ||
		r.Header.Get("Click") != msg.URL || r.Header.Get("Tags") != "robot" || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("sent %s %q %v", r.URL.Path, h.body[0], r.Header)
	}
	// No URL: no Click header at all, rather than an empty one.
	m.URL = ""
	if err := c.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.got[1].Header["Click"]; ok {
		t.Fatal("an empty Click was sent")
	}

	// A server of your own: the topic must be on it.
	f.API = "https://ntfy.sh"
	if _, err := f.Connect(h.srv.URL+"/conch-x7q", "", h.srv.URL); err != nil {
		t.Fatalf("own server: %v", err)
	}
}

// A webhook's address is its key: one on another host is refused before
// anything is sent, and the refusal does not repeat it.
func TestWebhookOnlyToItsHost(t *testing.T) {
	h := newHook(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	f := notifyFile(t, "slack", h)
	for name, key := range map[string]string{
		"other host":    "https://evil.example/services/T/B/SECRETPART",
		"http":          "http://hooks.slack.com/services/T/B/SECRETPART",
		"not a url":     "SECRETPART",
		"user in it":    strings.Replace(h.srv.URL, "http://", "http://SECRETPART@", 1) + "/x",
		"another port":  "http://127.0.0.1:1/SECRETPART",
		"empty":         "",
		"shipped slack": "https://hooks.slack.com.evil.example/SECRETPART",
	} {
		_, err := f.Connect(key, "", "")
		if err == nil {
			t.Errorf("%s: taken", name)
			continue
		}
		if strings.Contains(err.Error(), "SECRETPART") {
			t.Errorf("%s: the key is in %v", name, err)
		}
	}
	if len(h.got) > 0 {
		t.Fatal("something was sent")
	}
	// A redirect off the webhook's host is not followed.
	away := newHook(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	h2 := newHook(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, away.srv.URL+"/took-it", http.StatusTemporaryRedirect)
	})
	f2 := notifyFile(t, "slack", h2)
	c, _ := f2.Connect(h2.srv.URL+"/services/T/B/SECRETPART", "", "")
	if err := c.Send(context.Background(), msg); err == nil || strings.Contains(err.Error(), "SECRETPART") {
		t.Fatalf("redirect: %v", err)
	}
	if len(away.got) > 0 {
		t.Fatal("followed a redirect to another host")
	}
}

// A request that fails before an answer names the host, never the
// address: Go's own error quotes the URL whole.
func TestWebhookErrorHidesAddress(t *testing.T) {
	h := newHook(t, func(w http.ResponseWriter, r *http.Request) {})
	f := notifyFile(t, "slack", h)
	h.srv.Close()
	c, err := f.Connect(h.srv.URL+"/services/T/B/SECRETPART", "", "")
	if err != nil {
		t.Fatal(err)
	}
	err = c.Send(context.Background(), msg)
	if err == nil || strings.Contains(err.Error(), "SECRETPART") || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("closed: %v", err)
	}
}
