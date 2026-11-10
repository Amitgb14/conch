package cloudagent

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnit(t *testing.T) {
	u := Unit("/usr/local/bin/conch")
	for _, want := range []string{
		"ExecStart=/usr/local/bin/conch cloud agent\n", "KillMode=process\n", "Restart=on-failure\n",
		"RestartPreventExitStatus=3 4\n", "User=root\n", "WantedBy=multi-user.target\n",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("the unit lacks %q:\n%s", want, u)
		}
	}
	// The control plane restarts the unit by this name.
	if UnitName != "conch-cloud-agent.service" {
		t.Fatal(UnitName)
	}
}

func TestInstall(t *testing.T) {
	dir := t.TempDir()
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	o := InstallOptions{Executable: "/opt/conch/bin/conch", UnitDir: filepath.Join(dir, "units"), StateDir: filepath.Join(dir, "state"), User: me.Username,
		Systemctl: func(_ context.Context, args ...string) error {
			calls = append(calls, strings.Join(args, " "))
			return nil
		}}
	path, err := Install(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "ExecStart=/opt/conch/bin/conch cloud agent -user "+me.Username+"\n") {
		t.Fatalf("unit:\n%s", b)
	}
	if st, _ := os.Stat(o.StateDir); st == nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("state dir %v", st)
	}
	if strings.Join(calls, ";") != "daemon-reload;enable conch-cloud-agent.service" {
		t.Fatalf("systemctl %v", calls)
	}

	o.User = "no-such-user-here"
	if _, err := Install(context.Background(), o); err == nil || !strings.Contains(err.Error(), "useradd") {
		t.Fatalf("no user: %v", err)
	}
	o.User, o.Executable = me.Username, "/my apps/conch"
	if _, err := Install(context.Background(), o); err == nil {
		t.Fatal("a path with a space was put in ExecStart")
	}
}

// TestImageScript: the script an image is built with parses, and readies
// the machine through conch cloud install.
func TestImageScript(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "cloud-image.sh")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("does not parse: %v\n%s", err, out)
	}
	for _, want := range []string{"conch cloud install -user", "/usr/local/bin", "useradd"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the image script lost %q", want)
		}
	}
}
