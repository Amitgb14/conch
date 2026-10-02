package phone

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// The routes and their permissions are the contract's HTTP table
// (docs/plans/phone-api.md). A route added, dropped or given another
// permission shows here.
func TestRoutesAreTheContract(t *testing.T) {
	want := []string{
		"DELETE /api/push/subscribe view",
		"GET /api/agents view",
		"GET /api/hello view",
		"GET /api/panes view",
		"GET /api/projects view",
		"GET /api/push/key view",
		"POST /api/answer reply",
		"POST /api/close full",
		"POST /api/panes full",
		"POST /api/push/subscribe view",
		"POST /api/rename full",
		"POST /api/reply reply",
		"POST /api/task full",
	}
	var got []string
	for _, rt := range routes {
		got = append(got, rt.method+" "+rt.path+" "+rt.need)
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	wantSocket := map[string]string{"agents.watch": "view", "frame.open": "view", "frame.close": "view", "keys": "full", "ping": "view",
		"panes.watch": "view", "text": "full", "scroll": "view", "wheel": "reply", "resize": "reply"}
	if len(socketNeeds) != len(wantSocket) {
		t.Fatalf("socket messages: %v", socketNeeds)
	}
	for typ, need := range wantSocket {
		if socketNeeds[typ] != need {
			t.Errorf("%s needs %q, want %q", typ, socketNeeds[typ], need)
		}
	}
	// Every code the contract lists has its status.
	for code, status := range map[string]int{"bad_request": 400, "unauthorized": 401, "forbidden": 403, "not_found": 404,
		"agent_blocked": 409, "not_waiting": 409, "question_changed": 409, "no_choices": 422, "pair_expired": 410,
		"rate_limited": 429, "server_unavailable": 503} {
		if httpStatus[code] != status {
			t.Errorf("%s travels with %d, want %d", code, httpStatus[code], status)
		}
	}
}

func TestPermissionOrder(t *testing.T) {
	for _, c := range []struct {
		have, need string
		want       bool
	}{
		{"view", "view", true}, {"view", "reply", false}, {"view", "full", false},
		{"reply", "view", true}, {"reply", "reply", true}, {"reply", "full", false},
		{"full", "view", true}, {"full", "reply", true}, {"full", "full", true},
		{"", "view", false}, {"admin", "view", false}, {"full", "", false}, {"full", "admin", false},
	} {
		if got := allows(c.have, c.need); got != c.want {
			t.Errorf("allows(%q, %q) = %v", c.have, c.need, got)
		}
	}
}

// Every route, asked by nobody, by a stranger, by each permission and by a
// device since revoked: refused unless the device's permission reaches
// what the route needs.
func TestEveryRouteChecksPermission(t *testing.T) {
	f := newFixture(t)
	phones := map[string]*fakePhone{}
	for _, perm := range []string{PermView, PermReply, PermFull} {
		phones[perm] = f.pair(perm)
	}
	revoked := f.pair(PermFull)
	if ok, err := f.store.Revoke(revoked.id); !ok || err != nil {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	nobody := &fakePhone{t: t, base: f.web.URL}
	stranger := &fakePhone{t: t, base: f.web.URL, token: strings.Repeat("A", 43), csrf: csrfToken(strings.Repeat("A", 43))}

	code := func(p *fakePhone, rt route) (int, string) {
		status, b := p.do(rt.method, rt.path, struct{}{})
		var eb ErrorBody
		json.Unmarshal(b, &eb)
		if eb.Error == nil {
			return status, ""
		}
		return status, eb.Error.Code
	}
	for _, rt := range routes {
		name := rt.method + " " + rt.path
		for who, p := range map[string]*fakePhone{"nobody": nobody, "a stranger": stranger, "a revoked device": revoked} {
			if status, c := code(p, rt); status != 401 || c != CodeUnauthorized {
				t.Errorf("%s by %s: %d %s, want 401 unauthorized", name, who, status, c)
			}
		}
		for perm, p := range phones {
			status, c := code(p, rt)
			if allows(perm, rt.need) {
				if status == 401 || status == 403 {
					t.Errorf("%s with %s: refused (%d %s), though it needs %s", name, perm, status, c, rt.need)
				}
			} else if status != 403 || c != CodeForbidden {
				t.Errorf("%s with %s: %d %s, want 403 forbidden (needs %s)", name, perm, status, c, rt.need)
			}
		}
	}

	// The socket opens for view and above, and for nobody else.
	for who, token := range map[string]string{"nobody": "", "a stranger": stranger.token, "a revoked device": revoked.token} {
		conn, res, err := dialSocket(f.web.URL, token)
		if err == nil {
			conn.CloseNow()
			t.Errorf("the socket opened for %s", who)
		} else if res == nil || res.StatusCode != 401 {
			t.Errorf("the socket for %s: %v %v, want 401", who, res, err)
		}
	}
}

// Every socket message, by each permission: a device below what the
// message needs gets `forbidden` with the message's id, and nothing done.
func TestEverySocketMessageChecksPermission(t *testing.T) {
	f := newFixture(t)
	pane := f.pane("", "stty raw -echo; printf 'ready>'; exec cat")
	waitFor(t, "the pane's prompt", func() bool { return strings.Contains(f.screen(pane), "ready>") })

	for _, perm := range []string{PermView, PermReply, PermFull} {
		s := f.pair(perm).socket()
		for typ, need := range socketNeeds {
			if typ == MsgPing {
				continue // it is how the others are waited for; checked below
			}
			s.send(ClientMessage{Type: typ, ID: "m-" + typ, Pane: pane, Keys: []string{"Z"}})
			refused := false
			for _, m := range s.until("after-" + typ) {
				if m.Type == MsgError && m.ID == "m-"+typ && m.Error != nil && m.Error.Code == CodeForbidden {
					refused = true
				}
			}
			if refused == allows(perm, need) {
				t.Errorf("%s with %s (needs %s): refused = %v", typ, perm, need, refused)
			}
		}
		// Only full may type, and only its key arrived.
		typed := strings.Count(f.screen(pane), "Z")
		if perm != PermFull && typed != 0 {
			t.Fatalf("%s typed into the pane: %q", perm, f.screen(pane))
		}
		if perm == PermFull {
			waitFor(t, "full's key", func() bool { return strings.Count(f.screen(pane), "Z") == 1 })
		}
		s.conn.CloseNow()
	}
}

func TestPairing(t *testing.T) {
	f := newFixture(t)
	code, err := f.store.NewCode(PermReply, f.now())
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 7 || code[3] != '-' || len(codeDigits(code)) != 6 {
		t.Fatalf("code %q", code)
	}

	res := f.pairRaw(code, "  Amit's\tiPhone\x1b[31m  ")
	defer res.Body.Close()
	var pr PairResponse
	json.NewDecoder(res.Body).Decode(&pr)
	if res.StatusCode != 200 || pr.Permission != PermReply || !strings.HasPrefix(pr.DeviceID, "d_") || len(pr.DeviceID) != 6 {
		t.Fatalf("pair: %d %+v", res.StatusCode, pr)
	}
	var ck *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == CookieName {
			ck = c
		}
	}
	if ck == nil || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" || ck.MaxAge <= 0 {
		t.Fatalf("cookie %+v", ck)
	}

	// Kept hashed: neither the token nor the code is in the file, which
	// only its owner can read.
	b, err := os.ReadFile(f.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(ck.Value)) || bytes.Contains(b, []byte(codeDigits(code))) {
		t.Fatalf("a secret is in %s:\n%s", f.store.Path(), b)
	}
	if !bytes.Contains(b, []byte(hashSecret(ck.Value))) {
		t.Fatalf("the token's hash isn't in the file:\n%s", b)
	}
	if info, _ := os.Stat(f.store.Path()); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	devs, _ := f.store.Devices()
	if len(devs) != 1 || devs[0].ID != pr.DeviceID || devs[0].Name != "Amit's iPhone[31m" || devs[0].Permission != PermReply {
		t.Fatalf("devices %+v", devs)
	}

	// Single use.
	res2 := f.pairRaw(code, "again")
	res2.Body.Close()
	if res2.StatusCode != 410 {
		t.Fatalf("second use: %d", res2.StatusCode)
	}
	if devs, _ := f.store.Devices(); len(devs) != 1 {
		t.Fatalf("a used code paired again: %+v", devs)
	}
}

