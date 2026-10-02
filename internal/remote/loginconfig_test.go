package remote

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The login config is conch's config without connection sharing: the same
// includes and keys, so a login reaches what any other conch ssh reaches.
func TestLoginConfigSharesNothing(t *testing.T) {
	a4Env(t)
	home := os.Getenv("HOME")
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host bastion\n  HostName 192.0.2.1\n"), 0o600)
	os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("x"), 0o600)

	shared, login, err := sshConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if shared == login || filepath.Dir(shared) != filepath.Dir(login) || filepath.Base(login) != "login_config" {
		t.Fatalf("paths %q %q", shared, login)
	}
	sb, _ := os.ReadFile(shared)
	lb, _ := os.ReadFile(login)
	s, l := string(sb), string(lb)
	for _, want := range []string{"ControlMaster auto", "ControlPersist 60", "ControlPath "} {
		if !strings.Contains(s, want) {
			t.Fatalf("shared config lacks %q:\n%s", want, s)
		}
	}
	for _, want := range []string{"ControlMaster no", "ControlPath none", "ServerAliveInterval 15", "Include " + filepath.Join(home, ".ssh", "config"),
		"IdentityFile " + filepath.Join(home, ".ssh", "id_ed25519")} {
		if !strings.Contains(l, want) {
			t.Fatalf("login config lacks %q:\n%s", want, l)
		}
	}
	for _, not := range []string{"ControlMaster auto", "ControlPersist"} {
		if strings.Contains(l, not) {
			t.Fatalf("login config has %q:\n%s", not, l)
		}
	}
	if st, err := os.Stat(login); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("login config mode: %v %v", st, err)
	}

	// Asked again, the same files; after a reset (a key made since), both
	// are written again.
	if s2, l2, _ := sshConfigs(); s2 != shared || l2 != login {
		t.Fatal("paths changed")
	}
	os.Remove(login)
	resetConfig()
	if _, err := loginSSHConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(login); err != nil {
		t.Fatal("login config not written again after a reset")
	}
}

// What ssh itself makes of the configs, by ssh -G, which resolves a host's
// settings without connecting. A jump host's hop is started as
// "ssh -F <config> … -W host:port jumphost", so what -G says for the jump
// host under the login config is what that hop does.
func TestLoginConfigAsSSHReadsIt(t *testing.T) {
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("no ssh here")
	}
	a4Env(t)
	home := os.Getenv("HOME")
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	// The user's own sharing for one host still applies, as with plain ssh.
	user := "Host bastion\n  HostName 192.0.2.1\nHost own-mux\n  ControlMaster auto\n  ControlPath ~/.ssh/cm-%C\n"
	os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(user), 0o600)
	shared, login, err := sshConfigs()
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(cfg, host string) map[string]string {
		t.Helper()
		out, err := exec.Command(ssh, "-G", "-F", cfg, host).Output()
		if err != nil {
			t.Fatalf("ssh -G %s: %v", host, err)
		}
		m := map[string]string{}
		for _, line := range strings.Split(string(out), "\n") {
			if k, v, ok := strings.Cut(line, " "); ok {
				m[k] = v
			}
		}
		return m
	}
	if g := resolve(login, "bastion"); g["controlmaster"] != "false" || (g["controlpath"] != "" && g["controlpath"] != "none") || g["hostname"] != "192.0.2.1" {
		t.Fatalf("jump hop under the login config shares: master %q path %q host %q", g["controlmaster"], g["controlpath"], g["hostname"])
	}
	// ssh -G leaves controlpath out when it is none.
	if g := resolve(shared, "bastion"); g["controlmaster"] != "auto" || g["controlpath"] == "" || g["controlpath"] == "none" {
		t.Fatalf("shared config stopped sharing: %q %q", g["controlmaster"], g["controlpath"])
	}
	if g := resolve(login, "own-mux"); g["controlmaster"] != "auto" || !strings.Contains(g["controlpath"], "cm-") {
		t.Fatalf("user's own sharing overridden: %q %q", g["controlmaster"], g["controlpath"])
	}
}
