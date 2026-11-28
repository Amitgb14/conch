package server

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// tokenClock is a book's clock a test moves by hand.
type tokenClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *tokenClock) get() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *tokenClock) add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestBook(t *testing.T, dir string) (*tokenBook, *tokenClock) {
	t.Helper()
	b := loadTokens(dir)
	clock := &tokenClock{now: time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)}
	b.now = clock.get
	return b, clock
}

func mustCreate(t *testing.T, b *tokenBook, p proto.TokenCreateParams) proto.TokenCreateResult {
	t.Helper()
	res, perr := b.create(p, nil)
	if perr != nil {
		t.Fatalf("create %+v: %v", p, perr)
	}
	return res
}

func wantCode(t *testing.T, what string, perr *proto.Error, code string) {
	t.Helper()
	switch {
	case perr == nil && code != "":
		t.Errorf("%s: no error, want %s", what, code)
	case perr != nil && perr.Code != code:
		t.Errorf("%s: %s (%s), want %s", what, perr.Code, perr.Message, code)
	}
}

// A token made, checked, kept in the file as a hash only, read back by a
// server started later on the same directory, and revoked.
func TestTokenBookRoundTrip(t *testing.T) {
	dir := t.TempDir()
	b, _ := newTestBook(t, dir)
	res := mustCreate(t, b, proto.TokenCreateParams{Name: "ci", Level: proto.TokenPrompt})
	if !strings.HasPrefix(res.Token, "conch_t_") || res.Info.Name != "ci" || res.Info.Level != proto.TokenPrompt {
		t.Fatalf("made %+v", res)
	}
	if rec, ignored, perr := b.check(res.Token); perr != nil || ignored || rec.Name != "ci" {
		t.Fatalf("check: %+v %v %v", rec, ignored, perr)
	}

	raw, err := os.ReadFile(filepath.Join(dir, tokenFile))
	if err != nil {
		t.Fatal(err)
	}
	secret := res.Token[strings.LastIndex(res.Token, "_")+1:]
	if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte(res.Token)) {
		t.Fatal("tokens.json holds the secret, not only its hash")
	}
	if st, err := os.Stat(filepath.Join(dir, tokenFile)); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("tokens.json mode %v, %v", st.Mode().Perm(), err)
	}

	again, _ := newTestBook(t, dir) // the next server, after a reload or a restart
	if _, _, perr := again.check(res.Token); perr != nil {
		t.Fatalf("after loading again: %v", perr)
	}
	if list := again.list(); len(list) != 1 || list[0].ID != res.Info.ID {
		t.Errorf("list after loading again: %+v", list)
	}

	if _, perr := again.revoke("ci"); perr != nil {
		t.Fatal(perr)
	}
	_, _, perr := again.check(res.Token)
	wantCode(t, "revoked", perr, proto.ErrUnauthorized)
	_, perr = again.revoke("ci")
	wantCode(t, "revoked twice", perr, proto.ErrNotFound)
	third, _ := newTestBook(t, dir)
	_, _, perr = third.check(res.Token)
	wantCode(t, "revoked, loaded again", perr, proto.ErrUnauthorized)
}

// Revoking by id works as by name.
func TestTokenRevokeByID(t *testing.T) {
	b, _ := newTestBook(t, t.TempDir())
	res := mustCreate(t, b, proto.TokenCreateParams{Name: "ci", Level: proto.TokenRead})
	if _, perr := b.revoke(res.Info.ID); perr != nil {
		t.Fatal(perr)
	}
	if len(b.list()) != 0 {
		t.Error("still listed")
	}
}

