package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/cloud/cloudtest"
)

// TestMoveWakesBeforeConnecting: the move readies its destination before
// it connects there, and a machine that will not wake stops it, said as
// JSON for Conch.app.
func TestMoveWakesBeforeConnecting(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	oldWake, oldConnect := wakeMoveTarget, connectMoveTarget
	t.Cleanup(func() { wakeMoveTarget, connectMoveTarget = oldWake, oldConnect })
	var woke []string
	wakeMoveTarget = func(ref string) error {
		woke = append(woke, ref)
		return errors.New("box is in error: disk full")
	}
	connectMoveTarget = func(ref string) (*client.Client, error) {
		t.Errorf("connected to %s before it woke", ref)
		return nil, errors.New("no")
	}
	var err error
	out, _ := a4Capture(t, "", func() { err = projectMove(nil, []string{"api", "-to", "box", "-plan", "-json"}) })
	if err == nil || len(woke) != 1 || woke[0] != "box" || !strings.Contains(out, `"error":"box is in error: disk full"`) {
		t.Fatalf("err %v woke %v out %q", err, woke, out)
	}
}

// TestMoveWakesCloudDestination: a project moved to a Conch Cloud machine
// that sleeps wakes it and waits for it to run — moving there is asking
// for it, as a connection alone is not. A machine that is not a cloud one
// is left alone, and nothing is asked of Conch Cloud for it.
func TestMoveWakesCloudDestination(t *testing.T) {
	f, _ := cloudIsolated(t)
	if err := signIn(t, f); err != nil {
		t.Fatal(err)
	}
	m := f.AddMachine("box", "asleep", "", "")
	if err := runCloud([]string{"machine", "ls"}); err != nil { // into conch's machines
		t.Fatal(err)
	}
	wake := "POST /v1/machines/" + m.ID + "/wake"
	go func() {
		for f.Count(wake) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		f.SetMachine(m.ID, func(m *cloudtest.Machine) { m.State = "running" })
	}()
	if err := wakeMoveTarget("box"); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(wake); n != 1 {
		t.Fatalf("woken %d times", n)
	}
	// Running already: asked about, not woken again.
	if err := wakeMoveTarget("box"); err != nil || f.Count(wake) != 1 {
		t.Fatalf("running: %v, woken %d times", err, f.Count(wake))
	}
	// Not a cloud machine: nothing asked.
	before := f.Total()
	if err := wakeMoveTarget("devbox"); err != nil || f.Total() != before {
		t.Fatalf("devbox: %v, %d requests", err, f.Total()-before)
	}
	// One in error says so.
	f.SetMachine(m.ID, func(m *cloudtest.Machine) { m.State = "error"; m.StateDetail = "disk full" })
	if err := wakeMoveTarget("box"); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("error: %v", err)
	}
}

// TestCloudMachineSizes: conch cloud machine sizes says what the account
// may make, as a table and as JSON for Conch.app.
func TestCloudMachineSizes(t *testing.T) {
	f, out := cloudIsolated(t)
	if err := signIn(t, f); err != nil {
		t.Fatal(err)
	}
	out.b.Reset()
	if err := runCloud([]string{"machine", "sizes", "-json"}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Sizes []struct {
			ID    string   `json:"id"`
			VCPU  int      `json:"vcpu"`
			Plans []string `json:"plans"`
		} `json:"sizes"`
		Regions []struct {
			ID string `json:"id"`
		} `json:"regions"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil || len(got.Sizes) == 0 || len(got.Regions) == 0 {
		t.Fatalf("sizes %q %v", out.String(), err)
	}
	if got.Sizes[0].ID != "small" || got.Sizes[0].VCPU == 0 || len(got.Sizes[0].Plans) == 0 {
		t.Fatalf("first size %+v", got.Sizes[0])
	}
	out.b.Reset()
	if err := runCloud([]string{"machine", "sizes"}); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "standard") || !strings.Contains(s, "vCPU") {
		t.Fatalf("table %q", s)
	}
}