func pairStatus(t *testing.T, f *fixture, code string) (int, string, http.Header) {
	t.Helper()
	res := f.pairRaw(code, "x")
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	e := decodeResult(t, res.StatusCode, b, nil)
	if e == nil {
		return res.StatusCode, "", res.Header
	}
	return res.StatusCode, e.Code, res.Header
}

func TestPairingCodeExpires(t *testing.T) {
	f := newFixture(t)
	code, _ := f.store.NewCode(PermView, f.now())
	f.clock.Store(int64(PairTTL - time.Second))
	late, _ := f.store.NewCode(PermView, f.now()) // replaces the first
	if status, c, _ := pairStatus(t, f, code); status != 410 || c != CodePairExpired {
		t.Fatalf("a replaced code: %d %s", status, c)
	}
	f.clock.Store(int64(2*PairTTL - time.Second)) // exactly at the second code's end
	if status, c, _ := pairStatus(t, f, late); status != 410 || c != CodePairExpired {
		t.Fatalf("an expired code: %d %s", status, c)
	}
	if devs, _ := f.store.Devices(); len(devs) != 0 {
		t.Fatalf("paired: %+v", devs)
	}
	// Inside its time, a code still works.
	fresh, _ := f.store.NewCode(PermView, f.now())
	f.clock.Add(int64(PairTTL - 30*time.Second))
	if status, c, _ := pairStatus(t, f, fresh); status != 200 {
		t.Fatalf("a live code: %d %s", status, c)
	}
}

