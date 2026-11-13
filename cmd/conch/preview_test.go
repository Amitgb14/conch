package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/preview"
)

// fakePreview puts tools on the command that answer from a table and
// record what ran; nothing reaches a real simulator.
func fakePreview(t *testing.T, answers map[string]string) (*[]string, *bytes.Buffer) {
	t.Helper()
	var ran []string
	out := &bytes.Buffer{}
	oldTools, oldOut, oldM := previewTools, previewOut, machineFlag
	t.Cleanup(func() { previewTools, previewOut, machineFlag = oldTools, oldOut, oldM })
	machineFlag = ""
	previewOut = out
	previewTools = func() *preview.Tools {
		return &preview.Tools{
			Xcrun: "xcrun", Adb: "adb",
			Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
				line := strings.Join(append([]string{name}, args...), " ")
				ran = append(ran, line)
				if a, ok := answers[line]; ok {
					if strings.HasPrefix(a, "ERR ") {
						return nil, errors.New(a[4:])
					}
					return []byte(a), nil
				}
				return nil, nil
			},
			Start: func(string, ...string) error { return nil },
		}
	}
	return &ran, out
}

const previewSims = `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-2":[
 {"udid":"A1","name":"iPhone 16 Pro","state":"Booted","isAvailable":true},
 {"udid":"A2","name":"iPhone SE","state":"Shutdown","isAvailable":true}]}}`

func TestPreviewDevices(t *testing.T) {
	_, out := fakePreview(t, map[string]string{
		"xcrun simctl list devices available -j": previewSims,
		"adb devices -l":                         "ERR adb: cannot connect to daemon",
	})
	if err := runPreview([]string{"devices", "-json"}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Devices []preview.Device
		Notes   []string
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err, out.String())
	}
	if len(got.Devices) != 2 || got.Devices[0].ID != "A1" || !got.Devices[0].Booted() {
		t.Errorf("devices %+v", got.Devices)
	}
	if len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "cannot connect") {
		t.Errorf("notes %q", got.Notes)
	}
	out.Reset()
	if err := runPreview([]string{"devices"}); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(out.String()), "\n"); len(lines) != 2 || !strings.HasPrefix(lines[0], "booted ") || !strings.HasSuffix(lines[0], " A1") {
		t.Errorf("table:\n%s", out.String())
	}
}

// A computer with neither Xcode nor the Android SDK: an empty list and
// why, for the app; an error for a person.
func TestPreviewDevicesWithoutTools(t *testing.T) {
	_, out := fakePreview(t, nil)
	previewTools = func() *preview.Tools { return &preview.Tools{} }
	if err := runPreview([]string{"devices", "-json"}); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, `"devices":[]`) || !strings.Contains(s, "install Xcode") {
		t.Errorf("json: %s", s)
	}
	if err := runPreview([]string{"devices"}); !errors.Is(err, preview.ErrNoTools) {
		t.Errorf("table: %v", err)
	}
}

