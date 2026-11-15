package desktop

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// host builds a Host from what is on its PATH, with no environment and no
// kernel release unless the test gives one.
func host(goos string, onPath ...string) Host {
	have := map[string]bool{}
	for _, p := range onPath {
		have[p] = true
	}
	return Host{
		GOOS:     goos,
		Getenv:   func(string) string { return "" },
		Release:  func() string { return "" },
		LookPath: func(name string) bool { return have[name] },
	}
}

func TestOpenCommand(t *testing.T) {
	const url = "https://example.com/a?b=1&c=2"
	wsl := func(h Host) Host {
		h.Getenv = func(k string) string {
			if k == "WSL_DISTRO_NAME" {
				return "Ubuntu"
			}
			return ""
		}
		return h
	}
	cases := []struct {
		name string
		h    Host
		want []string
	}{
		{"mac", host("darwin"), []string{"open", url}},
		{"linux", host("linux", "xdg-open"), []string{"xdg-open", url}},
		// Nothing found is still xdg-open, so the error names what is missing.
		{"linux with nothing", host("linux"), []string{"xdg-open", url}},
		{"wsl with wslu", wsl(host("linux", "wslview", "xdg-open", "rundll32.exe")), []string{"wslview", url}},
		// xdg-open is usually there on WSL with no browser behind it, so
		// Windows' own handler comes first.
		{"wsl without wslu", wsl(host("linux", "xdg-open", "rundll32.exe")), []string{"rundll32.exe", "url.dll,FileProtocolHandler", url}},
		// Interop turned off: no Windows programs on PATH, so the Linux way.
		{"wsl without interop", wsl(host("linux", "xdg-open")), []string{"xdg-open", url}},
		{"wsl by kernel release", func() Host {
			h := host("linux", "rundll32.exe")
			h.Release = func() string { return "5.15.167.4-microsoft-standard-WSL2\n" }
			return h
		}(), []string{"rundll32.exe", "url.dll,FileProtocolHandler", url}},
		// A Windows program on PATH is not WSL by itself (Wine, a mount).
		{"linux with a stray rundll32", host("linux", "xdg-open", "rundll32.exe"), []string{"xdg-open", url}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.h.OpenCommand(url); !reflect.DeepEqual(got, c.want) {
				t.Errorf("OpenCommand = %q, want %q", got, c.want)
			}
		})
	}
}

func TestIsWSL(t *testing.T) {
	for _, c := range []struct {
		name, goos, env, release string
		want                     bool
	}{
		{"distro variable", "linux", "Ubuntu", "", true},
		{"wsl2 kernel", "linux", "", "5.15.167.4-microsoft-standard-WSL2", true},
		{"wsl1 kernel", "linux", "", "4.4.0-19041-Microsoft", true},
		{"plain linux", "linux", "", "6.8.0-45-generic", false},
		{"empty release", "linux", "", "", false},
		// The variable can be inherited through ssh into a Mac; it is not WSL.
		{"mac with the variable", "darwin", "Ubuntu", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := host(c.goos)
			h.Getenv = func(k string) string {
				if k == "WSL_DISTRO_NAME" {
					return c.env
				}
				return ""
			}
			h.Release = func() string { return c.release }
			if got := h.IsWSL(); got != c.want {
				t.Errorf("IsWSL = %v, want %v", got, c.want)
			}
		})
	}
}

func TestReadRelease(t *testing.T) {
	f := filepath.Join(t.TempDir(), "osrelease")
	if err := os.WriteFile(f, []byte("5.15.0-microsoft-standard-WSL2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readFile(f); got != "5.15.0-microsoft-standard-WSL2\n" {
		t.Errorf("readFile = %q", got)
	}
	if got := readFile(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Errorf("missing file read as %q", got)
	}
}

func TestLocalHostIsComplete(t *testing.T) {
	h := Local()
	if h.GOOS == "" || h.Getenv == nil || h.Release == nil || h.LookPath == nil {
		t.Fatalf("Local() left a field empty: %+v", h)
	}
	// Whatever this machine is, it must name a program.
	if cmd := h.OpenCommand("https://example.com"); len(cmd) < 2 || cmd[len(cmd)-1] != "https://example.com" {
		t.Errorf("OpenCommand = %q", cmd)
	}
}
