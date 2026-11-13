package preview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fake answers each command line from a table and records what ran.
type fake struct {
	answers map[string]string
	fails   map[string]string
	ran     []string
	started []string
}

func (f *fake) tools(xcrun, adb, emulator, idb string) *Tools {
	return &Tools{
		Xcrun: xcrun, Adb: adb, Emulator: emulator, Idb: idb,
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			line := strings.Join(append([]string{name}, args...), " ")
			f.ran = append(f.ran, line)
			if msg, ok := f.fails[line]; ok {
				return nil, errors.New(msg)
			}
			return []byte(f.answers[line]), nil
		},
		Start: func(name string, args ...string) error {
			f.started = append(f.started, strings.Join(append([]string{name}, args...), " "))
			return nil
		},
	}
}

const simJSON = `{"devices":{
 "com.apple.CoreSimulator.SimRuntime.iOS-9-3":[{"udid":"OLD","name":"iPhone 6s","state":"Shutdown","isAvailable":true}],
 "com.apple.CoreSimulator.SimRuntime.iOS-18-2":[
   {"udid":"A1","name":"iPhone 16 Pro","state":"Booted","isAvailable":true},
   {"udid":"A2","name":"iPhone SE (3rd generation)","state":"Shutdown","isAvailable":true},
   {"udid":"GONE","name":"iPhone 15","state":"Shutdown","isAvailable":false}],
 "com.apple.CoreSimulator.SimRuntime.iOS-17-0-1":[{"udid":"B1","name":"iPhone 16 Pro","state":"Shutdown"}],
 "com.apple.CoreSimulator.SimRuntime.watchOS-11-0":[{"udid":"W1","name":"Apple Watch","state":"Booted","isAvailable":true}]
}}`

const adbList = `* daemon not running; starting now at tcp:5037
* daemon started successfully
List of devices attached
emulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:1
R5CT1234               unauthorized usb:1-1 transport_id:2
0A1B2C                 device usb:1-2 product:husky model:Pixel_8_Pro device:husky transport_id:3

`

func TestDevicesBothPlatforms(t *testing.T) {
	f := &fake{answers: map[string]string{
		"xcrun simctl list devices available -j": simJSON,
		"adb devices -l":                         adbList,
		"adb -s emulator-5554 emu avd name":      "Pixel_9_API_35\nOK\n",
		"emulator -list-avds":                    "INFO    | Storing crashdata in: /tmp/x\nPixel_9_API_35\nPixel_Tablet\n",
	}}
	devs, notes, err := f.tools("xcrun", "adb", "emulator", "").Devices(context.Background())
	if err != nil || len(notes) > 0 {
		t.Fatal(err, notes)
	}
	var got []string
	for _, d := range devs {
		got = append(got, d.ID+"|"+d.Name+"|"+d.Platform+"|"+d.Kind+"|"+d.OS+"|"+d.State)
	}
	want := []string{
		// booted first, iOS before Android
		"A1|iPhone 16 Pro|ios|simulator|iOS 18.2|booted",
		"emulator-5554|Pixel 9 API 35|android|emulator||booted",
		"0A1B2C|Pixel 8 Pro|android|device||booted",
		// then by runtime, newest first: 18.2, 17.0.1, 9.3 (not by spelling)
		"A2|iPhone SE (3rd generation)|ios|simulator|iOS 18.2|shutdown",
		"B1|iPhone 16 Pro|ios|simulator|iOS 17.0.1|shutdown",
		"OLD|iPhone 6s|ios|simulator|iOS 9.3|shutdown",
		"R5CT1234|R5CT1234|android|device||unauthorized",
		// the running AVD is not listed twice; the emulator's log line is no AVD
		"avd:Pixel_Tablet|Pixel Tablet|android|emulator||shutdown",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("devices:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestDevicesOnePlatformFailing(t *testing.T) {
	f := &fake{
		answers: map[string]string{"adb devices -l": "List of devices attached\nemulator-5556 device\n"},
		fails:   map[string]string{"xcrun simctl list devices available -j": `xcrun: error: unable to find utility "simctl"`},
	}
	devs, notes, err := f.tools("xcrun", "adb", "", "").Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The emulator whose name the emulator would not give keeps its serial.
	if len(devs) != 1 || devs[0].ID != "emulator-5556" || devs[0].Name != "emulator-5556" {
		t.Errorf("devices = %+v", devs)
	}
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "iOS: ") || !strings.Contains(notes[0], "simctl") {
		t.Errorf("notes = %q", notes)
	}
}

func TestDevicesBadJSONAndNoTools(t *testing.T) {
	f := &fake{answers: map[string]string{"xcrun simctl list devices available -j": "{not json"}}
	_, notes, err := f.tools("xcrun", "", "", "").Devices(context.Background())
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "not JSON") {
		t.Errorf("err %v notes %q", err, notes)
	}
	if _, _, err := (&Tools{}).Devices(context.Background()); !errors.Is(err, ErrNoTools) {
		t.Errorf("no tools: %v", err)
	}
	// Empty answers are an empty list, not an error.
	devs, notes, err := (&fake{}).tools("xcrun", "adb", "emulator", "").Devices(context.Background())
	if len(devs) != 0 || err != nil {
		t.Errorf("empty: %v %v %v", devs, notes, err)
	}
}

