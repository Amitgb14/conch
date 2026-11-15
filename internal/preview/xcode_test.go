package preview

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// xcodeDir is a folder holding the named entries, made as folders, as
// .xcodeproj and .xcworkspace are.
func xcodeDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

var iphone = Device{ID: "U1", Name: "iPhone 17 Pro", Platform: "ios", Kind: "simulator", State: "booted"}

// What xcodebuild says of a project whose package brings a scheme of its
// own, as the real one does: the app's scheme is the project's name.
const listJSON = `{"project":{"configurations":["Debug","Release"],"name":"Conch","schemes":["Conch","ConchKit"],"targets":["Conch"]}}`

const settingsJSON = `[
 {"action":"build","target":"ConchKit","buildSettings":{"WRAPPER_EXTENSION":"","TARGET_BUILD_DIR":"/dd/Debug-iphonesimulator","PRODUCT_BUNDLE_IDENTIFIER":"dev.conch.kit"}},
 {"action":"build","target":"Conch","buildSettings":{"WRAPPER_EXTENSION":"app","TARGET_BUILD_DIR":"/dd/Debug-iphonesimulator","WRAPPER_NAME":"Conch.app","PRODUCT_BUNDLE_IDENTIFIER":"dev.conch.mobile"}}
]`

func xcodeAnswers(container, flag string) map[string]string {
	args := flag + " " + container + " -scheme Conch -configuration Debug -destination id=U1"
	return map[string]string{
		"xcrun xcodebuild -list -json " + flag + " " + container: listJSON,
		"xcrun xcodebuild " + args + " build":                    "Compiling Conch\n** BUILD SUCCEEDED **\n",
		"xcrun xcodebuild -showBuildSettings -json " + args:      settingsJSON,
	}
}

func TestXcodeRunBuildsInstallsAndOpens(t *testing.T) {
	dir := xcodeDir(t, "Conch.xcodeproj", "build")
	proj := filepath.Join(dir, "Conch.xcodeproj")
	f := &fake{answers: xcodeAnswers(proj, "-project")}
	var out bytes.Buffer
	app, err := f.tools("xcrun", "", "", "").XcodeRun(context.Background(), dir, iphone, "", &out)
	if err != nil {
		t.Fatal(err)
	}
	want := XcodeApp{Container: proj, Scheme: "Conch", Path: "/dd/Debug-iphonesimulator/Conch.app", BundleID: "dev.conch.mobile"}
	if app != want {
		t.Fatalf("%+v", app)
	}
	args := "-project " + proj + " -scheme Conch -configuration Debug -destination id=U1"
	ran := []string{
		"xcrun xcodebuild -list -json -project " + proj,
		"xcrun xcodebuild " + args + " build",
		"xcrun xcodebuild -showBuildSettings -json " + args,
		"xcrun simctl install U1 /dd/Debug-iphonesimulator/Conch.app",
		// closed first, so a second Run shows the new build
		"xcrun simctl launch --terminate-running-process U1 dev.conch.mobile",
	}
	if !reflect.DeepEqual(f.ran, ran) {
		t.Fatalf("ran:\n%s", strings.Join(f.ran, "\n"))
	}
	// The build is shown as it goes, after saying what is built for what.
	if !strings.Contains(out.String(), "building Conch for iPhone 17 Pro") || !strings.Contains(out.String(), "BUILD SUCCEEDED") {
		t.Fatalf("out: %q", out.String())
	}
}

