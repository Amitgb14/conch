package server

import (
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// A false "waiting" — or a waiting nobody is told about — is what makes
// people stop trusting a tool that watches agents. This soak drives an
// agent through its states many times over, the way a real one reports
// them (OSC 7501 on its terminal), while conch's own detection runs on as
// it does every tick, and fails on a single state that is missed, late or
// wrong. CONCH_SOAK_CYCLES runs it longer; the default keeps it in the
// suite under -race.
func TestStateSoak(t *testing.T) {
	cycles := 40
	if n, err := strconv.Atoi(os.Getenv("CONCH_SOAK_CYCLES")); err == nil && n > 0 {
		cycles = n
	}
	s, _, work := shareFixture(t)
	// The program reports whatever state it is sent, as an agent would —
	// awk rather than a shell loop, since a shell is never taken for the
	// agent.
	e := agentPane(t, s, "p1", "claude", work,
		`stty -echo; exec awk '{ printf "\033]7501;state=%s\007", $0; fflush() }'`)
	t.Cleanup(func() { e.p.Close() })

	// Detection keeps ticking meanwhile, as the server's watch loop does,
	// and every state it passes through is kept.
	var stop atomic.Bool
	var mu sync.Mutex
	var seen []string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		last := ""
		for !stop.Load() {
			s.observe(e)
			if a := e.info().Agent; a != nil && a.State != last {
				last = a.State
				mu.Lock()
				seen = append(seen, last)
				mu.Unlock()
			}
			time.Sleep(time.Millisecond)
		}
	}()

	steps := []struct{ send, want string }{
		{"working", proto.AgentWorking},
		{"blocked:kind=permission", proto.AgentBlocked},
		{"working", proto.AgentWorking},
		{"done", proto.AgentDone},
	}
	var worst time.Duration
	start := time.Now()
	for c := 0; c < cycles; c++ {
		for _, st := range steps {
			sent := time.Now()
			if err := e.p.SendText(st.send+"\r", false); err != nil {
				t.Fatal(err)
			}
			deadline := sent.Add(5 * time.Second)
			for {
				s.observe(e)
				if a := e.info().Agent; a != nil && a.State == st.want {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("cycle %d: sent %s, still %+v after 5s", c, st.send, e.info().Agent)
				}
				time.Sleep(time.Millisecond)
			}
			worst = max(worst, time.Since(sent))
		}
	}
	stop.Store(true)
	wg.Wait()
	elapsed := time.Since(start)

	// The sampler is slower than the states and misses some — that is the
	// sampler — but it must never see one that was not sent: no idle, no
	// unknown, nothing left over from the screen rules.
	mu.Lock()
	defer mu.Unlock()
	sent := map[string]bool{proto.AgentWorking: true, proto.AgentBlocked: true, proto.AgentDone: true}
	for i, got := range seen {
		if !sent[got] && i > 0 { // the first may be what the pane started as
			t.Errorf("detection showed %s, which was never reported (around %v)", got, seen[max(i-3, 0):min(i+3, len(seen))])
		}
	}
	if worst > time.Second {
		t.Errorf("a state took %v to show", worst)
	}
	t.Logf("%d cycles in %v; slowest state to show %v; sampler saw %d changes", cycles, elapsed, worst, len(seen))
}
