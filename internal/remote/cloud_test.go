package remote

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/cloud"
)

func TestParseCloudTarget(t *testing.T) {
	for target, want := range map[string]string{
		"conch:m_abcdefgh12345678":   "m_abcdefgh12345678",
		"conch:m_short":              "",
		"conch:":                     "",
		"daytona:m_abcdefgh12345678": "",
		"user@host":                  "",
	} {
		id, ok := ParseCloudTarget(target)
		if id != want || ok != (want != "") {
			t.Errorf("%q: %q %v", target, id, ok)
		}
	}
	if !(Machine{Target: CloudTarget("m_abcdefgh12345678")}).IsCloud() || (Machine{Target: "box"}).IsCloud() {
		t.Fatal("IsCloud")
	}
	if (Machine{Target: CloudTarget("m_abcdefgh12345678")}).IsSandbox() {
		t.Fatal("a cloud machine is not a sandbox")
	}
}

func TestSyncCloudMachines(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	if _, err := SaveMachine(Machine{Label: "box", Target: "me@box"}); err != nil {
		t.Fatal(err)
	}
	a := cloud.Machine{ID: "m_aaaaaaaaaaaaaaaa", Name: "box", State: "running"} // a name already taken
	b := cloud.Machine{ID: "m_bbbbbbbbbbbbbbbb", Name: "cloud", State: "asleep"}
	gone := cloud.Machine{ID: "m_cccccccccccccccc", Name: "old", State: "destroying"}
	if err := SyncCloudMachines([]cloud.Machine{a, b, gone}); err != nil {
		t.Fatal(err)
	}
	ms, _ := Machines()
	if len(ms) != 3 {
		t.Fatalf("machines %+v", ms)
	}
	byTarget := map[string]Machine{}
	for _, m := range ms {
		byTarget[m.Target] = m
	}
	if m := byTarget["me@box"]; m.Label != "box" {
		t.Fatalf("the ssh machine was touched: %+v", m)
	}
	if m := byTarget[CloudTarget(a.ID)]; m.Label == "box" || m.ID == "box" || !m.Enabled {
		t.Fatalf("a cloud machine took a name in use: %+v", m)
	}
	if m := byTarget[CloudTarget(b.ID)]; m.Label != "cloud" || m.ID != "cloud" {
		t.Fatalf("cloud: %+v", m)
	}
	// Renamed there: kept here as it is (the ID is what references hold).
	b.Name = "renamed"
	before, _ := Machines()
	if err := SyncCloudMachines([]cloud.Machine{a, b}); err != nil {
		t.Fatal(err)
	}
	after, _ := Machines()
	if len(after) != len(before) {
		t.Fatalf("%d then %d", len(before), len(after))
	}
	// Gone from the account: gone from here.
	if err := SyncCloudMachines([]cloud.Machine{b}); err != nil {
		t.Fatal(err)
	}
	ms, _ = Machines()
	if len(ms) != 2 {
		t.Fatalf("after removing one: %+v", ms)
	}
	if err := ForgetCloudMachines(); err != nil {
		t.Fatal(err)
	}
	ms, _ = Machines()
	if len(ms) != 1 || ms[0].Target != "me@box" {
		t.Fatalf("after forgetting: %+v", ms)
	}
}

// TestCloudTransportCommands: exec and bridge are this conch's own
// commands, and a tunnel that failed (255) is a connection error worth
// trying again where a refusal (254) is not.
func TestCloudTransportCommands(t *testing.T) {
	defer CloudExecutableForTest("/opt/conch")()
	tr, err := CloudTransport("box", "m_aaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	cmd, _ := tr.Command(context.Background(), "uname -s")
	if got := strings.Join(cmd.Args, " "); got != "/opt/conch cloud exec m_aaaaaaaaaaaaaaaa -- uname -s" {
		t.Fatalf("exec %q", got)
	}
	bcmd, _ := tr.forBridge().Command(context.Background(), "'/x/conch' bridge")
	if got := strings.Join(bcmd.Args, " "); got != "/opt/conch cloud bridge m_aaaaaaaaaaaaaaaa" {
		t.Fatalf("bridge %q", got)
	}
	if tr.Describe() != "box" || tr.interactive() {
		t.Fatal("describe/interactive")
	}
	for _, c := range []struct {
		code int
		conn bool
	}{{255, true}, {254, false}, {1, false}} {
		err := exec.Command("/bin/sh", "-c", "exit "+strconv.Itoa(c.code)).Run()
		got := tr.failed(err, "conch: the relay for box: gone\n")
		var ce *ConnectionError
		if errors.As(got, &ce) != c.conn || !strings.Contains(got.Error(), "the relay for box: gone") {
			t.Errorf("%d: %v", c.code, got)
		}
	}
}

// TestCloudTransportNotSignedIn: a cloud machine left in the catalog by a
// conch that is no longer signed in asks the network nothing.
func TestCloudTransportNotSignedIn(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	t.Setenv(cloud.URLEnv, "http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := TransportFor(ctx, "box", CloudTarget("m_aaaaaaaaaaaaaaaa"), false)
	if !errors.Is(err, cloud.ErrNotSignedIn) {
		t.Fatalf("err %v", err)
	}
}
