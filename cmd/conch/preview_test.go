package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
			Pipe: func(_ context.Context, w io.Writer, name string, args ...string) error {
				line := strings.Join(append([]string{name}, args...), " ")
				ran = append(ran, line)
				if a, ok := answers[line]; ok && strings.HasPrefix(a, "ERR ") {
					return errors.New(a[4:])
				}
				io.WriteString(w, answers[line]) //nolint:errcheck
				return nil
			},
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

// ▶ Run on an Xcode project: built for the simulator running, installed
// and opened, the build shown as it goes and what was opened said last.
func TestPreviewRunXcode(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Conch.xcodeproj"), 0o755)
	proj := filepath.Join(dir, "Conch.xcodeproj")
	args := "-project " + proj + " -scheme Conch -configuration Debug -destination id=A1"
	ran, out := fakePreview(t, map[string]string{
		"xcrun simctl list devices available -j":            previewSims,
		"xcrun xcodebuild -list -json -project " + proj:     `{"project":{"name":"Conch","schemes":["Conch","ConchKit"]}}`,
		"xcrun xcodebuild " + args + " build":               "** BUILD SUCCEEDED **\n",
		"xcrun xcodebuild -showBuildSettings -json " + args: `[{"target":"Conch","buildSettings":{"WRAPPER_EXTENSION":"app","TARGET_BUILD_DIR":"/dd","WRAPPER_NAME":"Conch.app","PRODUCT_BUNDLE_IDENTIFIER":"dev.conch.mobile"}}]`,
	})
	t.Chdir(dir)
	if err := runPreview([]string{"run"}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(*ran, "\n")
	for _, w := range []string{"xcrun simctl install A1 /dd/Conch.app", "xcrun simctl launch --terminate-running-process A1 dev.conch.mobile"} {
		if !strings.Contains(got, w) {
			t.Errorf("never ran %q:\n%s", w, got)
		}
	}
	if s := out.String(); !strings.Contains(s, "BUILD SUCCEEDED") || !strings.HasSuffix(s, "conch: Conch (dev.conch.mobile) is open on iPhone 16 Pro\n") {
		t.Errorf("out: %q", s)
	}

	flutter := t.TempDir()
	os.WriteFile(filepath.Join(flutter, "pubspec.yaml"), []byte("name: app"), 0o644)
	for _, c := range []struct {
		args []string
		err  string
	}{
		{[]string{"run", "a", "b"}, "usage"},
		{[]string{"run", filepath.Join(dir, "missing")}, "not a folder"},
		{[]string{"run", t.TempDir()}, "holds no mobile app conch knows"},
		{[]string{"run", flutter}, "holds a Flutter app; its own flutter run runs it"},
		{[]string{"run", "-scheme", "Nope", dir}, `no scheme "Nope"`},
		{[]string{"run", "-device", "Galaxy", dir}, `no device "Galaxy"`},
	} {
		if err := runPreview(c.args); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%q: %v, want %q", c.args, err, c.err)
		}
	}
}

// Several folders at once: a line each, its folder named, and a folder
// that is not there is nothing rather than an error for the others.
func TestPreviewDetectMany(t *testing.T) {
	_, out := fakePreview(t, nil)
	ios, flutter, plain := t.TempDir(), t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(ios, "App.xcodeproj"), 0o755)
	os.WriteFile(filepath.Join(flutter, "pubspec.yaml"), []byte("name: app"), 0o644)
	gone := filepath.Join(plain, "gone")
	if err := runPreview([]string{"detect", ios, flutter, plain, gone}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	want := map[string]string{ios: "xcode", flutter: "flutter", plain: "", gone: ""}
	if len(lines) != len(want) {
		t.Fatalf("%d lines: %q", len(lines), out.String())
	}
	for _, l := range lines {
		var p struct{ Dir, Kind string }
		if err := json.Unmarshal([]byte(l), &p); err != nil {
			t.Fatal(err)
		}
		if k, ok := want[p.Dir]; !ok || k != p.Kind {
			t.Errorf("%s: %q", p.Dir, p.Kind)
		}
	}
}

