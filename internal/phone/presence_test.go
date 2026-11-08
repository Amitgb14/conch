package phone

import (
	"os"
	"testing"
	"time"
)

// A phone with the app open is on, by the route it came; closing it
// leaves when it was last seen.
func TestPresenceFromSockets(t *testing.T) {
	isolate(t)
	f := newFixture(t)
	p := f.pair(PermFull)
	if _, ok := f.store.Presence()[p.id]; ok {
		t.Fatal("a phone that never opened the app has a presence")
	}
	s := p.socket()
	waitFor(t, "the phone on", func() bool { return f.store.Presence()[p.id].Connected })
	if got := f.store.Presence()[p.id]; got.Route != RouteDirect || got.Since.IsZero() {
		t.Fatalf("on directly: %+v", got)
	}
	s.conn.CloseNow()
	waitFor(t, "the phone off", func() bool { return !f.store.Presence()[p.id].Connected })
	if got := f.store.Presence()[p.id]; got.LastSeen.IsZero() || got.Route != RouteDirect {
		t.Fatalf("off: %+v", got)
	}
}

func TestPresenceCounts(t *testing.T) {
	st := OpenStore(t.TempDir())
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	pid := os.Getpid()
	if len(st.Presence()) != 0 {
		t.Fatal("an empty store has presences")
	}
	code, _ := st.NewCode(PermFull, now)
	d, _, err := st.Redeem(code, "phone", now)
	if err != nil {
		t.Fatal(err)
	}
	// Two sockets: on until both close; since is the first's.
	st.SocketOpened(pid, d.ID, RouteRelay, now)                  //nolint:errcheck
	st.SocketOpened(pid, d.ID, RouteRelay, now.Add(time.Minute)) //nolint:errcheck
	st.SocketClosed(pid, d.ID, now.Add(2*time.Minute))           //nolint:errcheck
	if p := st.Presence()[d.ID]; !p.Connected || !p.Since.Equal(now) || p.Route != RouteRelay {
		t.Fatalf("one of two closed: %+v", p)
	}
	st.SocketClosed(pid, d.ID, now.Add(3*time.Minute)) //nolint:errcheck
	st.SocketClosed(pid, d.ID, now.Add(4*time.Minute)) //nolint:errcheck — one too many stays at zero
	if p := st.Presence()[d.ID]; p.Connected || !p.LastSeen.Equal(now.Add(4*time.Minute)) {
		t.Fatalf("both closed: %+v", p)
	}
	// Closing a device never seen writes nothing.
	if err := st.SocketClosed(pid, "nobody", now); err != nil {
		t.Fatal(err)
	}

	// A conch web that died with a socket open isn't believed, and the
	// next one starts its count again.
	st.SocketOpened(pid, d.ID, RouteDirect, now)                          //nolint:errcheck
	st.update(func(s *state) error { s.SeenPID = 999999999; return nil }) //nolint:errcheck
	if p := st.Presence()[d.ID]; p.Connected {
		t.Fatalf("a dead conch web's socket counts: %+v", p)
	}
	st.SocketOpened(pid, d.ID, RouteDirect, now) //nolint:errcheck
	st.SocketClosed(pid, d.ID, now)              //nolint:errcheck
	if p := st.Presence()[d.ID]; p.Connected {
		t.Fatalf("a dead conch web's count carried over: %+v", p)
	}

	// Revoked, it is forgotten.
	if _, err := st.Revoke(d.ID); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.read(); s.Seen[d.ID] != nil {
		t.Fatal("a revoked device's presence is kept")
	}
}

func TestSeenWords(t *testing.T) {
	now := time.Date(2026, 11, 8, 15, 0, 0, 0, time.Local)
	for _, c := range []struct {
		p    Presence
		want string
	}{
		{Presence{}, "never"},
		{Presence{Connected: true, Route: RouteRelay}, "on · relay"},
		{Presence{Connected: true, Route: RouteDirect}, "on · direct"},
		{Presence{Connected: true}, "on · direct"},
		{Presence{LastSeen: now.Add(-20 * time.Second)}, "just now"},
		{Presence{LastSeen: now.Add(-2 * time.Hour)}, "13:00"},
		{Presence{LastSeen: now.Add(-48 * time.Hour)}, "Nov 6"},
		{Presence{LastSeen: now.AddDate(-1, 0, 0)}, "Nov 8"},
	} {
		if got := SeenWords(c.p, now); got != c.want {
			t.Errorf("%+v: %q, want %q", c.p, got, c.want)
		}
	}
}