// Six digits could be found by trying, so the tries are what is limited:
// a few from one address in a minute, and a few wrong ones in all before
// the code outstanding is thrown away.
func TestPairingIsRateLimited(t *testing.T) {
	f := newFixture(t)
	code, _ := f.store.NewCode(PermFull, f.now())
	wrong := "000-000"
	if codeDigits(code) == "000000" {
		wrong = "111-111"
	}
	for i := range pairPerAddr {
		if status, c, _ := pairStatus(t, f, wrong); status != 410 {
			t.Fatalf("wrong guess %d: %d %s", i, status, c)
		}
	}
	status, c, h := pairStatus(t, f, code)
	if status != 429 || c != CodeRateLimited || h.Get("Retry-After") == "" {
		t.Fatalf("over the limit: %d %s Retry-After=%q", status, c, h.Get("Retry-After"))
	}
	// The window passes; the code does not come back: five wrong guesses
	// spent it.
	f.clock.Add(int64(pairWindow))
	if status, c, _ := pairStatus(t, f, code); status != 410 || c != CodePairExpired {
		t.Fatalf("the code after %d wrong guesses: %d %s", pairMaxFailures, status, c)
	}
	if devs, _ := f.store.Devices(); len(devs) != 0 {
		t.Fatalf("paired: %+v", devs)
	}

	// The overall limit holds whatever address the tries come from.
	f.forgetAttempts()
	for i := range pairOverall {
		if ok, _ := f.g.allowPair("10.0.0." + string(rune('a'+i))); !ok {
			t.Fatalf("attempt %d refused", i)
		}
	}
	if ok, wait := f.g.allowPair("10.9.9.9"); ok || wait <= 0 || wait > pairWindow {
		t.Fatalf("attempt over the overall limit: %v %v", ok, wait)
	}
	f.clock.Add(int64(pairWindow))
	if ok, _ := f.g.allowPair("10.9.9.9"); !ok {
		t.Fatal("still limited after the window")
	}
	f.g.mu.Lock()
	n := len(f.g.attempts)
	f.g.mu.Unlock()
	if n != 2 { // this address and the overall count; the quiet ones are forgotten
		t.Fatalf("%d addresses remembered", n)
	}
}

func TestPairingBadRequests(t *testing.T) {
	f := newFixture(t)
	post := func(body string) (int, string) {
		f.forgetAttempts()
		res, err := http.Post(f.web.URL+"/pair", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, decodeResult(t, res.StatusCode, b, nil).Code
	}
	for body, want := range map[string]int{
		"":                           400,
		"{":                          400,
		"[]":                         400,
		`{"code":""}`:                400,
		`{"code":"abc"}`:             400,
		`{"device_name":"x"}`:        400,
		`{"code":"12"}`:              410,
		`{"code":"1234567"}`:         410,
		`{"code":"123-456"}`:         410, // none outstanding
		strings.Repeat("x", 100_000): 400,
	} {
		if status, _ := post(body); status != want {
			t.Errorf("%.30q: %d, want %d", body, status, want)
		}
	}
	res, _ := http.Get(f.web.URL + "/pair")
	res.Body.Close()
	if res.StatusCode != 405 {
		t.Fatalf("GET /pair: %d", res.StatusCode)
	}
	// A page on another site can't pair this browser.
	req, _ := http.NewRequest("POST", f.web.URL+"/pair", strings.NewReader(`{"code":"123-456"}`))
	req.Header.Set("Origin", "https://evil.example")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("cross-site pair: %d", res.StatusCode)
	}
}

// A change needs hello's token as well as the cookie, and must not come
// from another site's page.
func TestCSRF(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermFull)
	body := ReplyRequest{Pane: "p999", Text: "hi"}

	if status, e := p.post("/api/reply", body, nil); status != 404 || e.Code != CodeNotFound {
		t.Fatalf("with the token: %d %+v", status, e)
	}
	for name, csrf := range map[string]string{"none": "", "wrong": "0123456789abcdef0123456789abcdef", "another device's": f.pair(PermFull).csrf} {
		q := *p
		q.csrf = csrf
		if status, e := q.post("/api/reply", body, nil); status != 403 || e.Code != CodeForbidden {
			t.Errorf("csrf %s: %d %+v", name, status, e)
		}
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", f.web.URL+"/api/reply", bytes.NewReader(b))
	req.AddCookie(&http.Cookie{Name: CookieName, Value: p.token})
	req.Header.Set(CSRFHeader, p.csrf)
	req.Header.Set("Origin", "https://evil.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("cross-site request: %d", res.StatusCode)
	}
	// The socket has no header to carry a token, so its Origin is checked.
	req, _ = http.NewRequest("GET", f.web.URL+SocketPath, nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: p.token})
	for k, v := range map[string]string{"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13",
		"Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==", "Origin": "https://evil.example"} {
		req.Header.Set(k, v)
	}
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("cross-site socket: %d", res.StatusCode)
	}
}

func TestHelloAndUnknownRoutes(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermView)
	var h Hello
	if status := p.get("/api/hello", &h); status != 200 {
		t.Fatal(status)
	}
	if h.APIVersion != 1 || h.ConchVersion != proto.Version || h.DeviceID != p.id || h.Permission != PermView || len(h.CSRFToken) != 32 {
		t.Fatalf("hello %+v", h)
	}

	// Unknown routes and methods answer in the error shape.
	if status, b := p.do("GET", "/api/nope", nil); status != 404 || decodeResult(t, status, b, nil).Code != CodeNotFound {
		t.Fatalf("unknown route: %d %s", status, b)
	}
	if status, b := p.do("PUT", "/api/hello", nil); status != 405 || decodeResult(t, status, b, nil).Code != CodeBadRequest {
		t.Fatalf("PUT hello: %d %s", status, b)
	}
	if status, b := p.do("POST", SocketPath, nil); status != 405 {
		t.Fatalf("POST socket: %d %s", status, b)
	}
}

