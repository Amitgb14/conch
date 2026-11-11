package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/cloud/cloudtest"
)

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// login signs in through the fake, approving at once.
func login(t *testing.T, f *cloudtest.Fake) *Client {
	t.Helper()
	ctx := ctxT(t)
	da, err := StartLogin(ctx, f.URL(), ClientCLI, "test laptop", "0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	f.Approve("approved")
	c, err := FinishLogin(ctx, f.URL(), da)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNoAccountNoRequest(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	f.Forbidden = true
	if SignedIn() {
		t.Fatal("signed in with an empty CONCH_HOME")
	}
	if _, err := Open(); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("Open: %v", err)
	}
	if n := f.Total(); n != 0 {
		t.Fatalf("%d requests with no account", n)
	}
}

func TestDefaultURL(t *testing.T) {
	t.Setenv(URLEnv, "")
	if APIURL() != DefaultURL || DefaultURL != "https://api.cloud.conch.dev" {
		t.Fatalf("APIURL %q", APIURL())
	}
	t.Setenv(URLEnv, "http://127.0.0.1:1/")
	if APIURL() != "http://127.0.0.1:1" {
		t.Fatalf("APIURL %q", APIURL())
	}
}

func TestLoginPendingThenApproved(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	ctx := ctxT(t)
	da, err := StartLogin(ctx, f.URL(), ClientCLI, "test laptop", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(da.VerificationURIComplete, da.UserCode) {
		t.Fatalf("device auth %+v", da)
	}
	go func() {
		for f.Count("POST /v1/auth/token") < 3 {
			time.Sleep(time.Millisecond)
		}
		f.Approve("approved")
	}()
	c, err := FinishLogin(ctx, f.URL(), da)
	if err != nil {
		t.Fatal(err)
	}
	if n := f.Count("POST /v1/auth/token"); n < 4 {
		t.Fatalf("polled %d times; pending should have been polled through", n)
	}
	if !SignedIn() || c.Account().DeviceID == "" {
		t.Fatalf("signed in %v, account %+v", SignedIn(), c.Account())
	}
	me, err := c.Me(ctx)
	if err != nil || me.Handle != "ada" {
		t.Fatalf("me %+v %v", me, err)
	}
	// The handle is remembered without asking again.
	a, _ := LoadAccount()
	if a.Handle != "ada" {
		t.Fatalf("account %+v", a)
	}
}

func TestLoginSlowDown(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	ctx := ctxT(t)
	da, _ := StartLogin(ctx, f.URL(), ClientCLI, "x", "")
	f.Mu.Lock()
	f.Codes[da.DeviceCode].SlowDown = 2
	f.Codes[da.DeviceCode].State = "approved"
	f.Mu.Unlock()
	start := time.Now()
	if _, err := FinishLogin(ctx, f.URL(), da); err != nil {
		t.Fatal(err)
	}
	// 1 + 6 + 11 intervals: each slow_down adds five.
	if took := time.Since(start); took < 18*pollUnit {
		t.Fatalf("took %v: slow_down not honoured", took)
	}
}

func TestLoginDeniedExpiredSuspended(t *testing.T) {
	for _, c := range []struct {
		state string
		want  error
	}{{"denied", ErrLoginDenied}, {"suspended", ErrSuspended}} {
		isolate(t)
		f := cloudtest.New(t)
		ctx := ctxT(t)
		da, _ := StartLogin(ctx, f.URL(), ClientCLI, "x", "")
		f.Approve(c.state)
		if _, err := FinishLogin(ctx, f.URL(), da); !errors.Is(err, c.want) {
			t.Fatalf("%s: %v", c.state, err)
		}
		if SignedIn() {
			t.Fatalf("%s: signed in", c.state)
		}
	}
	// A code the server no longer knows.
	isolate(t)
	f := cloudtest.New(t)
	ctx := ctxT(t)
	da, _ := StartLogin(ctx, f.URL(), ClientCLI, "x", "")
	f.Mu.Lock()
	delete(f.Codes, da.DeviceCode)
	f.Mu.Unlock()
	if _, err := FinishLogin(ctx, f.URL(), da); !errors.Is(err, ErrLoginExpired) {
		t.Fatalf("expired: %v", err)
	}
	// And one that runs out while nobody approves it.
	da.ExpiresIn = 5
	da.DeviceCode = "unknown-but-pending"
	f.Mu.Lock()
	f.Codes[da.DeviceCode] = &cloudtest.DeviceCode{State: "pending"}
	f.Mu.Unlock()
	if _, err := FinishLogin(ctx, f.URL(), da); !errors.Is(err, ErrLoginExpired) {
		t.Fatalf("ran out: %v", err)
	}
}

func TestRefreshRotates(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	f.AccessTTL = 30 // under refreshEarly: every call refreshes first
	c := login(t, f)
	ctx := ctxT(t)
	first, _ := LoadAccount()
	if _, err := c.Machines(ctx); err != nil {
		t.Fatal(err)
	}
	second, _ := LoadAccount()
	if first.RefreshToken == second.RefreshToken || second.RefreshToken == "" {
		t.Fatal("the refresh token did not rotate")
	}
	if _, err := c.Machines(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.Count("POST /v1/auth/token"); n < 3 {
		t.Fatalf("%d token calls", n)
	}
}

// TestRefreshOn401: an access token the server stopped taking is renewed
// once and the call made again.
func TestRefreshOn401(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	c := login(t, f)
	f.Mu.Lock()
	f.Access = map[string]cloudtest.AccessTok{}
	f.Mu.Unlock()
	if _, err := c.Me(ctxT(t)); err != nil {
		t.Fatal(err)
	}
}

// TestTwoProcessesRefreshOnce: two clients (the TUI and a bridge it
// started) holding the same tokens must not both use the refresh token —
// the second use is reuse, which signs the device out.
func TestTwoProcessesRefreshOnce(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	f.AccessTTL = 30
	login(t, f)
	a, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	f.AccessTTL = 3600
	ctx := ctxT(t)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, c := range []*Client{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); _, errs[i] = c.Me(ctx) }()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("errors %v", errs)
	}
	if !SignedIn() {
		t.Fatal("signed out")
	}
	if n := f.Count("POST /v1/auth/token"); n != 2 { // the login, and one refresh
		t.Fatalf("%d token calls", n)
	}
}

// TestRevokedRefreshSignsOut: a refresh token the server refuses leaves
// this conch signed out, cleanly, and says so.
func TestRevokedRefreshSignsOut(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	f.AccessTTL = 30
	c := login(t, f)
	acc, _ := LoadAccount()
	f.Mu.Lock()
	f.Revoked[acc.DeviceID] = true
	f.Mu.Unlock()
	_, err := c.Machines(ctxT(t))
	if !errors.Is(err, ErrSignedOut) {
		t.Fatalf("err %v", err)
	}
	if SignedIn() {
		t.Fatal("still signed in")
	}
	if _, err := Open(); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("Open after: %v", err)
	}
}