// A forged secret, a token of the wrong kind and things that are not
// tokens at all are all refused, the first two in the same words as a
// revoked one.
func TestTokenForgedAndMalformed(t *testing.T) {
	b, _ := newTestBook(t, t.TempDir())
	res := mustCreate(t, b, proto.TokenCreateParams{Name: "ci", Level: proto.TokenFull})
	cut := strings.LastIndex(res.Token, "_")
	forged := res.Token[:cut+1] + strings.Repeat("0", 64)
	_, _, perr := b.check(forged)
	wantCode(t, "forged", perr, proto.ErrUnauthorized)
	if perr != nil && !strings.Contains(perr.Message, "unknown or revoked") {
		t.Errorf("forged says %q", perr.Message)
	}
	// The same secret dressed as a pane token: a person's token must not
	// pass for a pane's, which would place the connection in a pane.
	asPane := "conch_p_" + strings.TrimPrefix(res.Token, "conch_t_")
	_, _, perr = b.check(asPane)
	wantCode(t, "a person's token as a pane's", perr, proto.ErrUnauthorized)

	for _, bad := range []string{"", "   ", "conch_", "hello", res.Token[:len(res.Token)-1],
		res.Token + "0", "conch_x_" + strings.TrimPrefix(res.Token, "conch_t_"),
		strings.Replace(res.Token, "_", "-", -1), "conch_t_zzzzzzzz_00000000_" + strings.Repeat("0", 64)} {
		_, ignored, perr := b.check(bad)
		if ignored {
			t.Errorf("%q ignored", bad)
		}
		wantCode(t, "malformed "+bad, perr, proto.ErrUnauthorized)
	}
}

// A token stops working at its date, also for a connection already
// holding it; one made with Never does not; the default is thirty days.
func TestTokenExpiry(t *testing.T) {
	b, clock := newTestBook(t, t.TempDir())
	short := mustCreate(t, b, proto.TokenCreateParams{Name: "short", Level: proto.TokenRead, ExpiresIn: 60})
	forever := mustCreate(t, b, proto.TokenCreateParams{Name: "forever", Level: proto.TokenRead, Never: true})
	month := mustCreate(t, b, proto.TokenCreateParams{Name: "month", Level: proto.TokenRead})
	huge := mustCreate(t, b, proto.TokenCreateParams{Name: "huge", Level: proto.TokenRead, ExpiresIn: 1 << 62})
	if !forever.Info.Expires.IsZero() {
		t.Errorf("never expires on %v", forever.Info.Expires)
	}
	if got := month.Info.Expires.Sub(month.Info.Created); got != proto.TokenDefaultLife {
		t.Errorf("default life %v", got)
	}
	if huge.Info.Expires.Before(huge.Info.Created) {
		t.Errorf("an enormous life wrapped round to %v", huge.Info.Expires)
	}

	clock.add(59 * time.Second)
	if _, _, perr := b.check(short.Token); perr != nil {
		t.Fatalf("a second early: %v", perr)
	}
	clock.add(time.Second) // exactly at the date: over
	_, _, perr := b.check(short.Token)
	wantCode(t, "expired", perr, proto.ErrUnauthorized)
	if perr != nil && !strings.Contains(perr.Message, "expired") {
		t.Errorf("expired says %q", perr.Message)
	}
	wantCode(t, "expired, still held", b.still(short.Info.ID), proto.ErrUnauthorized)

	clock.add(100 * 365 * 24 * time.Hour)
	if _, _, perr := b.check(forever.Token); perr != nil {
		t.Errorf("never: %v", perr)
	}
}

// Another server's token: one the person made is refused by name, since
// somebody expected it to work; a pane's is ignored, since a process only
// inherited it.
func TestTokenOtherMachine(t *testing.T) {
	here, _ := newTestBook(t, t.TempDir())
	there, _ := newTestBook(t, t.TempDir())
	if here.store == there.store {
		t.Fatal("two stores, one mark")
	}
	res := mustCreate(t, there, proto.TokenCreateParams{Name: "ci", Level: proto.TokenRead})
	_, ignored, perr := here.check(res.Token)
	wantCode(t, "another machine's", perr, proto.ErrUnauthorized)
	if ignored || perr == nil || !strings.Contains(perr.Message, "another machine") {
		t.Errorf("another machine's: ignored %v, %v", ignored, perr)
	}
	pane := there.issuePane("p1")
	if _, ignored, perr := here.check(pane); perr != nil || !ignored {
		t.Errorf("another server's pane token: ignored %v, %v", ignored, perr)
	}
}

