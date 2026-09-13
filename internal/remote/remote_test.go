package remote

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amitghadge/conch/internal/proto"
)

func TestParseProbe(t *testing.T) {
	p, err := parseProbe("Linux\naarch64\nbin=/home/dev/.local/bin/conch\n" +
		`{"version":"0.1.0-dev","build":"abc","platform":"linux/arm64","capabilities":["pane.v1"]}` + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if p.Platform != "linux/arm64" || p.Bin != "/home/dev/.local/bin/conch" || p.Info == nil || p.Info.Build != "abc" {
		t.Fatalf("probe: %+v", p)
	}
	if missing := p.Missing(); len(missing) != len(proto.Capabilities)-1 {
		t.Fatalf("missing %v", missing)
	}

	bare, err := parseProbe("Darwin\nx86_64\n")
	if err != nil || bare.Platform != "darwin/amd64" || bare.Bin != "" || len(bare.Missing()) != len(proto.Capabilities) {
		t.Fatalf("bare probe: %+v %v", bare, err)
	}
	if _, err := parseProbe("FreeBSD\namd64\n"); err == nil {
		t.Fatal("FreeBSD accepted")
	}
	if _, err := parseProbe("Linux\nriscv64\n"); err == nil {
		t.Fatal("riscv64 accepted")
	}
}

func TestCatalog(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	a, err := SaveMachine(Machine{Target: "dev@build.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := SaveMachine(Machine{Target: "ssh://ops@build.example.com:2222"})
	c, _ := SaveMachine(Machine{Target: "local", Label: "local"})
	again, _ := SaveMachine(Machine{Target: "dev@build.example.com", Label: "builder"})
	if a.ID != "build-example-com" || b.ID != "build-example-com-2" || c.ID == "local" || again.ID != a.ID || again.Label != "builder" {
		t.Fatalf("ids: %q %q %q %q/%q", a.ID, b.ID, c.ID, again.ID, again.Label)
	}
	ms, _ := Machines()
	if len(ms) != 3 {
		t.Fatalf("machines: %+v", ms)
	}
	if FindMachine("builder").Target != "dev@build.example.com" || FindMachine("other.host").Target != "other.host" {
		t.Fatal("FindMachine")
	}
	if err := RemoveMachine(b.ID); err != nil {
		t.Fatal(err)
	}
	if ms, _ := Machines(); len(ms) != 2 {
		t.Fatalf("after remove: %+v", ms)
	}
}

func TestSSHHosts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".ssh", "conf.d"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(`
Include conf.d/*
Host gpu-box build
  HostName 10.0.0.2
Host *.internal !bastion
Host *
  ServerAliveInterval 30
`), 0o600)
	os.WriteFile(filepath.Join(home, ".ssh", "conf.d", "work"), []byte("Host work-vm\n"), 0o600)
	if got := strings.Join(SSHHosts(), ","); got != "work-vm,gpu-box,build" {
		t.Fatalf("hosts: %s", got)
	}
}

func TestSourceDirAndCrossBuild(t *testing.T) {
	src := SourceDir()
	if src == "" || !exists(filepath.Join(src, "cmd", "conch")) {
		t.Fatalf("source dir %q", src)
	}
	if testing.Short() {
		t.Skip("cross-build")
	}
	t.Setenv("CONCH_HOME", t.TempDir())
	var steps []string
	say := func(s string) { steps = append(steps, s) }
	bin, err := crossBuild(context.Background(), "linux/amd64", say)
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 4)
	f, _ := os.Open(bin)
	f.Read(head)
	f.Close()
	if string(head) != "\x7fELF" || len(steps) != 1 {
		t.Fatalf("binary %s head %q steps %v", bin, head, steps)
	}
	// Same client build: served from the cache without building again.
	if _, err := crossBuild(context.Background(), "linux/amd64", say); err != nil || len(steps) != 1 {
		t.Fatalf("second build: %v steps %v", err, steps)
	}
}