func TestLogoutRevokes(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	c := login(t, f)
	if err := c.Logout(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if SignedIn() || f.Count("POST /v1/auth/revoke") != 1 {
		t.Fatalf("signed in %v, revokes %d", SignedIn(), f.Count("POST /v1/auth/revoke"))
	}
}

// TestLogoutOffline: the server cannot be told, and the sign-in is still
// forgotten here.
func TestLogoutOffline(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	c := login(t, f)
	f.Srv.Close()
	err := c.Logout(ctxT(t))
	if err == nil || !strings.Contains(err.Error(), "could not be told") {
		t.Fatalf("err %v", err)
	}
	if SignedIn() {
		t.Fatal("still signed in")
	}
}

func TestKeychainStore(t *testing.T) {
	isolate(t)
	k := &memKeychain{}
	old := SetKeychain(k)
	t.Cleanup(func() { SetKeychain(old) })
	f := cloudtest.New(t)
	c := login(t, f)
	b, _ := os.ReadFile(AccountPath())
	if strings.Contains(string(b), "cca_") || strings.Contains(string(b), "ccr_") || !strings.Contains(string(b), `"keychain"`) {
		t.Fatalf("cloud.json holds tokens with a Keychain: %s", b)
	}
	if len(k.items) != 1 {
		t.Fatalf("keychain %v", k.items)
	}
	if _, err := c.Me(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if err := c.Logout(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if len(k.items) != 0 {
		t.Fatal("the Keychain item outlived logout")
	}
}

func TestFileStoreRefusedWhenOthersCanRead(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	login(t, f)
	st, _ := os.Stat(AccountPath())
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("cloud.json is %04o", st.Mode().Perm())
	}
	os.Chmod(AccountPath(), 0o644)
	if _, err := Open(); err == nil || !strings.Contains(err.Error(), "others can read it") {
		t.Fatalf("0644: %v", err)
	}
}

func TestBadAccountFile(t *testing.T) {
	isolate(t)
	os.MkdirAll(os.Getenv("CONCH_HOME"), 0o700)
	os.WriteFile(AccountPath(), []byte("{not json"), 0o600)
	if _, err := Open(); err == nil {
		t.Fatal("a broken cloud.json opened")
	}
	os.WriteFile(AccountPath(), []byte(`{"store":"file"}`), 0o600)
	if _, err := Open(); err == nil || !strings.Contains(err.Error(), "names no server") {
		t.Fatalf("no api: %v", err)
	}
}

func TestProblemsAreTyped(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	c := login(t, f)
	ctx := ctxT(t)
	for i := range 3 {
		if _, err := c.CreateMachine(ctx, MachineCreate{Name: "m" + string(rune('a'+i)), Size: "standard"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	_, err := c.CreateMachine(ctx, MachineCreate{Name: "four", Size: "standard"}, "k1")
	if !errors.Is(err, ErrPlanLimit) || errors.Is(err, ErrAgentsWorking) {
		t.Fatalf("plan limit: %v", err)
	}
	if !strings.Contains(err.Error(), "allows 3 machines") {
		t.Fatalf("message %q", err)
	}
	if _, err := c.Machine(ctx, "m_0000000000000999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	ms, _ := c.Machines(ctx)
	f.SetMachine(ms[0].ID, func(m *cloudtest.Machine) {
		m.State = StateRunning
		m.Activity = &cloudtest.Activity{Agents: []cloudtest.AgentActivity{{Agent: "claude", State: "working"}}, ReportedAt: time.Now().UTC()}
	})
	if _, err := c.Sleep(ctx, ms[0].ID, false); !errors.Is(err, ErrAgentsWorking) {
		t.Fatalf("sleep: %v", err)
	}
	if m, err := c.Sleep(ctx, ms[0].ID, true); err != nil || m.State != StateAsleep {
		t.Fatalf("forced sleep: %+v %v", m, err)
	}
	if m, err := c.Wake(ctx, ms[0].ID); err != nil || m.State != StateWaking {
		t.Fatalf("wake: %+v %v", m, err)
	}
	if err := c.DeleteMachine(ctx, ms[1].ID); err != nil {
		t.Fatal(err)
	}
}

func TestProblemCodes(t *testing.T) {
	for code, want := range map[string]error{"no_provider": ErrNoProvider, "suspended": ErrSuspended, "agents_working": ErrAgentsWorking,
		"plan_limit": ErrPlanLimit, "contract_unsupported": ErrContractUnsupported} {
		p := &Problem{Status: 409, Code: code}
		if !errors.Is(p, want) {
			t.Errorf("%s is not %v", code, want)
		}
	}
	p := &Problem{Status: 426, Code: "contract_unsupported", ContractSupported: ">=2.0.0 <3.0.0", Detail: "No."}
	if !strings.Contains(p.Error(), ">=2.0.0") || !strings.Contains(p.Error(), "update conch") {
		t.Fatalf("message %q", p.Error())
	}
}

// TestRetryAfter: a 429 is tried again after retry_after; a 503 that
// lasts is given back.
func TestRetryAfter(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	c := login(t, f)
	n := 0
	inner := f.Srv.Config.Handler
	f.Srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/me" && n < 1 {
			n++
			cloudtest.WriteProblem(w, 429, "rate_limited", "Slow down.")
			return
		}
		inner.ServeHTTP(w, r)
	})
	if _, err := c.Me(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	f.Srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudtest.WriteProblem(w, 503, "unavailable", "Down for a moment.")
	})
	if _, err := c.Me(ctxT(t)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("503: %v", err)
	}
}

func TestEventsResume(t *testing.T) {
	isolate(t)
	f := cloudtest.New(t)
	c := login(t, f)
	var mu sync.Mutex
	var lastIDs []string
	streams := 0
	f.Srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		streams++
		n := streams
		lastIDs = append(lastIDs, r.Header.Get("Last-Event-ID"))
		mu.Unlock()
		if f.Spec != nil {
			for _, e := range f.Spec.CheckRequest(r.Method, r.URL.Path, r.Header, nil) {
				t.Errorf("request breaks the contract: %s", e)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		body := ": hi\n\n"
		if n == 1 {
			body += "id: 7\ndata: {\"type\":\"machine.state\",\"at\":\"2026-11-10T10:00:00Z\",\"machine_id\":\"m_0000000000000001\",\"data\":{\"state\":\"waking\"}}\n\n"
		} else {
			body += "id: 8\ndata: {\"type\":\"made.up\",\"at\":\"2026-11-10T10:00:01Z\"}\n\n" +
				"id: 9\ndata: {\"type\":\"machine.state\",\"at\":\"2026-11-10T10:00:02Z\",\"machine_id\":\"m_0000000000000001\",\"data\":{\"state\":\"running\"}}\n\n"
		}
		if f.Spec != nil {
			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Type", "text/event-stream")
			for _, e := range f.Spec.CheckResponse("GET", "/v1/events", 200, rec.Header(), []byte(body)) {
				if !strings.Contains(body, "made.up") || !strings.Contains(e, "made.up") {
					t.Errorf("the fake's events break the contract: %s", e)
				}
			}
		}
		w.Write([]byte(body))
	})
	ctx, cancel := context.WithCancel(ctxT(t))
	var got []Event
	err := c.Events(ctx, func(e Event) {
		got = append(got, e)
		if len(got) == 3 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Events: %v", err)
	}
	if got[0].Data["state"] != "waking" || got[2].ID != "9" {
		b, _ := json.Marshal(got)
		t.Fatalf("events %s", b)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lastIDs) < 2 || lastIDs[0] != "" || lastIDs[1] != "7" {
		t.Fatalf("Last-Event-ID %q", lastIDs)
	}
}
