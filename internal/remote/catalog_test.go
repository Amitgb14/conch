package remote

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestA4MachinesEmptyAndCorrupt(t *testing.T) {
	dir := a4Env(t)
	if ms, err := Machines(); err != nil || ms != nil {
		t.Fatalf("no catalog: %v %v", ms, err)
	}
	path := filepath.Join(dir, "machines.json")
	os.WriteFile(path, []byte("{not json"), 0o600)
	if _, err := Machines(); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("corrupt catalog: %v", err)
	}
	if _, err := SaveMachine(Machine{Target: "box"}); err == nil {
		t.Fatal("SaveMachine over a corrupt catalog")
	}
	if err := RemoveMachine("box"); err == nil || strings.Contains(err.Error(), "no machine") {
		t.Fatalf("RemoveMachine over a corrupt catalog: %v", err)
	}
	// FindMachine falls back to an ad-hoc target.
	if m := FindMachine("dev@box:22"); m.Target != "dev@box:22" || m.Label != "box" || m.ID != "box" || !m.Enabled {
		t.Fatalf("ad hoc: %+v", m)
	}

	os.Remove(path)
	os.Mkdir(path, 0o700) // unreadable as a file
	if _, err := Machines(); err == nil {
		t.Fatal("directory as catalog")
	}
}

func TestA4CatalogFileFormat(t *testing.T) {
	dir := a4Env(t)
	m, err := SaveMachine(Machine{Target: "ssh://ops@gpu.lab:2222", Label: "GPU Box #1"})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "gpu-box-1" || m.Label != "GPU Box #1" || !m.Enabled || m.Added.IsZero() {
		t.Fatalf("saved %+v", m)
	}
	path := filepath.Join(dir, "machines.json")
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("catalog mode %v %v", st, err)
	}
	b, _ := os.ReadFile(path)
	var file struct {
		Machines []map[string]any `json:"machines"`
	}
	if err := json.Unmarshal(b, &file); err != nil || len(file.Machines) != 1 || file.Machines[0]["target"] != "ssh://ops@gpu.lab:2222" {
		t.Fatalf("file %s: %v", b, err)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Fatal("no trailing newline")
	}

	// Removing the last machine leaves an empty list, not null.
	if err := RemoveMachine("GPU Box #1"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), `"machines": []`) {
		t.Fatalf("empty catalog %s", b)
	}
	if err := RemoveMachine("gpu-box-1"); err == nil || !strings.Contains(err.Error(), `no machine "gpu-box-1"`) {
		t.Fatalf("remove twice: %v", err)
	}
}

func TestA4SaveMachineReenables(t *testing.T) {
	dir := a4Env(t)
	os.WriteFile(filepath.Join(dir, "machines.json"),
		[]byte(`{"machines":[{"id":"box","label":"box","target":"dev@box","enabled":false}]}`), 0o600)
	m, err := SaveMachine(Machine{Target: "dev@box"})
	if err != nil || m.ID != "box" || m.Label != "box" || !m.Enabled {
		t.Fatalf("re-add keeps label, enables: %+v %v", m, err)
	}
	if ms, _ := Machines(); len(ms) != 1 || !ms[0].Enabled {
		t.Fatalf("persisted %+v", ms)
	}
	// Found by target too.
	if FindMachine("dev@box").ID != "box" {
		t.Fatal("FindMachine by target")
	}
}

func TestA4SaveMachineUnwritable(t *testing.T) {
	a4Env(t)
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	t.Setenv("CONCH_HOME", filepath.Join(file, "conch"))
	if _, err := SaveMachine(Machine{Target: "box"}); err == nil {
		t.Fatal("save under a file")
	}
	if err := saveMachines(nil); err == nil {
		t.Fatal("saveMachines under a file")
	}
}

func TestA4LabelAndSlug(t *testing.T) {
	for in, want := range map[string]string{
		"box":                      "box",
		"dev@box":                  "box",
		"ssh://dev@box.example:22": "box.example",
		"a@b@host":                 "host",
		"host:2222":                "host",
		"ssh://host":               "host",
	} {
		if got := labelFor(in); got != want {
			t.Errorf("labelFor(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"Build.Example.COM": "build-example-com",
		"--x--y--":          "x-y",
		"":                  "machine",
		"!!!":               "machine",
		"LOCAL":             "machine",
		"über box":          "ber-box",
		"a  b":              "a-b",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
	ms := []Machine{{ID: "box"}, {ID: "box-2"}}
	if got := uniqueID(ms, "box"); got != "box-3" {
		t.Fatalf("uniqueID %q", got)
	}
	if got := uniqueID(nil, "local"); got != "local-2" {
		t.Fatalf("local reserved: %q", got)
	}
}

func TestA4SSHHostsIncludes(t *testing.T) {
	a4Env(t)
	home := os.Getenv("HOME")
	ssh := filepath.Join(home, ".ssh")
	os.MkdirAll(filepath.Join(ssh, "d"), 0o700)
	abs := filepath.Join(t.TempDir(), "abs.conf")
	os.WriteFile(abs, []byte("Host abs-host\n"), 0o600)
	os.WriteFile(filepath.Join(ssh, "config"), []byte(
		"# comment\n"+
			"Include ~/.ssh/d/tilde.conf "+abs+"\n"+
			"HOST dup other?\n"+
			"Host\n"+
			"Include loop.conf\n"), 0o600)
	os.WriteFile(filepath.Join(ssh, "d", "tilde.conf"), []byte("Host tilde-host dup\n"), 0o600)
	// A self-including file stops at the depth limit instead of looping.
	os.WriteFile(filepath.Join(ssh, "loop.conf"), []byte("Host loop\nInclude loop.conf\n"), 0o600)

	got := strings.Join(SSHHosts(), ",")
	if got != "tilde-host,dup,abs-host,loop" {
		t.Fatalf("hosts %q", got)
	}
}

func TestA4SSHHostsNoConfig(t *testing.T) {
	a4Env(t)
	if hosts := SSHHosts(); len(hosts) != 0 {
		t.Fatalf("hosts without config: %v", hosts)
	}
	t.Setenv("HOME", "")
	if hosts := SSHHosts(); len(hosts) != 0 {
		t.Fatalf("hosts without HOME: %v", hosts)
	}
}