func TestAgentsList(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermView)
	if got := p.agents(); got == nil || len(got) != 0 {
		t.Fatalf("no agents: %#v", got) // [] rather than null
	}
	_, b := p.do("GET", "/api/agents", nil)
	if string(b) != `{"agents":[]}` {
		t.Fatalf("empty list %s", b)
	}

	f.pane("", "exec sleep 30") // a shell is not an agent
	idle := f.pane("claude", "stty -echo; exec cat")
	working := f.pane("claude", "stty -echo; exec cat")
	asked := f.pane("claude", "stty -echo; exec cat")
	menu := f.pane("claude", "stty raw -echo; printf 'Do you want to proceed?\\r\\n❯ 1. Yes\\r\\n  2. No\\r\\n'; exec sleep 30")
	first := p.waiting(menu, func(q *Question) bool { return len(q.Choices) == 2 })
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: working, Agent: "claude", Event: "UserPromptSubmit"}, nil)
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: asked, Agent: "claude", Event: "Notification",
		NotificationType: "permission_prompt", Message: "Claude needs your permission to use Bash"}, nil)

	var agents []Agent
	waitFor(t, "four agents in their states", func() bool {
		agents = p.agents()
		return len(agents) == 4 && agents[0].State == StateWaiting && agents[1].State == StateWaiting &&
			agents[2].State == StateWorking && agents[3].State == StateIdle
	})
	// Waiting first, the longest wait first.
	if agents[0].Pane != menu || agents[1].Pane != asked || agents[2].Pane != working || agents[3].Pane != idle {
		t.Fatalf("order: %s %s %s %s", agents[0].Pane, agents[1].Pane, agents[2].Pane, agents[3].Pane)
	}
	a := agents[0]
	if a.Machine != "local" || a.Agent != "claude" || a.Since.IsZero() || a.Since.Location() != time.UTC || a.Question == nil {
		t.Fatalf("agent %+v", a)
	}
	want := []Choice{{Choice: "1", Label: "Yes", Default: true}, {Choice: "2", Label: "No"}}
	if a.Question.ID != first.ID || a.Question.Text != "Do you want to proceed?" || len(a.Question.Choices) != 2 ||
		a.Question.Choices[0] != want[0] || a.Question.Choices[1] != want[1] {
		t.Fatalf("question %+v", a.Question)
	}
	// A question a hook reported, with nothing on screen to choose from:
	// the hook's words, and no choices — but [] rather than null.
	q := agents[1].Question
	if q == nil || q.Text != "Claude needs your permission to use Bash" || q.Choices == nil || len(q.Choices) != 0 || q.ID == first.ID {
		t.Fatalf("hook question %+v", q)
	}
	if agents[2].Question != nil || agents[3].Question != nil {
		t.Fatal("a question on an agent that isn't waiting")
	}
	_, b = p.do("GET", "/api/agents", nil)
	if !bytes.Contains(b, []byte(`"failed":false`)) || bytes.Contains(b, []byte("null")) || bytes.Contains(b, []byte(`"blocked"`)) {
		t.Fatalf("list: %s", b)
	}
}

