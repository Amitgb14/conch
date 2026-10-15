package remote

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func forwardEnv(t *testing.T, taken map[int]bool) *fakeSSH {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CONCH_HOME", home)
	f := newFakeSSH(t)
	was := localFree
	localFree = func(port int) bool { return !taken[port] }
	t.Cleanup(func() { localFree = was })
	return f
}

func TestForwardPortAsksTheMaster(t *testing.T) {
	f := forwardEnv(t, nil)
	local, err := ForwardPort(context.Background(), "dev@box", 41000)
	if err != nil {
		t.Fatal(err)
	}
	if local != 41000 {
		t.Fatalf("forwarded to %d, want the same port when it is free here", local)
	}
	calls := f.calls(t)
	if len(calls) != 1 {
		t.Fatalf("ssh ran %d times", len(calls))
	}
	argv := calls[0]
	i := slices.Index(argv, "-O")
	if i < 0 || argv[i+1] != "forward" || !slices.Contains(argv, "-L") || !slices.Contains(argv, "41000:127.0.0.1:41000") {
		t.Fatalf("argv %q", argv)
	}
	if argv[len(argv)-1] != "dev@box" || argv[len(argv)-2] != "--" {
		t.Fatalf("target not last after --: %q", argv)
	}
}

func TestForwardPortTakesAnotherWhenItsOwnIsBusy(t *testing.T) {
	f := forwardEnv(t, map[int]bool{3000: true})
	local, err := ForwardPort(context.Background(), "dev@box", 3000)
	if err != nil {
		t.Fatal(err)
	}
	if local == 3000 || local == 0 {
		t.Fatalf("forwarded to %d, want some other free port", local)
	}
	if argv := f.calls(t)[0]; !slices.ContainsFunc(argv, func(a string) bool { return strings.HasSuffix(a, ":127.0.0.1:3000") && !strings.HasPrefix(a, "3000:") }) {
		t.Fatalf("argv %q", argv)
	}
}

func TestForwardPortFailures(t *testing.T) {
	forwardEnv(t, nil)
	for _, port := range []int{0, -1, 65536} {
		if _, err := ForwardPort(context.Background(), "dev@box", port); err == nil {
			t.Errorf("port %d was forwarded", port)
		}
	}
	// No master: said in words that mend it.
	t.Setenv("A4_STDERR", "Control socket connect(/tmp/x): No such file or directory\n")
	t.Setenv("A4_EXIT", "255")
	_, err := ForwardPort(context.Background(), "dev@box", 41000)
	if err == nil || !strings.Contains(err.Error(), "not connected to dev@box") {
		t.Fatalf("no master: %v", err)
	}
	// Anything else is passed on as ssh said it.
	t.Setenv("A4_STDERR", "mux_client_forward: forwarding request failed: Port forwarding failed\n")
	_, err = ForwardPort(context.Background(), "dev@box", 41000)
	if err == nil || !strings.Contains(err.Error(), "Port forwarding failed") {
		t.Fatalf("refused: %v", err)
	}
	// Silent failure still says something.
	t.Setenv("A4_STDERR", "")
	_, err = ForwardPort(context.Background(), "dev@box", 41000)
	if err == nil || !strings.Contains(err.Error(), "exit status 255") {
		t.Fatalf("silent: %v", err)
	}
	// A cancelled context stops it.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	t.Setenv("A4_EXIT", "0")
	if _, err := ForwardPort(ctx, "dev@box", 41000); err == nil {
		t.Fatal("a cancelled forward succeeded")
	}
}