func TestPreviewShutdownAndDelete(t *testing.T) {
	sims := `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-2":[
 {"udid":"A1","name":"iPhone 16 Pro","state":"Booted","isAvailable":true},
 {"udid":"A2","name":"iPhone SE","state":"Shutdown","isAvailable":true},
 {"udid":"A3","name":"iPhone 16","state":"Booted","isAvailable":true}],
 "com.apple.CoreSimulator.SimRuntime.iOS-17-0":[
 {"udid":"B1","name":"iPhone 16 Pro","state":"Shutdown","isAvailable":true}]}}`
	ran, out := fakePreview(t, map[string]string{"xcrun simctl list devices available -j": sims})
	if err := runPreview([]string{"shutdown", "A1"}); err != nil {
		t.Fatal(err)
	}
	if last := (*ran)[len(*ran)-1]; last != "xcrun simctl shutdown A1" {
		t.Errorf("shutdown ran %q", last)
	}
	if !strings.Contains(out.String(), "iPhone 16 Pro is shut down") {
		t.Errorf("said %q", out.String())
	}
	// Every running one, and only those.
	*ran = nil
	if err := runPreview([]string{"shutdown", "-all"}); err != nil {
		t.Fatal(err)
	}
	var stopped []string
	for _, l := range *ran {
		if strings.Contains(l, " shutdown ") {
			stopped = append(stopped, l)
		}
	}
	if want := []string{"xcrun simctl shutdown A1", "xcrun simctl shutdown A3"}; strings.Join(stopped, ",") != strings.Join(want, ",") {
		t.Errorf("shutdown -all ran %q", stopped)
	}
	*ran = nil
	out.Reset()
	if err := runPreview([]string{"delete", "A2"}); err != nil {
		t.Fatal(err)
	}
	if last := (*ran)[len(*ran)-1]; last != "xcrun simctl delete A2" || !strings.Contains(out.String(), "iPhone SE is deleted") {
		t.Errorf("delete ran %q, said %q", last, out.String())
	}
	// A name two simulators share is not enough to delete one: that
	// cannot be undone.
	*ran = nil
	if err := runPreview([]string{"delete", "iPhone 16 Pro"}); err == nil || !strings.Contains(err.Error(), "A1") || !strings.Contains(err.Error(), "B1") {
		t.Errorf("ambiguous delete: %v", err)
	}
	for _, l := range *ran {
		if strings.Contains(l, " delete ") {
			t.Errorf("deleted on an ambiguous name: %q", l)
		}
	}
	for _, c := range []struct {
		args []string
		err  string
	}{
		{[]string{"shutdown"}, "usage"},
		{[]string{"shutdown", "-all", "A1"}, "usage"},
		{[]string{"delete"}, "usage"},
		{[]string{"delete", "A1", "A2"}, "usage"},
		{[]string{"delete", "Galaxy"}, `no device "Galaxy"`},
		{[]string{"shutdown", "Galaxy"}, `no device "Galaxy"`},
	} {
		if err := runPreview(c.args); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%q: %v, want %q", c.args, err, c.err)
		}
	}
}

func TestPreviewKeyboard(t *testing.T) {
	ran, _ := fakePreview(t, map[string]string{"xcrun simctl list devices available -j": previewSims})
	for arg, value := range map[string]string{"show": "false", "hide": "true"} {
		*ran = nil
		if err := runPreview([]string{"keyboard", "-device", "A1", arg}); err != nil {
			t.Fatal(arg, err)
		}
		if want := "AutomaticMinimizationEnabled -bool " + value; !strings.Contains(strings.Join(*ran, "\n"), want) {
			t.Errorf("%s ran %q", arg, *ran)
		}
	}
	for _, c := range []struct {
		args []string
		err  string
	}{
		{[]string{"keyboard"}, "usage"},
		{[]string{"keyboard", "maybe"}, "usage"},
		{[]string{"keyboard", "show", "hide"}, "usage"},
		{[]string{"keyboard", "-device", "Galaxy", "show"}, `no device "Galaxy"`},
		{[]string{"keyboard", "-device", "A2", "show"}, "conch preview boot"},
	} {
		if err := runPreview(c.args); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%q: %v, want %q", c.args, err, c.err)
		}
	}
}