func TestPick(t *testing.T) {
	devs := []Device{
		{ID: "A1", Name: "iPhone 16 Pro", State: "booted"},
		{ID: "B1", Name: "iPhone 16 Pro", State: "shutdown"},
		{ID: "A2", Name: "iPhone SE", State: "shutdown"},
	}
	cases := []struct{ want, id, err string }{
		{"", "A1", ""},
		{"B1", "B1", ""},
		{"iphone 16 pro", "A1", ""}, // the booted one of two
		{"iPhone SE", "A2", ""},
		{"Galaxy", "", `no device "Galaxy"`},
	}
	for _, c := range cases {
		d, err := Pick(devs, c.want)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("Pick(%q) err = %v", c.want, err)
			}
			continue
		}
		if err != nil || d.ID != c.id {
			t.Errorf("Pick(%q) = %s, %v; want %s", c.want, d.ID, err, c.id)
		}
	}
	if _, err := Pick(devs[1:], ""); err == nil || !strings.Contains(err.Error(), `conch preview boot "iPhone 16 Pro"`) {
		t.Errorf("none booted: %v", err)
	}
	if _, err := Pick(nil, ""); err == nil || !strings.Contains(err.Error(), "no simulator or emulator") {
		t.Errorf("none at all: %v", err)
	}
}

