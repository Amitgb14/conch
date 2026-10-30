package remote

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestA4MachineOpts(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"none", nil, ""},
		{"empty", []string{}, ""},
		{"jump host", []string{"-J", "jump"}, "-J|jump"},
		{"a setting", []string{"-o", "KexAlgorithms=+diffie-hellman-group14-sha1"}, "-o|KexAlgorithms=+diffie-hellman-group14-sha1"},
		{"port and key", []string{"-p", "2222", "-i", "~/.ssh/old"}, "-p|2222|-i|~/.ssh/old"},
		{"a user", []string{"-l", "ops"}, "-l|ops"},
		{"compression and family", []string{"-4", "-C"}, "-4|-C"},
		// A terminal's own session: a forward that a second connection
		// would fail on, an agent it has no use for, a tty it is not.
		{"a local forward", []string{"-L", "8080:localhost:80"}, ""},
		{"a remote forward", []string{"-R", "9000:localhost:90"}, ""},
		{"a dynamic forward", []string{"-D", "1080"}, ""},
		{"agent forwarding", []string{"-A"}, ""},
		{"a tty", []string{"-t"}, ""},
		{"backgrounding", []string{"-f", "-N"}, ""},
		{"a forward by setting", []string{"-o", "LocalForward=8080 localhost:80"}, ""},
		{"a tty by setting", []string{"-o", "RequestTTY=force"}, ""},
		{"a command by setting", []string{"-o", "RemoteCommand=sh"}, ""},
		{"batch mode by setting", []string{"-o", "BatchMode=no"}, ""},
		{"kept beside dropped", []string{"-L", "80:x:80", "-p", "2222", "-A"}, "-p|2222"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := MachineOpts(c.args)
			if err != nil {
				t.Fatalf("%q: %v", c.args, err)
			}
			if strings.Join(got, "|") != c.want {
				t.Fatalf("%q kept %q, want %q", c.args, got, c.want)
			}
		})
	}

	// Nothing ssh would take as a list of options, said rather than
	// quietly dropped: a word that is not one, a value missing, and the
	// options that print or query instead of logging in.
	for _, args := range [][]string{{"-G"}, {"-V"}, {"-O", "check"}, {"-Q", "cipher"}, {"-p"}, {"box2"}, {"--"}, {"-z"}} {
		if got, err := MachineOpts(args); err == nil {
			t.Fatalf("%q was accepted as %q", args, got)
		}
	}

	// A machine's options are the same set a terminal's host keeps, so a
	// login read off the process table and a list typed by hand agree.
	l, ok := ParseLogin([]string{"ssh", "-p", "2222", "-L", "80:localhost:80", "--", "me@box"})
	opts, err := MachineOpts([]string{"-p", "2222", "-L", "80:localhost:80"})
	if !ok || err != nil || strings.Join(l.Opts, "|") != strings.Join(opts, "|") {
		t.Fatalf("a login keeps %q, a machine %q (%v %v)", l.Opts, opts, ok, err)
	}
}