// Run's status file: each step as it begins, and how it ended, which is
// what Conch.app's status bar says while the build is in a terminal.
func TestPreviewRunStatus(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Conch.xcodeproj"), 0o755)
	proj := filepath.Join(dir, "Conch.xcodeproj")
	args := "-project " + proj + " -scheme Conch -configuration Debug -destination id=A1"
	build := "xcrun xcodebuild " + args + " build"
	answers := map[string]string{
		"xcrun simctl list devices available -j":        previewSims,
		"xcrun xcodebuild -list -json -project " + proj: `{"project":{"name":"Conch","schemes":["Conch"]}}`,
		build: "** BUILD SUCCEEDED **\n",
		"xcrun xcodebuild -showBuildSettings -json " + args: `[{"target":"Conch","buildSettings":{"WRAPPER_EXTENSION":"app","TARGET_BUILD_DIR":"/dd","WRAPPER_NAME":"Conch.app","PRODUCT_BUNDLE_IDENTIFIER":"dev.conch.mobile"}}]`,
	}
	fakePreview(t, answers)
	status := filepath.Join(t.TempDir(), "run.json")
	read := func() previewRunStatus {
		t.Helper()
		b, err := os.ReadFile(status)
		if err != nil {
			t.Fatal(err)
		}
		var s previewRunStatus
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatalf("%q: %v", b, err)
		}
		return s
	}
	var during previewRunStatus
	tools := previewTools
	previewTools = func() *preview.Tools {
		tl := tools()
		pipe := tl.Pipe
		tl.Pipe = func(ctx context.Context, w io.Writer, name string, args ...string) error {
			during = read()
			return pipe(ctx, w, name, args...)
		}
		return tl
	}

	if err := runPreview([]string{"run", "-status", status, dir}); err != nil {
		t.Fatal(err)
	}
	me := os.Getpid()
	if want := (previewRunStatus{PID: me, Step: preview.StepBuilding, Device: "iPhone 16 Pro"}); during != want {
		t.Errorf("while building: %+v", during)
	}
	if want, got := (previewRunStatus{PID: me, Step: previewRunOpen, Device: "iPhone 16 Pro"}), read(); got != want {
		t.Errorf("at the end: %+v", got)
	}

	answers[build] = "ERR exit status 65"
	if err := runPreview([]string{"run", "-status", status, dir}); err == nil {
		t.Fatal("a failed build passed")
	}
	if s := read(); s.Step != previewRunFailed || s.Failed != preview.StepBuilding || !strings.Contains(s.Error, "exit status 65") {
		t.Errorf("failed build: %+v", s)
	}

	// Failed before any step: still said, or the app waits on it.
	for _, c := range []struct {
		args []string
		err  string
	}{
		{[]string{"run", "-status", status, filepath.Join(dir, "gone")}, "not a folder"},
		{[]string{"run", "-status", status, "-device", "Galaxy", dir}, `no device "Galaxy"`},
		{[]string{"run", "-status", status, "a", "b"}, "usage"},
	} {
		if err := runPreview(c.args); err == nil {
			t.Fatalf("%q passed", c.args)
		}
		if s := read(); s.Step != previewRunFailed || s.Failed != "" || !strings.Contains(s.Error, c.err) {
			t.Errorf("%q: %+v", c.args, s)
		}
	}

	// A status nobody can write never stops the run.
	answers[build] = "ok"
	if err := runPreview([]string{"run", "-status", filepath.Join(dir, "no", "such", "run.json"), dir}); err != nil {
		t.Errorf("unwritable status: %v", err)
	}
}