// lineAfter is the rest of the screen row that starts with marker: every
// byte the fake agent was sent in that phase, in order.
func lineAfter(screen, marker string) string {
	for _, line := range strings.Split(screen, "\n") {
		if rest, ok := strings.CutPrefix(line, marker); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// Answering sends exactly the keys for the choice, on the question shown —
// and nothing at all when the question is no longer that one.
func TestAnswerSendsTheKeysForTheQuestionShown(t *testing.T) {
	f := newFixture(t)
	view, p := f.pair(PermView), f.pair(PermReply)
	pane := f.asker()
	q1 := p.waiting(pane, func(q *Question) bool { return len(q.Choices) == 3 })
	if q1.Text != "Bash command go test ./... Do you want to proceed?" || !q1.Choices[0].Default || q1.Choices[2].Label != "No" {
		t.Fatalf("first question %+v", q1)
	}
	waitFor(t, "the agent reading", func() bool { return strings.Contains(f.screen(pane), "first:") })

	// Everything that must type nothing.
	idle := f.pane("claude", "stty -echo; exec cat")
	hooked := f.pane("claude", "stty -echo; exec cat")
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: hooked, Agent: "claude", Event: "Notification",
		NotificationType: "permission_prompt", Message: "Claude needs your permission"}, nil)
	hq := p.waiting(hooked, func(*Question) bool { return true })
	shell := f.pane("", "exec sleep 30")
	for _, c := range []struct {
		name   string
		by     *fakePhone
		req    AnswerRequest
		status int
		code   string
	}{
		{"view may not answer", view, AnswerRequest{Pane: pane, QuestionID: q1.ID, Choice: "1"}, 403, CodeForbidden},
		{"another question's id", p, AnswerRequest{Pane: pane, QuestionID: "q_000000000000", Choice: "1"}, 409, CodeQuestionChanged},
		{"another pane's question", p, AnswerRequest{Pane: pane, QuestionID: hq.ID, Choice: "1"}, 409, CodeQuestionChanged},
		{"a choice that isn't there", p, AnswerRequest{Pane: pane, QuestionID: q1.ID, Choice: "4"}, 400, CodeBadRequest},
		{"choice 0", p, AnswerRequest{Pane: pane, QuestionID: q1.ID, Choice: "0"}, 400, CodeBadRequest},
		{"a choice that isn't a number", p, AnswerRequest{Pane: pane, QuestionID: q1.ID, Choice: "yes"}, 400, CodeBadRequest},
		{"a choice written oddly", p, AnswerRequest{Pane: pane, QuestionID: q1.ID, Choice: "01"}, 400, CodeBadRequest},
		{"no choice", p, AnswerRequest{Pane: pane, QuestionID: q1.ID}, 400, CodeBadRequest},
		{"no question", p, AnswerRequest{Pane: pane, Choice: "1"}, 400, CodeBadRequest},
		{"a pane by name", p, AnswerRequest{Pane: "reviewer", QuestionID: q1.ID, Choice: "1"}, 400, CodeBadRequest},
		{"no such pane", p, AnswerRequest{Pane: "p999", QuestionID: q1.ID, Choice: "1"}, 404, CodeNotFound},
		{"an agent that isn't waiting", p, AnswerRequest{Pane: idle, QuestionID: q1.ID, Choice: "1"}, 409, CodeNotWaiting},
		{"a pane with no agent", p, AnswerRequest{Pane: shell, QuestionID: q1.ID, Choice: "1"}, 409, CodeNotWaiting},
		{"a question with no choices to read", p, AnswerRequest{Pane: hooked, QuestionID: hq.ID, Choice: "1"}, 422, CodeNoChoices},
	} {
		status, e := c.by.post("/api/answer", c.req, nil)
		if status != c.status || e == nil || e.Code != c.code {
			t.Errorf("%s: %d %+v, want %d %s", c.name, status, e, c.status, c.code)
		}
	}

	// The third choice, from a cursor on the first: down, down, Enter.
	var res AnswerResponse
	if status, e := p.post("/api/answer", AnswerRequest{Pane: pane, QuestionID: q1.ID, Choice: "3"}, &res); status != 200 {
		t.Fatalf("answer: %d %+v", status, e)
	}
	if res != (AnswerResponse{Pane: pane, Sent: true}) {
		t.Fatalf("answer result %+v", res)
	}
	// Exactly those bytes since the question appeared: the refusals above
	// typed nothing, and the answer typed nothing more.
	waitFor(t, "the first answer's keys", func() bool { return strings.Contains(f.screen(pane), "0d.") })
	if got := lineAfter(f.screen(pane), "first:"); got != "1b.5b.42.1b.5b.42.0d." {
		t.Fatalf("the agent was sent %q:\n%s", got, f.screen(pane))
	}

	// The agent now asks something else. A tap made on the first question
	// arrives late: refused, and the new question is left alone.
	q2 := p.waiting(pane, func(q *Question) bool { return q.ID != q1.ID && len(q.Choices) == 3 && q.Choices[2].Default })
	if q2.Text != "Do you want to make this edit to main.go?" || q2.Choices[1].Label != "Yes, allow all edits" {
		t.Fatalf("second question %+v", q2)
	}
	waitFor(t, "the agent reading again", func() bool { return strings.Contains(f.screen(pane), "second:") })
	if status, e := p.post("/api/answer", AnswerRequest{Pane: pane, QuestionID: q1.ID, Choice: "1"}, nil); status != 409 || e.Code != CodeQuestionChanged {
		t.Fatalf("a late answer: %d %+v", status, e)
	}
	// The first choice, from a cursor on the third: up, up, Enter. Then a
	// key of the test's own, which must be the very next thing to arrive.
	if status, e := p.post("/api/answer", AnswerRequest{Pane: pane, QuestionID: q2.ID, Choice: "1"}, nil); status != 200 {
		t.Fatalf("second answer: %d %+v", status, e)
	}
	f.call(proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: pane, Keys: []string{"x"}}, nil)
	waitFor(t, "the sentinel", func() bool { return strings.Contains(f.screen(pane), "78.") })
	if got := lineAfter(f.screen(pane), "second:"); got != "1b.5b.41.1b.5b.41.0d.78." {
		t.Fatalf("the second answer sent %q:\n%s", got, f.screen(pane))
	}
	// The cursor's own row: Enter alone.
	if keys, ok := choiceKeys(strings.Split(f.screen(pane), "\n"), "3"); !ok || len(keys) != 1 || keys[0] != "enter" {
		t.Fatalf("keys for the row under the cursor: %v %v", keys, ok)
	}
}

