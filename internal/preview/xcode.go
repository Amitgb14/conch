package preview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// An Xcode project has no dev server to start: its app is built, put on
// the simulator and opened, which is what the preview's ▶ Run does for
// one (`conch preview run`). Everything is asked of Xcode itself — the
// schemes, where the build lands, the bundle ID — since a project.yml or
// an Info.plist can say one thing and the build settings another.

// XcodeApp is what a build of an Xcode project made, and how it is opened.
type XcodeApp struct {
	Container string // the .xcworkspace or .xcodeproj built
	Scheme    string
	Path      string // the built .app
	BundleID  string
}

// xcodeContainer is the workspace in dir, else the project: a workspace
// (CocoaPods, several projects) is what has to be built when there is
// one. Two of the same kind is a choice conch does not make.
func xcodeContainer(dir string) (flag, path string, err error) {
	for _, k := range []struct{ glob, flag string }{{"*.xcworkspace", "-workspace"}, {"*.xcodeproj", "-project"}} {
		m, _ := filepath.Glob(filepath.Join(dir, k.glob))
		sort.Strings(m)
		switch len(m) {
		case 0:
			continue
		case 1:
			return k.flag, m[0], nil
		}
		names := make([]string, len(m))
		for i, p := range m {
			names[i] = filepath.Base(p)
		}
		return "", "", fmt.Errorf("%s has %s: open one of them in Xcode, or run conch preview run in a folder with one", dir, strings.Join(names, " and "))
	}
	return "", "", fmt.Errorf("%s has no .xcodeproj or .xcworkspace", dir)
}

// pickScheme is the one asked for, else the one named as the project is
// (a Swift package beside the app brings a scheme of its own), else the
// only one.
func pickScheme(schemes []string, name, want string) (string, error) {
	if want != "" {
		for _, s := range schemes {
			if s == want {
				return s, nil
			}
		}
		return "", fmt.Errorf("no scheme %q: there are %s", want, strings.Join(schemes, ", "))
	}
	for _, s := range schemes {
		if s == name {
			return s, nil
		}
	}
	switch len(schemes) {
	case 0:
		return "", fmt.Errorf("%s has no schemes: open it in Xcode once so it makes them", name)
	case 1:
		return schemes[0], nil
	}
	return "", fmt.Errorf("which scheme? %s: conch preview run -scheme NAME", strings.Join(schemes, ", "))
}

// XcodeRun builds the Xcode project in dir for the simulator d, installs
// it and opens it, the build printed to w as it goes. A simulator that
// is shut down is started first: the build is for it.
func (t *Tools) XcodeRun(ctx context.Context, dir string, d Device, scheme string, w io.Writer) (XcodeApp, error) {
	var app XcodeApp
	if d.Platform != "ios" || d.Kind != "simulator" {
		return app, fmt.Errorf("%s is not an iOS simulator: conch preview run puts an Xcode app on a simulator", d.Name)
	}
	if t.Xcrun == "" {
		return app, errors.New("xcrun not found: install Xcode")
	}
	flag, container, err := xcodeContainer(dir)
	if err != nil {
		return app, err
	}
	app.Container = container
	out, err := t.Run(ctx, t.Xcrun, "xcodebuild", "-list", "-json", flag, container)
	if err != nil {
		return app, err
	}
	var list struct {
		Project, Workspace *struct {
			Name    string   `json:"name"`
			Schemes []string `json:"schemes"`
		}
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return app, fmt.Errorf("xcodebuild -list: %w", err)
	}
	info := list.Project
	if info == nil {
		info = list.Workspace
	}
	if info == nil {
		return app, fmt.Errorf("xcodebuild -list said nothing of %s", filepath.Base(container))
	}
	name := info.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(container), filepath.Ext(container))
	}
	if app.Scheme, err = pickScheme(info.Schemes, name, scheme); err != nil {
		return app, err
	}
	if err := t.Boot(ctx, d); err != nil {
		return app, err
	}
	args := []string{flag, container, "-scheme", app.Scheme, "-configuration", "Debug", "-destination", "id=" + d.ID}
	fmt.Fprintf(w, "conch: building %s for %s\n", app.Scheme, d.Name)
	if err := t.Pipe(ctx, w, t.Xcrun, append(append([]string{"xcodebuild"}, args...), "build")...); err != nil {
		return app, fmt.Errorf("the build failed (%w); its errors are above", err)
	}
	out, err = t.Run(ctx, t.Xcrun, append([]string{"xcodebuild", "-showBuildSettings", "-json"}, args...)...)
	if err != nil {
		return app, err
	}
	if app.Path, app.BundleID, err = builtApp(out); err != nil {
		return app, fmt.Errorf("%s: %w", app.Scheme, err)
	}
	if _, err := t.Run(ctx, t.Xcrun, "simctl", "install", d.ID, app.Path); err != nil {
		return app, err
	}
	// By ID: d may still say shutdown, though the build has booted it.
	if err := t.keyboard(ctx, d.ID, true); err != nil {
		fmt.Fprintf(w, "conch: the keyboard may stay hidden: %v\n", err)
	}
	// The app as it was is closed, so a second Run shows the new build.
	if _, err := t.Run(ctx, t.Xcrun, "simctl", "launch", "--terminate-running-process", d.ID, app.BundleID); err != nil {
		return app, err
	}
	return app, nil
}

// builtApp reads where the scheme's app was built and its bundle ID from
// xcodebuild -showBuildSettings -json: the one target that makes an .app,
// of the several a scheme can build (tests, extensions, frameworks).
func builtApp(settings []byte) (path, bundleID string, err error) {
	var targets []struct {
		Target   string            `json:"target"`
		Settings map[string]string `json:"buildSettings"`
	}
	if err := json.Unmarshal(settings, &targets); err != nil {
		return "", "", fmt.Errorf("xcodebuild -showBuildSettings: %w", err)
	}
	for _, t := range targets {
		s := t.Settings
		if s["WRAPPER_EXTENSION"] != "app" || s["TARGET_BUILD_DIR"] == "" || s["WRAPPER_NAME"] == "" {
			continue
		}
		if s["PRODUCT_BUNDLE_IDENTIFIER"] == "" {
			return "", "", fmt.Errorf("the app %s has no bundle ID", t.Target)
		}
		return filepath.Join(s["TARGET_BUILD_DIR"], s["WRAPPER_NAME"]), s["PRODUCT_BUNDLE_IDENTIFIER"], nil
	}
	return "", "", errors.New("it builds no app to put on a simulator")
}