func TestPreviewShotTapTypeButton(t *testing.T) {
	ran, out := fakePreview(t, map[string]string{"xcrun simctl list devices available -j": previewSims})
	dir := t.TempDir()
	t.Chdir(dir)
	if err := runPreview([]string{"shot", "-o", "s.png"}); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "s.png")
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		// macOS's temporary folder is reached through /var → /private/var.
		if got := strings.TrimSpace(out.String()); got != want && got != filepath.Join(real, "s.png") {
			t.Errorf("printed %q", got)
		}
	}
	if last := (*ran)[len(*ran)-1]; !strings.HasPrefix(last, "xcrun simctl io A1 screenshot --type=png --mask=black ") || !strings.HasSuffix(last, "/s.png") {
		t.Errorf("ran %q", last)
	}
	// No -o: a fresh file in the temporary folder.
	out.Reset()
	if err := runPreview([]string{"shot"}); err != nil {
		t.Fatal(err)
	}
	if p := strings.TrimSpace(out.String()); !filepath.IsAbs(p) || !strings.Contains(filepath.Base(p), "conch-preview-") {
		t.Errorf("default path %q", p)
	}
	// iOS taps need idb, which these tools have not got: said, not done.
	if err := runPreview([]string{"tap", "10", "20"}); err == nil || !strings.Contains(err.Error(), "idb") {
		t.Errorf("tap: %v", err)
	}
	for _, c := range []struct {
		args []string
		err  string
	}{
		{[]string{"tap", "x", "2"}, "whole pixels"},
		{[]string{"tap", "1"}, "usage"},
		{[]string{"type"}, "usage"},
		{[]string{"button"}, "usage"},
		{[]string{"shot", "extra"}, "usage"},
		{[]string{"shot", "-device", "Galaxy"}, `no device "Galaxy"`},
		{[]string{"shot", "-device", "iPhone SE"}, `conch preview boot "iPhone SE"`},
		{[]string{"boot"}, "usage"},
		{[]string{"boot", ""}, "which device"},
		{[]string{"warp"}, "unknown preview subcommand"},
		{nil, "usage"},
		{[]string{"detect", "a", "b"}, "usage"},
		{[]string{"detect", filepath.Join(dir, "missing")}, "not a folder"},
	} {
		if err := runPreview(c.args); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%q: %v, want %q", c.args, err, c.err)
		}
	}
	out.Reset()
	if err := runPreview([]string{"boot", "iPhone SE"}); err != nil || !strings.Contains(out.String(), "iPhone SE is starting") {
		t.Errorf("boot: %v %q", err, out.String())
	}
	if last := (*ran)[len(*ran)-1]; last != "xcrun simctl boot A2" {
		t.Errorf("boot ran %q", last)
	}
}

func TestPreviewAndroidByName(t *testing.T) {
	ran, _ := fakePreview(t, map[string]string{"adb devices -l": "List of devices attached\n0A1B device model:Pixel_8\n"})
	for _, args := range [][]string{
		{"tap", "-device", "pixel 8", "5", "6"},
		{"type", "-device", "0A1B", "hi", "there"},
		{"button", "back"},
	} {
		if err := runPreview(args); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
	got := strings.Join(*ran, "\n")
	for _, w := range []string{"adb -s 0A1B shell input tap 5 6", "adb -s 0A1B shell input text hi%sthere", "adb -s 0A1B shell input keyevent KEYCODE_BACK"} {
		if !strings.Contains(got, w) {
			t.Errorf("never ran %q:\n%s", w, got)
		}
	}
}

func TestPreviewDetectAndMachine(t *testing.T) {
	_, out := fakePreview(t, nil)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte("name: app"), 0o644)
	if err := runPreview([]string{"detect", dir}); err != nil {
		t.Fatal(err)
	}
	var p preview.Project
	if err := json.Unmarshal(out.Bytes(), &p); err != nil || p.Kind != "flutter" || p.Dev != "flutter run" {
		t.Errorf("detect: %v %+v", err, p)
	}
	machineFlag = "busybox"
	if err := runPreview([]string{"devices"}); err == nil || !strings.Contains(err.Error(), "busybox itself") {
		t.Errorf("-m: %v", err)
	}
}

// The settings' [preview] reach the tools: an Xcode named there is the
// one the simulators are listed through.
func TestPreviewToolsReadSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CONCH_HOME", home)
	xcode := filepath.Join(t.TempDir(), "Xcode.app")
	os.MkdirAll(filepath.Join(xcode, "Contents", "Developer", "usr", "bin"), 0o755)
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[preview]\nxcode = \""+xcode+"\"\n"), 0o644)
	if got := previewTools().DeveloperDir; got != filepath.Join(xcode, "Contents", "Developer") {
		t.Errorf("DeveloperDir %q", got)
	}
	// A broken config.toml is not a reason to stop: the defaults are used.
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[preview\n"), 0o644)
	if previewTools() == nil {
		t.Error("no tools")
	}
}