// A workspace is what is built when there is one (CocoaPods puts the
// project and its pods in it), and a shut simulator is started first.
func TestXcodeRunWorkspaceAndBoots(t *testing.T) {
	dir := xcodeDir(t, "App.xcodeproj", "App.xcworkspace")
	ws := filepath.Join(dir, "App.xcworkspace")
	ans := xcodeAnswers(ws, "-workspace")
	ans["xcrun xcodebuild -list -json -workspace "+ws] = `{"workspace":{"name":"App","schemes":["Pods-App","App"]}}`
	for k, v := range ans {
		ans[strings.ReplaceAll(k, "-scheme Conch", "-scheme App")] = v
	}
	f := &fake{answers: ans}
	shut := iphone
	shut.State = "shutdown"
	app, err := f.tools("xcrun", "", "", "").XcodeRun(context.Background(), dir, shut, "", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if app.Scheme != "App" || app.Container != ws {
		t.Fatalf("%+v", app)
	}
	if f.ran[1] != "xcrun simctl boot U1" {
		t.Fatalf("not booted before the build: %v", f.ran)
	}
}

func TestXcodeRunRefuses(t *testing.T) {
	ctx := context.Background()
	f := &fake{}
	tl := f.tools("xcrun", "", "", "")
	cases := []struct {
		name, want string
		dir        string
		dev        Device
		xcrun      string
	}{
		{"android", "is not an iOS simulator", xcodeDir(t, "A.xcodeproj"), Device{ID: "emulator-5554", Name: "Pixel 9", Platform: "android", Kind: "emulator", State: "booted"}, "xcrun"},
		{"a phone", "is not an iOS simulator", xcodeDir(t, "A.xcodeproj"), Device{ID: "X", Name: "iPhone", Platform: "ios", Kind: "device", State: "booted"}, "xcrun"},
		{"no xcrun", "install Xcode", xcodeDir(t, "A.xcodeproj"), iphone, ""},
		{"empty folder", "has no .xcodeproj or .xcworkspace", xcodeDir(t), iphone, "xcrun"},
		{"two projects", "has A.xcodeproj and B.xcodeproj", xcodeDir(t, "B.xcodeproj", "A.xcodeproj"), iphone, "xcrun"},
	}
	for _, c := range cases {
		tl.Xcrun = c.xcrun
		_, err := tl.XcodeRun(ctx, c.dir, c.dev, "", &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if len(f.ran) > 0 {
		t.Fatalf("ran: %v", f.ran)
	}
}

// A build that fails stops there: nothing is installed, and the person
// is pointed at the errors the build printed.
func TestXcodeRunBuildFails(t *testing.T) {
	dir := xcodeDir(t, "Conch.xcodeproj")
	proj := filepath.Join(dir, "Conch.xcodeproj")
	f := &fake{answers: xcodeAnswers(proj, "-project"), fails: map[string]string{
		"xcrun xcodebuild -project " + proj + " -scheme Conch -configuration Debug -destination id=U1 build": "exit status 65",
	}}
	_, err := f.tools("xcrun", "", "", "").XcodeRun(context.Background(), dir, iphone, "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "errors are above") {
		t.Fatal(err)
	}
	for _, r := range f.ran {
		if strings.Contains(r, "simctl install") || strings.Contains(r, "simctl launch") {
			t.Fatalf("went on after a failed build: %v", f.ran)
		}
	}
}

func TestPickScheme(t *testing.T) {
	cases := []struct {
		schemes         []string
		name, want, got string
		err             string
	}{
		{[]string{"Conch", "ConchKit"}, "Conch", "", "Conch", ""},
		{[]string{"ConchKit", "Conch"}, "Conch", "ConchKit", "ConchKit", ""},
		{[]string{"Only"}, "Other", "", "Only", ""},
		{[]string{"A", "B"}, "Other", "", "", "which scheme? A, B"},
		{[]string{"A", "B"}, "A", "C", "", `no scheme "C": there are A, B`},
		{nil, "Conch", "", "", "has no schemes"},
	}
	for _, c := range cases {
		got, err := pickScheme(c.schemes, c.name, c.want)
		if got != c.got || (c.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%v %q %q: %q %v", c.schemes, c.name, c.want, got, err)
		}
	}
}

func TestBuiltApp(t *testing.T) {
	if p, id, err := builtApp([]byte(settingsJSON)); err != nil || p != "/dd/Debug-iphonesimulator/Conch.app" || id != "dev.conch.mobile" {
		t.Fatal(p, id, err)
	}
	for in, want := range map[string]string{
		`[{"target":"Kit","buildSettings":{"WRAPPER_EXTENSION":"framework"}}]`: "builds no app",
		`[]`: "builds no app",
		`[{"target":"App","buildSettings":{"WRAPPER_EXTENSION":"app","TARGET_BUILD_DIR":"/d","WRAPPER_NAME":"App.app"}}]`: "no bundle ID",
		`not json`: "-showBuildSettings",
	} {
		if _, _, err := builtApp([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", in, err)
		}
	}
}