func TestBoot(t *testing.T) {
	f := &fake{fails: map[string]string{"xcrun simctl boot B": "simctl: Unable to boot device in current state: Booted"}}
	tl := f.tools("xcrun", "adb", "emulator", "")
	ctx := context.Background()
	if err := tl.Boot(ctx, Device{ID: "A", Platform: "ios", State: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	// Booted meanwhile: not an error.
	if err := tl.Boot(ctx, Device{ID: "B", Platform: "ios", State: "shutdown"}); err != nil {
		t.Errorf("already booted: %v", err)
	}
	if err := tl.Boot(ctx, Device{ID: "avd:Pixel_9", Platform: "android", State: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	if err := tl.Boot(ctx, Device{ID: "X", Platform: "ios", State: "booted"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"xcrun simctl boot A", "xcrun simctl boot B"}; !reflect.DeepEqual(f.ran, want) {
		t.Errorf("ran %q", f.ran)
	}
	if want := []string{"emulator -avd Pixel_9"}; !reflect.DeepEqual(f.started, want) {
		t.Errorf("started %q", f.started)
	}
	if err := tl.Boot(ctx, Device{ID: "R5", Name: "Phone", Platform: "android", State: "unauthorized"}); err == nil || !strings.Contains(err.Error(), "on the phone") {
		t.Errorf("a real phone: %v", err)
	}
	if err := (&fake{}).tools("", "adb", "", "").Boot(ctx, Device{ID: "avd:P", Platform: "android", State: "shutdown"}); err == nil || !strings.Contains(err.Error(), "emulator not found") {
		t.Errorf("no emulator: %v", err)
	}
	if err := (&fake{}).tools("", "adb", "", "").Boot(ctx, Device{ID: "A", Platform: "ios", State: "shutdown"}); err == nil || !strings.Contains(err.Error(), "Xcode") {
		t.Errorf("no xcrun: %v", err)
	}
}

func TestShot(t *testing.T) {
	dir := t.TempDir()
	png := "\x89PNG\r\n\x1a\nrest"
	f := &fake{answers: map[string]string{"adb -s emu exec-out screencap -p": png, "adb -s bad exec-out screencap -p": "error: closed"}}
	tl := f.tools("xcrun", "adb", "", "")
	ctx := context.Background()
	p := filepath.Join(dir, "a.png")
	if err := tl.Shot(ctx, Device{ID: "emu", Platform: "android", State: "booted"}, p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != png {
		t.Errorf("wrote %q", b)
	}
	if _, err := os.Stat(p + ".part"); !os.IsNotExist(err) {
		t.Errorf("left its part file: %v", err)
	}
	if err := tl.Shot(ctx, Device{ID: "bad", Name: "Pixel", Platform: "android", State: "booted"}, p); err == nil || !strings.Contains(err.Error(), "no picture") {
		t.Errorf("not a PNG: %v", err)
	}
	if err := tl.Shot(ctx, Device{ID: "U", Platform: "ios", State: "booted"}, p); err != nil {
		t.Fatal(err)
	}
	if last := f.ran[len(f.ran)-1]; last != "xcrun simctl io U screenshot --type=png "+p {
		t.Errorf("ran %q", last)
	}
	if err := tl.Shot(ctx, Device{ID: "U", Name: "iPhone SE", Platform: "ios", State: "shutdown"}, p); err == nil || !strings.Contains(err.Error(), `conch preview boot "iPhone SE"`) {
		t.Errorf("shut down: %v", err)
	}
}

func TestTapTypeButton(t *testing.T) {
	f := &fake{answers: map[string]string{
		"idb describe --udid U --json": `{"screen_dimensions":{"width":1179,"height":2556,"density":3,"width_points":393,"height_points":852}}`,
	}}
	tl := f.tools("xcrun", "adb", "", "idb")
	ctx := context.Background()
	and := Device{ID: "emu", Platform: "android", State: "booted"}
	ios := Device{ID: "U", Platform: "ios", State: "booted"}
	for _, err := range []error{
		tl.Tap(ctx, and, 540, 1200),
		tl.Tap(ctx, ios, 300, 600), // pixels → points: 100, 200
		tl.Type(ctx, and, `it's 5 & done`),
		tl.Type(ctx, ios, "hello world"),
		tl.Type(ctx, and, ""), // nothing to type runs nothing
		tl.Button(ctx, and, "back"),
		tl.Button(ctx, ios, "home"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"adb -s emu shell input tap 540 1200",
		"idb describe --udid U --json",
		"idb ui tap --udid U 100 200",
		`adb -s emu shell input text it\'s%s5%s\&%sdone`,
		"idb ui text --udid U hello world",
		"adb -s emu shell input keyevent KEYCODE_BACK",
		"idb ui button --udid U HOME",
	}
	if !reflect.DeepEqual(f.ran, want) {
		t.Errorf("ran:\n%s", strings.Join(f.ran, "\n"))
	}
	if err := tl.Button(ctx, ios, "back"); err == nil || !strings.Contains(err.Error(), "no \"back\"") {
		t.Errorf("iPhone back: %v", err)
	}
	if err := tl.Button(ctx, and, "power"); err == nil {
		t.Error("power button pressed")
	}
	if err := tl.Tap(ctx, and, -1, 4); err == nil {
		t.Error("tapped off the screen")
	}
	noIdb := (&fake{}).tools("xcrun", "", "", "")
	for _, err := range []error{noIdb.Tap(ctx, ios, 1, 1), noIdb.Type(ctx, ios, "x"), noIdb.Button(ctx, ios, "home")} {
		if err == nil || !strings.Contains(err.Error(), "idb") {
			t.Errorf("no idb: %v", err)
		}
	}
	if err := tl.Tap(ctx, Device{ID: "emu", Name: "Pixel", Platform: "android", State: "offline"}, 1, 1); err == nil {
		t.Error("tapped an offline device")
	}
	// idb that says nothing of the screen
	f2 := &fake{answers: map[string]string{"idb describe --udid U --json": `{}`}}
	if err := f2.tools("", "", "", "idb").Tap(ctx, ios, 1, 1); err == nil || !strings.Contains(err.Error(), "screen's size") {
		t.Errorf("no size: %v", err)
	}
	// density alone is enough
	f3 := &fake{answers: map[string]string{"idb describe --udid U --json": `{"screen_dimensions":{"density":2}}`}}
	if err := f3.tools("", "", "", "idb").Tap(ctx, ios, 10, 20); err != nil || f3.ran[1] != "idb ui tap --udid U 5 10" {
		t.Errorf("density: %v %q", err, f3.ran)
	}
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestFind runs real (fake) programs: adb from the SDK in the home folder,
// not on PATH, as Android Studio leaves it; a failure says the program's
// own last line; an emulator is started and left running.
func TestFind(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	writeExec(t, filepath.Join(bin, "xcrun"), `echo "noise" >&2; echo "xcrun: error: unable to find utility \"simctl\"" >&2; exit 72`)
	mark := filepath.Join(t.TempDir(), "started")
	writeExec(t, filepath.Join(home, "Library/Android/sdk/platform-tools/adb"), `echo "List of devices attached"`)
	writeExec(t, filepath.Join(home, "Library/Android/sdk/emulator/emulator"), `echo "$@" > `+mark)
	os.WriteFile(filepath.Join(bin, "idb"), []byte("not executable"), 0o644)
	env := []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home}
	tl := Find(env)
	if tl.Xcrun != filepath.Join(bin, "xcrun") || !strings.HasSuffix(tl.Adb, "platform-tools/adb") || !strings.HasSuffix(tl.Emulator, "emulator/emulator") {
		t.Fatalf("found %+v", tl)
	}
	if tl.Idb != "" {
		t.Errorf("a file that is not executable was taken for idb: %q", tl.Idb)
	}
	_, notes, err := tl.Devices(context.Background())
	if err != nil || len(notes) != 1 || notes[0] != `iOS: xcrun: xcrun: error: unable to find utility "simctl"` {
		t.Errorf("err %v notes %q", err, notes)
	}
	if err := tl.Start(tl.Emulator, "-avd", "P"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, _ := os.ReadFile(mark); strings.TrimSpace(string(b)) == "-avd P" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the emulator never ran")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// ANDROID_HOME is looked in before the home folder's SDK.
	sdk := t.TempDir()
	writeExec(t, filepath.Join(sdk, "platform-tools/adb"), "true")
	if got := Find(append(env, "ANDROID_HOME="+sdk)).Adb; got != filepath.Join(sdk, "platform-tools/adb") {
		t.Errorf("ANDROID_HOME's adb: %q", got)
	}
	if got := Find([]string{"PATH=", "HOME="}); got.Adb != "" || got.Xcrun != "" {
		t.Errorf("nothing anywhere: %+v", got)
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		files map[string]string
		kind  string
	}{
		{map[string]string{"package.json": `{"dependencies":{"expo":"~52.0.0","react-native":"0.76"}}`}, "expo"},
		{map[string]string{"package.json": `{"devDependencies":{"react-native":"0.76"}}`}, "react-native"},
		{map[string]string{"package.json": `{"dependencies":{"react":"19"}}`}, ""},
		{map[string]string{"package.json": `{bad json`, "pubspec.yaml": "name: x"}, "flutter"},
		{map[string]string{"Fieldnotes.xcodeproj/project.pbxproj": ""}, "xcode"},
		{map[string]string{"settings.gradle.kts": "", "app/build.gradle.kts": ""}, "android"},
		{map[string]string{"build.gradle": `plugins { id 'com.android.application' }`}, "android"},
		{map[string]string{"build.gradle": `plugins { id 'java' }`}, ""},
		{map[string]string{"deep/package.json": `{"dependencies":{"expo":"1"}}`}, ""},
		{nil, ""},
	}
	for i, c := range cases {
		dir := t.TempDir()
		for n, body := range c.files {
			p := filepath.Join(dir, n)
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(body), 0o644)
		}
		p := Detect(dir)
		if p.Kind != c.kind {
			t.Errorf("%d %v: kind %q, want %q", i, c.files, p.Kind, c.kind)
		}
		if p.Kind != "" && (p.Label == "" || len(p.Platforms) == 0) {
			t.Errorf("%d: %+v", i, p)
		}
	}
	if p := Detect(filepath.Join(t.TempDir(), "missing")); p.Kind != "" {
		t.Errorf("a missing folder: %+v", p)
	}
}