// A machine's options reach ssh, after conch's own: ssh keeps the first
// value it is given for a setting, so a host cannot turn off the batch
// mode that keeps an unwatched run from hanging on a prompt.
func TestA4SSHCmdCarriesMachineOpts(t *testing.T) {
	a4Env(t)
	t.Setenv("CONCH_SSH", "/fake/ssh")
	cfg, _ := sshConfig()

	tr := SSH("dev@box", false, "-J", "jump", "-o", "Port=2222")
	cmd, err := tr.Command(context.Background(), "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/fake/ssh", "-F", cfg, "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		"-J", "jump", "-o", "Port=2222", "--", "dev@box", "echo hi"}
	if strings.Join(cmd.Args, "|") != strings.Join(want, "|") {
		t.Fatalf("args\n got %q\nwant %q", cmd.Args, want)
	}

	// The bridge runs with them too: the protocol goes down that very
	// connection, so a machine unreachable without them is unreachable.
	cmd, err = tr.forBridge().Command(context.Background(), "conch bridge")
	if err != nil || !strings.Contains(strings.Join(cmd.Args, "|"), "-J|jump|-o|Port=2222|--|dev@box") {
		t.Fatalf("bridge args %q (%v)", cmd.Args, err)
	}

	// And a machine with none is reached exactly as it always was.
	cmd, _ = SSH("dev@box", false).Command(context.Background(), "echo hi")
	plain, _ := sshCmd(context.Background(), "dev@box", "echo hi", false)
	if strings.Join(cmd.Args, "|") != strings.Join(plain.Args, "|") {
		t.Fatalf("no options changed the command: %q", cmd.Args)
	}
}

func TestA4CatalogKeepsMachineOpts(t *testing.T) {
	dir := a4Env(t)
	path := filepath.Join(dir, "machines.json")

	m, err := SaveMachine(Machine{Target: "me@old", Label: "old", Opts: []string{"-o", "Port=2222"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.Opts, "|") != "-o|Port=2222" {
		t.Fatalf("saved %+v", m)
	}
	var file struct {
		Machines []map[string]any `json:"machines"`
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Machines) != 1 || file.Machines[0]["opts"] == nil {
		t.Fatalf("file: %s", b)
	}

	// Options given again replace them; none given leaves them alone, as
	// a label does — there is nowhere else they could come back from.
	if m, err = SaveMachine(Machine{Target: "me@old", Opts: []string{"-J", "jump"}}); err != nil ||
		strings.Join(m.Opts, "|") != "-J|jump" {
		t.Fatalf("replaced: %+v %v", m, err)
	}
	if m, err = SaveMachine(Machine{Target: "me@old", Label: "renamed"}); err != nil ||
		strings.Join(m.Opts, "|") != "-J|jump" || m.Label != "renamed" {
		t.Fatalf("kept: %+v %v", m, err)
	}

	// Read back from the file they are the same: the filter on load must
	// not take a bite out of what the add just put there.
	if ms, err := Machines(); err != nil || len(ms) != 1 || strings.Join(ms[0].Opts, "|") != "-J|jump" {
		t.Fatalf("round trip: %+v %v", ms, err)
	}

	// A machine from a build before any of this loads with none, and is
	// reached the way it always was.
	os.WriteFile(path, []byte(`{"machines":[{"id":"box","label":"box","target":"me@box","enabled":true}]}`), 0o600)
	ms, err := Machines()
	if err != nil || len(ms) != 1 || ms[0].Opts != nil {
		t.Fatalf("older file: %+v %v", ms, err)
	}

	// A hand-edited file is cleaned on the way in, so every reader — the
	// TUI, the phone gateway, a command — reaches a machine one way.
	os.WriteFile(path, []byte(`{"machines":[{"id":"box","target":"me@box","enabled":true,`+
		`"opts":["-L","8080:localhost:80","-p","2222","-A"]}]}`), 0o600)
	if ms, err = Machines(); err != nil || len(ms) != 1 || strings.Join(ms[0].Opts, "|") != "-p|2222" {
		t.Fatalf("hand-edited: %+v %v", ms, err)
	}
	// One that says nothing ssh could use costs that machine its options
	// and nothing else: the machine is still there to connect to, and the
	// sidebar is not emptied over a typo nobody would be told about.
	os.WriteFile(path, []byte(`{"machines":[{"id":"box","target":"me@box","enabled":true,"opts":["box2"]}]}`), 0o600)
	if ms, err = Machines(); err != nil || len(ms) != 1 || ms[0].Opts != nil || ms[0].Target != "me@box" {
		t.Fatalf("nothing ssh could use: %+v %v", ms, err)
	}
	// A broken file is still a broken file.
	os.WriteFile(path, []byte(`{"machines":[{`), 0o600)
	if _, err = Machines(); err == nil {
		t.Fatal("half a file loaded")
	}
}
