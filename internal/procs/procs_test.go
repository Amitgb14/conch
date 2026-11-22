package procs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseCPUTime(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{"00:00:00", 0},
		{"00:01:02", 62 * time.Second}, // Linux
		{"01:02:03", time.Hour + 2*time.Minute + 3*time.Second},
		{"2-01:00:00", 49 * time.Hour},                          // Linux, over a day
		{"0:00.03", 30 * time.Millisecond},                      // macOS
		{"0:00.29", 290 * time.Millisecond},                     // no float noise
		{"123:45.67", 123*time.Minute + 45670*time.Millisecond}, // macOS minutes pass 59
		{"1:02:03.50", time.Hour + 2*time.Minute + 3500*time.Millisecond},
	} {
		got, ok := ParseCPUTime(c.in)
		if !ok || got != c.want {
			t.Errorf("ParseCPUTime(%q) = %v, %v; want %v", c.in, got, ok, c.want)
		}
	}
	for _, bad := range []string{"", "12", "1:2:3:4", "a:00", "00:xx", "-1:00", "x-00:00", "1:-5", "0:NaN", "0:+Inf", "1-2-00:00"} {
		if got, ok := ParseCPUTime(bad); ok {
			t.Errorf("ParseCPUTime(%q) = %v, want a failure", bad, got)
		}
	}
}

func TestParse(t *testing.T) {
	out := "    1     0  12000 00:00:05 systemd\n" + // Linux
		"  200     1   4096 0:01.50 /Applications/Some App.app/Contents/MacOS/Some App\n" + // macOS, spaces
		"garbage line\n" +
		"  300   200   notnum 0:00.00 x\n" +
		"  301   200   10 bad y\n" +
		"  -4    1   10 0:00.00 neg\n" +
		"  400   200   2048 0:00.00\n" + // no comm
		"\n"
	procs := Parse([]byte(out))
	if len(procs) != 3 {
		t.Fatalf("parsed %d processes: %+v", len(procs), procs)
	}
	if p := procs[1]; p.PPID != 0 || p.RSS != 12000 || p.CPU != 5*time.Second || p.Comm != "systemd" {
		t.Errorf("pid 1: %+v", p)
	}
	if p := procs[200]; p.Comm != "/Applications/Some App.app/Contents/MacOS/Some App" || p.CPU != 1500*time.Millisecond {
		t.Errorf("pid 200: %+v", p)
	}
	if p := procs[400]; p.RSS != 2048 || p.Comm != "" {
		t.Errorf("pid 400: %+v", p)
	}
	if got := Parse(nil); len(got) != 0 {
		t.Errorf("nil: %v", got)
	}
	// An overlong line is skipped along with the rest; nothing panics.
	if got := Parse([]byte(strings.Repeat("x", 2<<20))); len(got) != 0 {
		t.Errorf("overlong: %v", got)
	}
}

// The real ps, read-only: its format must parse on macOS and Linux alike.
func TestRealPS(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("no ps")
	}
	// What is checked is ps's format, not its speed: under the whole race
	// suite ps has taken longer than the monitor's own sampling interval,
	// and was killed by it (signal: killed). So it is given time here.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := RunPS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	procs := Parse(out)
	me, ok := procs[os.Getpid()]
	if !ok || me.RSS <= 0 || me.PPID != os.Getppid() {
		t.Fatalf("this process: %+v (found %v) among %d", me, ok, len(procs))
	}
}

func fakePS(t *testing.T, out string, err error) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	was := RunPS
	RunPS = func(context.Context) ([]byte, error) { calls.Add(1); return []byte(out), err }
	t.Cleanup(func() { RunPS = was })
	return &calls
}

func TestTake(t *testing.T) {
	fakePS(t, "1 0 10 0:00.00 init\n", errors.New("exit status 1"))
	if s := Take(time.Second); s.Err != nil || len(s.Procs) != 1 {
		t.Errorf("failed ps with a list: %+v", s)
	}
	fakePS(t, "", errors.New("exec: ps not found"))
	if s := Take(time.Second); s.Err == nil || !strings.Contains(s.Err.Error(), "not found") {
		t.Errorf("missing ps: %+v", s)
	}
	fakePS(t, "", nil)
	if s := Take(time.Second); s.Err == nil {
		t.Errorf("empty list: %+v", s)
	}
}

// ps lists processes one by one, so a pid reused mid-listing can make two
// processes each other's parent. A tree must still end.
func TestTreeParentLoop(t *testing.T) {
	ps := map[int]Proc{200: {PID: 200, PPID: 201}, 201: {PID: 201, PPID: 200}}
	got := Tree(200, map[int][]int{200: {201}, 201: {200}}, ps, map[int]bool{})
	if len(got) != 2 {
		t.Errorf("loop tree: %v", got)
	}
	// A root that is not running, or is already someone else's, is nothing.
	if got := Tree(999, nil, ps, map[int]bool{}); got != nil {
		t.Errorf("missing root: %v", got)
	}
	if got := Tree(200, nil, ps, map[int]bool{200: true}); got != nil {
		t.Errorf("claimed root: %v", got)
	}
}

func TestChildrenAndUse(t *testing.T) {
	t0 := time.Unix(1000, 0)
	prev := &Sample{At: t0, Procs: map[int]Proc{
		1: {PID: 1, CPU: time.Second}, 2: {PID: 2, PPID: 1, CPU: 5 * time.Second}, 3: {PID: 3, PPID: 1, CPU: 0}}}
	cur := &Sample{At: t0.Add(2 * time.Second), Procs: map[int]Proc{
		1: {PID: 1, RSS: 10, CPU: 2 * time.Second},
		2: {PID: 2, PPID: 1, RSS: 20, CPU: time.Second},            // reused: all its time is new
		4: {PID: 4, PPID: 1, RSS: 40, CPU: 300 * time.Millisecond}, // new
		5: {PID: 5, PPID: 5, RSS: 1}}}                              // its own parent
	ch := Children(cur.Procs)
	if got := ch[1]; len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Errorf("children of 1: %v", got)
	}
	if len(ch[5]) != 0 {
		t.Errorf("a process is its own child: %v", ch[5])
	}
	pids := []int{1, 2, 4}
	if got := cur.RSS(pids); got != 70 {
		t.Errorf("rss %d", got)
	}
	if got := Used(prev, cur, pids); got != 2300*time.Millisecond {
		t.Errorf("used %v", got)
	}
	if got := Used(nil, cur, pids); got != 3300*time.Millisecond {
		t.Errorf("used with no earlier sample %v", got)
	}
	if d, ok := Elapsed(prev, cur); !ok || d != 2*time.Second {
		t.Errorf("elapsed %v %v", d, ok)
	}
	for _, c := range []struct{ prev, cur *Sample }{
		{nil, cur}, {prev, nil}, {&Sample{At: t0, Err: errors.New("x")}, cur}, {cur, prev}, {cur, cur}} {
		if d, ok := Elapsed(c.prev, c.cur); ok {
			t.Errorf("elapsed %v from %+v", d, c)
		}
	}
}