// A reply is refused while the agent waits on a question — typed in, its
// Enter would answer it — and the refusal comes through as its own code.
func TestReply(t *testing.T) {
	f := newFixture(t)
	view, p := f.pair(PermView), f.pair(PermReply)
	pane := f.pane("claude", "stty -echo; exec cat")

	if status, e := view.post("/api/reply", ReplyRequest{Pane: pane, Text: "hi"}, nil); status != 403 || e.Code != CodeForbidden {
		t.Fatalf("view replying: %d %+v", status, e)
	}
	for _, c := range []struct {
		req    ReplyRequest
		status int
	}{
		{ReplyRequest{Pane: pane}, 400},
		{ReplyRequest{Pane: pane, Text: " \n"}, 400},
		{ReplyRequest{Text: "hi"}, 400},
		{ReplyRequest{Pane: "reviewer", Text: "hi"}, 400},
		{ReplyRequest{Pane: "p999", Text: "hi"}, 404},
		{ReplyRequest{Pane: f.pane("", "exec sleep 30"), Text: "hi"}, 400}, // no agent there
	} {
		if status, e := p.post("/api/reply", c.req, nil); status != c.status {
			t.Errorf("%+v: %d %+v, want %d", c.req, status, e, c.status)
		}
	}

	var res ReplyResponse
	if status, e := p.post("/api/reply", ReplyRequest{Pane: pane, Text: "check finding three again"}, &res); status != 200 {
		t.Fatalf("reply: %d %+v", status, e)
	}
	if res != (ReplyResponse{Pane: pane, Agent: "claude", Turn: 1}) {
		t.Fatalf("reply result %+v", res)
	}
	// cat prints the line once Enter reaches it.
	waitFor(t, "the reply submitted", func() bool { return strings.Count(f.screen(pane), "check finding three again") == 1 })

	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: pane, Agent: "claude", Event: "PermissionRequest"}, nil)
	p.waiting(pane, func(*Question) bool { return true })
	status, e := p.post("/api/reply", ReplyRequest{Pane: pane, Text: "and now the docs"}, nil)
	if status != 409 || e.Code != CodeAgentBlocked || !strings.Contains(e.Message, "nothing was typed") {
		t.Fatalf("reply to a waiting agent: %d %+v", status, e)
	}
	// Nothing was typed: a marker sent now is the next thing on screen.
	f.call(proto.MethodPaneSendText, proto.PaneSendTextParams{ID: pane, Text: "marker-line"}, nil)
	f.call(proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: pane, Keys: []string{"enter"}}, nil)
	waitFor(t, "the marker", func() bool { return strings.Contains(f.screen(pane), "marker-line") })
	if s := f.screen(pane); strings.Contains(s, "and now the docs") {
		t.Fatalf("typed into a waiting agent:\n%s", s)
	}

	// Nothing a phone typed or an agent showed is in the log: kinds and
	// sizes only.
	log := f.logged()
	if !strings.Contains(log, "POST /api/reply 200") || !strings.Contains(log, "POST /api/reply 409 agent_blocked") {
		t.Fatalf("log:\n%s", log)
	}
	for _, secret := range []string{"check finding", "now the docs", p.token, p.csrf} {
		if strings.Contains(log, secret) {
			t.Fatalf("%q is in the log:\n%s", secret, log)
		}
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestProjectsAndTask(t *testing.T) {
	f := newFixture(t)
	reply, full := f.pair(PermReply), f.pair(PermFull)
	var list ProjectList
	if status := reply.get("/api/projects", &list); status != 200 || list.Projects == nil || len(list.Projects) != 0 {
		t.Fatalf("no projects: %d %#v", status, list)
	}

	// The agent a task starts is whatever the login shell runs: a fake one
	// that stays up and launches nothing.
	sh := filepath.Join(f.dir, "fake-shell")
	if err := os.WriteFile(sh, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", sh)
	repo := filepath.Join(f.dir, "api")
	os.MkdirAll(repo, 0o755)
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "config", "user.name", "t")
	gitIn(t, repo, "config", "user.email", "t@example.com")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	var proj proto.ProjectInfo
	f.call(proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj)
	waitFor(t, "the project's base", func() bool {
		reply.get("/api/projects", &list)
		return len(list.Projects) == 1 && list.Projects[0].Base == "main"
	})
	if got := list.Projects[0]; got.ID != proj.ID || got.Name != "api" || got.Path != repo {
		t.Fatalf("project %+v", got)
	}

	req := TaskRequest{Project: proj.ID, Agent: "claude", Name: "reviewer", Prompt: "Review the login change"}
	if status, e := reply.post("/api/task", req, nil); status != 403 || e.Code != CodeForbidden {
		t.Fatalf("reply starting a task: %d %+v", status, e)
	}
	for _, c := range []struct {
		req    TaskRequest
		status int
	}{
		{TaskRequest{Project: proj.ID}, 400},
		{TaskRequest{Project: proj.ID, Prompt: "  "}, 400},
		{TaskRequest{Prompt: "x"}, 400},
		{TaskRequest{Project: "rnope", Prompt: "x"}, 404},
		{TaskRequest{Project: proj.ID, Prompt: "x", Name: "p7"}, 400}, // a name that reads as a pane ID
	} {
		if status, e := full.post("/api/task", c.req, nil); status != c.status {
			t.Errorf("%+v: %d %+v, want %d", c.req, status, e, c.status)
		}
	}
	var panes proto.PaneList
	f.call(proto.MethodPaneList, nil, &panes)
	if len(panes.Panes) != 0 {
		t.Fatalf("a refused task started a pane: %+v", panes.Panes)
	}

	var res TaskResponse
	if status, e := full.post("/api/task", req, &res); status != 200 {
		t.Fatalf("task: %d %+v", status, e)
	}
	if !proto.IsPaneID(res.Pane) || !strings.HasPrefix(res.Worktree, filepath.Join(f.dir, "api.worktrees")) || res.Branch == "" {
		t.Fatalf("task result %+v", res)
	}
	f.call(proto.MethodPaneList, nil, &panes)
	if len(panes.Panes) != 1 || panes.Panes[0].ID != res.Pane || panes.Panes[0].Name != "reviewer" || panes.Panes[0].Cwd != res.Worktree {
		t.Fatalf("panes %+v", panes.Panes)
	}
}

func TestPushSubscriptions(t *testing.T) {
	f := newFixture(t)
	p, other := f.pair(PermView), f.pair(PermView)
	var k1, k2 PushKey
	if status := p.get("/api/push/key", &k1); status != 200 {
		t.Fatal(status)
	}
	other.get("/api/push/key", &k2)
	// An uncompressed P-256 point, base64url, and the same one each time.
	if len(k1.VAPIDPublicKey) != 87 || !strings.HasPrefix(k1.VAPIDPublicKey, "B") || k1 != k2 {
		t.Fatalf("keys %q %q", k1.VAPIDPublicKey, k2.VAPIDPublicKey)
	}

	keys := PushKeys{P256dh: "BNc", Auth: "au"}
	for _, bad := range []PushSubscription{
		{},
		{Endpoint: "http://fcm.googleapis.com/fcm/send/x", Keys: keys},
		{Endpoint: "https://push.example/x", Keys: keys}, // not a push service
		{Endpoint: "https://127.0.0.1/x", Keys: keys},
		{Endpoint: "https://", Keys: keys},
		{Endpoint: "javascript:alert(1)", Keys: keys},
		{Endpoint: "https://fcm.googleapis.com/fcm/send/x"},
		{Endpoint: "https://fcm.googleapis.com/fcm/send/x", Keys: PushKeys{P256dh: "BNc"}},
		{Endpoint: "https://fcm.googleapis.com/fcm/send/x", Keys: keys, On: []string{"working"}},
	} {
		if status, _ := p.post("/api/push/subscribe", bad, nil); status != 400 {
			t.Errorf("%+v: %d", bad, status)
		}
	}
	sub := PushSubscription{Endpoint: "https://fcm.googleapis.com/fcm/send/x", Keys: keys, On: []string{"waiting", "done"}}
	for range 2 { // subscribing again replaces, it doesn't pile up
		if status, b := p.do("POST", "/api/push/subscribe", sub); status != 204 || len(b) != 0 {
			t.Fatalf("subscribe: %d %s", status, b)
		}
	}
	if status, _ := other.post("/api/push/subscribe", PushSubscription{Endpoint: "https://web.push.apple.com/y", Keys: keys}, nil); status != 204 {
		t.Fatal(status)
	}
	push := func(id string) []PushSubscription {
		devs, _ := f.store.Devices()
		for _, d := range devs {
			if d.ID == id {
				return d.Push
			}
		}
		return nil
	}
	if got := push(p.id); len(got) != 1 || got[0].Endpoint != sub.Endpoint || len(got[0].On) != 2 {
		t.Fatalf("stored %+v", got)
	}
	if got := push(other.id); len(got) != 1 || len(got[0].On) != 1 || got[0].On[0] != "waiting" {
		t.Fatalf("default events %+v", got)
	}
	// One device can't drop another's subscription.
	if status, _ := other.do("DELETE", "/api/push/subscribe", PushSubscription{Endpoint: sub.Endpoint}); status != 204 {
		t.Fatal(status)
	}
	if len(push(p.id)) != 1 {
		t.Fatal("a device dropped another's subscription")
	}
	if status, _ := p.do("DELETE", "/api/push/subscribe", PushSubscription{Endpoint: sub.Endpoint}); status != 204 || len(push(p.id)) != 0 {
		t.Fatalf("unsubscribe: %d %+v", status, push(p.id))
	}
	if status, _ := p.do("DELETE", "/api/push/subscribe", struct{}{}); status != 400 {
		t.Fatal(status)
	}
}

// With no conch server to reach, a request says so; who the device is
// still answers.
func TestServerUnavailable(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	store := OpenStore(dir)
	g := newGateway(store, func() (*client.Client, error) { return nil, errors.New("no server") }, nil, pushOptions{client: noNetwork})
	web := httptest.NewServer(g.Handler())
	t.Cleanup(func() { g.Close(); web.Close() })
	f := &fixture{t: t, dir: dir, store: store, g: g, web: web}
	p := f.pair(PermFull)

	for _, path := range []string{"/api/agents", "/api/projects"} {
		status, b := p.do("GET", path, nil)
		if status != 503 || decodeResult(t, status, b, nil).Code != CodeServerUnavailable {
			t.Errorf("%s: %d %s", path, status, b)
		}
	}
	for path, body := range map[string]any{
		"/api/reply":  ReplyRequest{Pane: "p1", Text: "hi"},
		"/api/answer": AnswerRequest{Pane: "p1", QuestionID: "q_1", Choice: "1"},
		"/api/task":   TaskRequest{Project: "r1", Prompt: "x"},
	} {
		if status, e := p.post(path, body, nil); status != 503 || e.Code != CodeServerUnavailable {
			t.Errorf("%s: %d %+v", path, status, e)
		}
	}
	s := p.socket()
	msgs := s.closed()
	if len(msgs) != 1 || msgs[0].Type != MsgBye || msgs[0].Reason != ByeServerGone {
		t.Fatalf("socket without a server: %+v", msgs)
	}
	g.Close()
	g.Close() // twice is fine
	if status, e := p.post("/api/reply", ReplyRequest{Pane: "p1", Text: "hi"}, nil); status != 503 || e.Code != CodeServerUnavailable {
		t.Fatalf("after close: %d %+v", status, e)
	}
}

// A request body is a reply or a prompt, not a file.
func TestOversizedBody(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermReply)
	status, e := p.post("/api/reply", ReplyRequest{Pane: "p1", Text: strings.Repeat("x", maxBody)}, nil)
	if status != 400 || e.Code != CodeBadRequest {
		t.Fatalf("%d %+v", status, e)
	}
	if status, b := p.do("POST", "/api/reply", nil); status != 400 {
		t.Fatalf("no body: %d %s", status, b)
	}
}