func TestTokenCreateRefuses(t *testing.T) {
	b, _ := newTestBook(t, t.TempDir())
	mustCreate(t, b, proto.TokenCreateParams{Name: "ci", Level: proto.TokenRead})
	for _, p := range []proto.TokenCreateParams{
		{Name: "", Level: proto.TokenRead},
		{Name: "  ", Level: proto.TokenRead},
		{Name: "ci", Level: proto.TokenFull}, // taken
		{Name: strings.Repeat("x", tokenNameMax+1), Level: proto.TokenRead},
		{Name: "pane p1", Level: proto.TokenRead},
		{Name: "two\nlines", Level: proto.TokenRead},
		{Name: "x", Level: ""},
		{Name: "x", Level: proto.TokenPane}, // never made by hand
		{Name: "x", Level: "admin"},
		{Name: "x", Level: proto.TokenRead, ExpiresIn: -1},
		{Name: "x", Level: proto.TokenRead, ExpiresIn: 5, Never: true},
	} {
		if _, perr := b.create(p, nil); perr == nil {
			t.Errorf("made %+v", p)
		}
	}
	mustCreate(t, b, proto.TokenCreateParams{Name: strings.Repeat("x", tokenNameMax), Level: proto.TokenRead})
	if n := len(b.list()); n != 2 {
		t.Errorf("%d tokens", n)
	}
}

// A pane's token is not the person's to list or revoke, ends with its
// pane, and does not outlive a restart.
func TestTokenPaneLifecycle(t *testing.T) {
	dir := t.TempDir()
	b, _ := newTestBook(t, dir)
	p1, p2 := b.issuePane("p1"), b.issuePane("p2")
	if !proto.IsPaneToken(p1) || p1 == p2 {
		t.Fatalf("pane tokens %q %q", p1, p2)
	}
	if rec, _, perr := b.check(p1); perr != nil || rec.Pane != "p1" || rec.Level != proto.TokenPane {
		t.Fatalf("check: %+v %v", rec, perr)
	}
	if len(b.list()) != 0 {
		t.Error("pane tokens listed as the person's")
	}
	if _, perr := b.revoke("pane p1"); perr == nil {
		t.Error("a pane token revoked by hand")
	}
	b.dropPane("p1")
	_, _, perr := b.check(p1)
	wantCode(t, "closed pane", perr, proto.ErrUnauthorized)
	if _, _, perr := b.check(p2); perr != nil {
		t.Fatalf("p2 went with p1: %v", perr)
	}

	again, _ := newTestBook(t, dir) // a reload: p2 adopted, so kept
	again.keepPanes(func(id string) bool { return id == "p2" })
	if _, _, perr := again.check(p2); perr != nil {
		t.Fatalf("an adopted pane's token after a reload: %v", perr)
	}
	again.keepPanes(func(string) bool { return false }) // a restart: none
	_, _, perr = again.check(p2)
	wantCode(t, "after a restart", perr, proto.ErrUnauthorized)
	third, _ := newTestBook(t, dir)
	_, _, perr = third.check(p2)
	wantCode(t, "after a restart, loaded again", perr, proto.ErrUnauthorized)
}

// What is on disk from an older build, or broken, loads as what can be
// trusted: nothing it cannot read grants anything.
func TestTokenLoadDamaged(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, tokenFile)
	for _, body := range []string{"", "{", "[]", `{"store":"short","tokens":null}`} {
		os.WriteFile(file, []byte(body), 0o600)
		b, _ := newTestBook(t, dir)
		if len(b.byID) != 0 || !validHex(b.store, 8) {
			t.Errorf("%q: %d tokens, store %q", body, len(b.byID), b.store)
		}
	}
	good := loadTokens(t.TempDir())
	res := mustCreate(t, good, proto.TokenCreateParams{Name: "ok", Level: proto.TokenRead})
	data := tokenFileData{Store: good.store, Tokens: []tokenRec{
		*good.byID[res.Info.ID],
		{ID: "aaaaaaaa", Name: "no hash", Level: proto.TokenRead},
		{ID: "bbbbbbbb", Name: "odd level", Level: "admin", Hash: "x"},
		{Name: "no id", Level: proto.TokenRead, Hash: "x"},
	}}
	raw, _ := json.Marshal(data)
	os.WriteFile(file, raw, 0o600)
	b, _ := newTestBook(t, dir)
	if len(b.byID) != 1 {
		t.Errorf("%d tokens kept, want the one good one", len(b.byID))
	}
	if _, _, perr := b.check(res.Token); perr != nil {
		t.Errorf("the good one: %v", perr)
	}
}

// No file to keep them in (a server made with no directory): tokens work
// for as long as it runs.
func TestTokenBookWithoutFile(t *testing.T) {
	b, _ := newTestBook(t, "")
	res := mustCreate(t, b, proto.TokenCreateParams{Name: "ci", Level: proto.TokenRead})
	if _, _, perr := b.check(res.Token); perr != nil {
		t.Fatal(perr)
	}
}

// Every method an agent in a pane is held back from needs a full token,
// unless it is one of the few that prompt or drive an agent; nothing that
// reads is a scoped method; and a new method is closed to the lesser
// levels by default.
func TestTokenNeedsEveryScopedMethod(t *testing.T) {
	for m := range scoped {
		got := tokenNeeds(m, tokenRef{}, proto.PaneCreateParams{Agent: "claude"})
		want := proto.TokenFull
		switch {
		case tokenPrompts[m]:
			want = proto.TokenPrompt
		case tokenDrives[m]:
			want = proto.TokenDrive
		}
		if got != want {
			t.Errorf("%s needs %s, want %s", m, got, want)
		}
		if tokenReads[m] {
			t.Errorf("%s is scoped and listed as reading", m)
		}
	}
	for m := range tokenReads {
		if tokenPrompts[m] || tokenDrives[m] {
			t.Errorf("%s is in two lists", m)
		}
	}
	if got := tokenNeeds("some.method.added.later", tokenRef{}, proto.PaneCreateParams{}); got != proto.TokenFull {
		t.Errorf("an unknown method needs %s", got)
	}
	// What a lesser token may do it may do only in its narrow form.
	for _, tc := range []struct {
		method string
		ref    tokenRef
		create proto.PaneCreateParams
		want   string
	}{
		{proto.MethodPaneCreate, tokenRef{}, proto.PaneCreateParams{Agent: "claude"}, proto.TokenDrive},
		{proto.MethodPaneCreate, tokenRef{}, proto.PaneCreateParams{}, proto.TokenFull}, // a shell
		{proto.MethodPaneCreate, tokenRef{}, proto.PaneCreateParams{Agent: "claude", Command: []string{"sh"}}, proto.TokenFull},
		{proto.MethodPaneCreate, tokenRef{}, proto.PaneCreateParams{Agent: "claude", SSH: "p1"}, proto.TokenFull},
		// Flags and an environment are the agent's to be told by the
		// person: only its --model is a drive token's to choose.
		{proto.MethodPaneCreate, tokenRef{}, proto.PaneCreateParams{Agent: "claude", AgentArgs: "--model opus"}, proto.TokenDrive},
		{proto.MethodPaneCreate, tokenRef{}, proto.PaneCreateParams{Agent: "claude", AgentArgs: "--model=\"claude-opus-4-1[1m]\""}, proto.TokenDrive},
		{proto.MethodPaneCreate, tokenRef{}, proto.PaneCreateParams{Agent: "claude", AgentArgs: "--model \"$(touch x)\""}, proto.TokenFull},
		{proto.MethodPaneCreate, tokenRef{}, proto.PaneCreateParams{Agent: "claude", AgentArgs: "--dangerously-skip-permissions"}, proto.TokenFull},
		{proto.MethodPaneCreate, tokenRef{}, proto.PaneCreateParams{Agent: "claude", AgentArgs: "--settings /tmp/hooks.json"}, proto.TokenFull},
		{proto.MethodPaneCreate, tokenRef{}, proto.PaneCreateParams{Agent: "claude", Env: []string{"NODE_OPTIONS=--require /tmp/x.js"}}, proto.TokenFull},
		// Adding or checking an ACP agent runs its command in the server.
		{proto.MethodAgentACPCheck, tokenRef{}, proto.PaneCreateParams{}, proto.TokenFull},
		{proto.MethodAgentACPAdd, tokenRef{}, proto.PaneCreateParams{}, proto.TokenFull},
		{proto.MethodAgentBroadcast, tokenRef{scopeRef: scopeRef{Shells: true}}, proto.PaneCreateParams{}, proto.TokenFull},
		{proto.MethodAgentBroadcast, tokenRef{}, proto.PaneCreateParams{}, proto.TokenPrompt},
		{proto.MethodFSRead, tokenRef{}, proto.PaneCreateParams{}, proto.TokenRead},
		{proto.MethodFSRead, tokenRef{scopeRef: scopeRef{SSH: "p1"}}, proto.PaneCreateParams{}, proto.TokenFull},
		{proto.MethodFSWrite, tokenRef{}, proto.PaneCreateParams{}, proto.TokenFull},
		{proto.MethodAgentLibrary, tokenRef{}, proto.PaneCreateParams{}, proto.TokenRead},
		{proto.MethodAgentLibrary, tokenRef{scopeRef: scopeRef{Import: "x"}}, proto.PaneCreateParams{}, proto.TokenFull},
		{proto.MethodAgentSync, tokenRef{scopeRef: scopeRef{Apply: true}}, proto.PaneCreateParams{}, proto.TokenFull},
		{proto.MethodTokenCreate, tokenRef{}, proto.PaneCreateParams{}, proto.TokenFull},
		{proto.MethodServerStop, tokenRef{}, proto.PaneCreateParams{}, proto.TokenFull},
		// Ended agents are listed as sessions are; bringing one back or
		// dropping it stays the person's.
		{proto.MethodPaneEndedList, tokenRef{}, proto.PaneCreateParams{}, proto.TokenRead},
		{proto.MethodPaneEndedResume, tokenRef{}, proto.PaneCreateParams{}, proto.TokenFull},
		{proto.MethodPaneEndedDismiss, tokenRef{}, proto.PaneCreateParams{}, proto.TokenFull},
	} {
		if got := tokenNeeds(tc.method, tc.ref, tc.create); got != tc.want {
			t.Errorf("%s %+v %+v needs %s, want %s", tc.method, tc.ref, tc.create, got, tc.want)
		}
	}
}