// A browser on a plain-http page other than this computer drops the
// device's cookie, so pairing there is refused before the code is spent —
// instead of making a device that can never get in. Behind tailscale
// serve, the https address given as -url is the gateway's own.
func TestPairingNeedsHTTPS(t *testing.T) {
	f := newFixture(t)
	code, _ := f.store.NewCode(PermReply, f.now())
	post := func(origin, host string) (int, *APIError) {
		t.Helper()
		f.forgetAttempts()
		body, _ := json.Marshal(PairRequest{Code: code, DeviceName: "phone"})
		req, _ := http.NewRequest("POST", f.web.URL+"/pair", bytes.NewReader(body))
		req.Header.Set("Origin", origin)
		if host != "" {
			req.Host = host
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, decodeResult(t, res.StatusCode, b, nil)
	}
	for _, origin := range []string{"http://100.101.102.103:8722", "http://laptop.tail1234.ts.net", "http://192.168.1.20:8722"} {
		host := strings.TrimPrefix(origin, "http://")
		status, e := post(origin, host)
		if status != 400 || e.Code != CodeBadRequest || !strings.Contains(e.Message, "pairing needs HTTPS") {
			t.Fatalf("%s: %d %+v", origin, status, e)
		}
	}
	if devs, _ := f.store.Devices(); len(devs) != 0 {
		t.Fatalf("a device made over http: %+v", devs)
	}

	// tailscale serve in front: the page is https://….ts.net, the Host
	// the gateway sees may be its own address. Refused as another site
	// until the gateway is told that address is its own.
	if status, e := post("https://laptop.tail1234.ts.net", "100.101.102.103:8722"); status != 403 || e.Code != CodeForbidden {
		t.Fatalf("an unannounced public address: %d %+v", status, e)
	}
	for _, bad := range []string{"", "laptop.ts.net", "ftp://x", "https://"} {
		if f.g.AllowOrigin(bad) == nil {
			t.Errorf("AllowOrigin(%q) took it", bad)
		}
	}
	if err := f.g.AllowOrigin("https://Laptop.tail1234.ts.net/"); err != nil {
		t.Fatal(err)
	}
	if status, e := post("https://evil.example", "100.101.102.103:8722"); status != 403 {
		t.Fatalf("another site, with a public address set: %d %+v", status, e)
	}
	// The same code still works: the refusals above didn't spend it.
	if status, e := post("https://laptop.tail1234.ts.net", "100.101.102.103:8722"); status != 200 {
		t.Fatalf("from the public address: %d %+v", status, e)
	}

	// On this computer plain http is a secure context and keeps the cookie.
	code, _ = f.store.NewCode(PermReply, f.now())
	if status, e := post(f.web.URL, ""); status != 200 {
		t.Fatalf("from loopback: %d %+v", status, e)
	}
	code, _ = f.store.NewCode(PermReply, f.now())
	if status, e := post("http://localhost:8722", "localhost:8722"); status != 200 {
		t.Fatalf("from localhost: %d %+v", status, e)
	}

	// The socket takes the public address too, and still no other site.
	p := f.pair(PermView)
	for origin, want := range map[string]int{"https://laptop.tail1234.ts.net": 101, "https://evil.example": 403} {
		req, _ := http.NewRequest("GET", f.web.URL+SocketPath, nil)
		req.AddCookie(&http.Cookie{Name: CookieName, Value: p.token})
		for k, v := range map[string]string{"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13",
			"Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==", "Origin": origin} {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("socket from %s: %d, want %d", origin, res.StatusCode, want)
		}
	}
}