// A refusal is written down with the token's name.
func TestTokenRefusalIsLogged(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	perr := tokenRefused(&tokenRec{Name: "ci-bot", Level: proto.TokenRead}, "%s", "stop the server")
	if perr.Code != proto.ErrOutOfScope || !strings.Contains(perr.Message, `"ci-bot"`) {
		t.Errorf("refusal %+v", perr)
	}
	if !strings.Contains(buf.String(), `token "ci-bot" (read): refused stop the server`) {
		t.Errorf("log %q", buf.String())
	}
}

// Folders a call names are judged where they land.
func TestTokenDirs(t *testing.T) {
	t.Setenv("HOME", "/home/x")
	got := tokenDirs(tokenRef{Cwd: "/a/b/../c", Path: "rel", Root: "/r"})
	want := []string{"/a/c", "/r", "/r/rel"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("dirs %q, want %q", got, want)
	}
	if got := tokenDirs(tokenRef{Path: "rel"}); len(got) != 1 || got[0] != "" {
		t.Errorf("a relative path with no root: %q (must be refused, as \"\")", got)
	}
	if got := tokenDirs(tokenRef{Path: "~/p"}); len(got) != 1 || got[0] != "/home/x/p" {
		t.Errorf("~: %q", got)
	}
	if got := tokenDirs(tokenRef{}); len(got) != 0 {
		t.Errorf("none: %q", got)
	}
}
