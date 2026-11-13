// Package preview reaches the phones a mobile developer runs their app on
// while an agent works on it: the iOS simulators Xcode installs and the
// Android emulators and devices adb sees. It lists them, starts one, takes
// a screenshot, taps, types and presses a button — the same few things
// whether Conch.app is drawing the phone beside the agent or the agent is
// looking at its own work with `conch preview shot`.
//
// It is the real device, never a picture of a layout: what is shown is
// what the app does, crashes and all. Everything goes through the
// platforms' own tools (xcrun simctl, adb, emulator, and idb for touching
// an iOS simulator, which simctl cannot do), so a machine without them
// says which is missing rather than pretending there is nothing to show.
package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Device is a phone an app can be previewed on.
type Device struct {
	// ID is what every other call takes: a simulator's UDID, an adb
	// serial, or "avd:NAME" for an emulator that is not running yet.
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"` // ios, android
	Kind     string `json:"kind"`     // simulator, emulator, device
	OS       string `json:"os,omitempty"`
	// State is booted, shutdown, or what adb says of a device it cannot
	// use (offline, unauthorized).
	State string `json:"state"`
}

// Booted reports whether the device can be shown and touched now.
func (d Device) Booted() bool { return d.State == "booted" }

// Tools are the programs the platforms ship, found once.
type Tools struct {
	Xcrun    string // xcrun, for simctl
	Adb      string
	Emulator string
	Idb      string
	// Run runs a program to its end and answers its standard output;
	// Start leaves one running (an emulator). Tests replace both.
	Run   func(ctx context.Context, name string, args ...string) ([]byte, error)
	Start func(name string, args ...string) error
}

// Find looks for the tools on PATH and where the Android SDK keeps its
// own (it is seldom on PATH: Android Studio installs it in the home
// folder), given the environment as KEY=VALUE pairs.
func Find(env []string) *Tools {
	get := func(k string) string {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, k+"="); ok {
				return v
			}
		}
		return ""
	}
	path := get("PATH")
	look := func(name string, extra ...string) string {
		for _, dir := range filepath.SplitList(path) {
			if dir == "" {
				continue
			}
			if p := filepath.Join(dir, name); isExec(p) {
				return p
			}
		}
		for _, p := range extra {
			if isExec(p) {
				return p
			}
		}
		return ""
	}
	var sdks []string
	for _, k := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if v := get(k); v != "" {
			sdks = append(sdks, v)
		}
	}
	if home := get("HOME"); home != "" {
		sdks = append(sdks, filepath.Join(home, "Library", "Android", "sdk"), filepath.Join(home, "Android", "Sdk"))
	}
	in := func(sub, name string) []string {
		var out []string
		for _, s := range sdks {
			out = append(out, filepath.Join(s, sub, name))
		}
		return out
	}
	t := &Tools{
		Adb:      look("adb", in("platform-tools", "adb")...),
		Emulator: look("emulator", in("emulator", "emulator")...),
		Idb:      look("idb"),
	}
	// /usr/bin/xcrun is there without Xcode, and only answers that simctl
	// is missing; that is said when it is used, which is when it matters.
	t.Xcrun = look("xcrun")
	t.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return out, fmt.Errorf("%s: %s", filepath.Base(name), lastLine(msg))
			}
			return out, fmt.Errorf("%s: %w", filepath.Base(name), err)
		}
		return out, nil
	}
	t.Start = func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Env = env
		cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
		detach(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	return t
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// ErrNoTools is what Devices says when neither platform's tools are here.
var ErrNoTools = errors.New("no iOS simulator or Android tools found: install Xcode (for xcrun simctl) or Android Studio (for adb and emulator)")

// Devices lists every phone either platform offers, booted ones first.
// A platform whose tool fails is left out and said in notes, so an
// Android emulator still shows on a Mac whose Xcode is broken.
func (t *Tools) Devices(ctx context.Context) (devs []Device, notes []string, err error) {
	if t.Xcrun == "" && t.Adb == "" && t.Emulator == "" {
		return nil, nil, ErrNoTools
	}
	if t.Xcrun != "" {
		d, err := t.simulators(ctx)
		if err != nil {
			notes = append(notes, "iOS: "+err.Error())
		}
		devs = append(devs, d...)
	}
	if t.Adb != "" || t.Emulator != "" {
		d, err := t.android(ctx)
		if err != nil {
			notes = append(notes, "Android: "+err.Error())
		}
		devs = append(devs, d...)
	}
	sort.SliceStable(devs, func(i, j int) bool {
		if devs[i].Booted() != devs[j].Booted() {
			return devs[i].Booted()
		}
		if devs[i].Platform != devs[j].Platform {
			return devs[i].Platform == "ios"
		}
		return false
	})
	return devs, notes, nil
}

type simList struct {
	Devices map[string][]struct {
		UDID        string `json:"udid"`
		Name        string `json:"name"`
		State       string `json:"state"`
		IsAvailable *bool  `json:"isAvailable"`
	} `json:"devices"`
}

var runtimeRe = regexp.MustCompile(`SimRuntime\.iOS-(\d+)-(\d+)(?:-(\d+))?$`)

// simulators reads simctl's list: iPhones and iPads, which is what an app
// is previewed on — watches and TVs are left out.
func (t *Tools) simulators(ctx context.Context) ([]Device, error) {
	out, err := t.Run(ctx, t.Xcrun, "simctl", "list", "devices", "available", "-j")
	if err != nil {
		return nil, err
	}
	var l simList
	if err := json.Unmarshal(out, &l); err != nil {
		return nil, fmt.Errorf("simctl's list was not JSON: %v", err)
	}
	runtimes := make([]string, 0, len(l.Devices))
	for rt := range l.Devices {
		runtimes = append(runtimes, rt)
	}
	// Newest runtime first, by version and not by spelling (iOS-9 < iOS-17).
	sort.Slice(runtimes, func(i, j int) bool { return versionLess(runtimes[j], runtimes[i]) })
	var devs []Device
	for _, rt := range runtimes {
		m := runtimeRe.FindStringSubmatch(rt)
		if m == nil {
			continue
		}
		osName := "iOS " + m[1] + "." + m[2]
		if m[3] != "" {
			osName += "." + m[3]
		}
		for _, d := range l.Devices[rt] {
			if d.IsAvailable != nil && !*d.IsAvailable || d.UDID == "" {
				continue
			}
			state := "shutdown"
			if d.State == "Booted" {
				state = "booted"
			}
			devs = append(devs, Device{ID: d.UDID, Name: d.Name, Platform: "ios", Kind: "simulator", OS: osName, State: state})
		}
	}
	return devs, nil
}

func versionLess(a, b string) bool {
	ma, mb := runtimeRe.FindStringSubmatch(a), runtimeRe.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return a < b
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(ma[i])
		y, _ := strconv.Atoi(mb[i])
		if x != y {
			return x < y
		}
	}
	return false
}

// android lists what adb sees — running emulators and plugged-in phones —
// and every emulator image that is not running, which can be started.
func (t *Tools) android(ctx context.Context) ([]Device, error) {
	var devs []Device
	running := map[string]bool{} // AVD names already in the list
	var errs []string
	if t.Adb != "" {
		out, err := t.Run(ctx, t.Adb, "devices", "-l")
		if err != nil {
			errs = append(errs, err.Error())
		}
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 || f[0] == "List" || strings.HasPrefix(f[0], "*") {
				continue
			}
			serial, st := f[0], f[1]
			d := Device{ID: serial, Platform: "android", Kind: "device", State: st}
			if st == "device" {
				d.State = "booted"
			}
			for _, kv := range f[2:] {
				if v, ok := strings.CutPrefix(kv, "model:"); ok {
					d.Name = strings.ReplaceAll(v, "_", " ")
				}
			}
			if strings.HasPrefix(serial, "emulator-") {
				d.Kind = "emulator"
				// The emulator knows the image it is running; adb's model
				// is the system image's, the same for every one.
				if o, err := t.Run(ctx, t.Adb, "-s", serial, "emu", "avd", "name"); err == nil {
					if n := strings.TrimSpace(strings.SplitN(string(o), "\n", 2)[0]); n != "" && n != "OK" {
						running[n] = true
						d.Name = strings.ReplaceAll(n, "_", " ")
					}
				}
			}
			if d.Name == "" {
				d.Name = serial
			}
			devs = append(devs, d)
		}
	}
	if t.Emulator != "" {
		out, err := t.Run(ctx, t.Emulator, "-list-avds")
		if err != nil {
			errs = append(errs, err.Error())
		}
		for _, line := range strings.Split(string(out), "\n") {
			n := strings.TrimSpace(line)
			// The emulator prints its own warnings to stdout too.
			if n == "" || strings.ContainsAny(n, " :|") || running[n] {
				continue
			}
			devs = append(devs, Device{ID: "avd:" + n, Name: strings.ReplaceAll(n, "_", " "), Platform: "android", Kind: "emulator", State: "shutdown"})
		}
	}
	if len(errs) > 0 {
		return devs, errors.New(strings.Join(errs, "; "))
	}
	return devs, nil
}

// Pick finds the device a command means: by ID or by name (any case),
// else the first booted one. Nothing booted is an error that says how to
// start one, since that is the next thing to do.
func Pick(devs []Device, want string) (Device, error) {
	if want != "" {
		for _, d := range devs {
			if d.ID == want {
				return d, nil
			}
		}
		var found []Device
		for _, d := range devs {
			if strings.EqualFold(d.Name, want) {
				found = append(found, d)
			}
		}
		// One name for several (an iPhone 16 on two iOS versions): the
		// booted one, which is the one somebody is looking at.
		for _, d := range found {
			if d.Booted() {
				return d, nil
			}
		}
		if len(found) > 0 {
			return found[0], nil
		}
		return Device{}, fmt.Errorf("no device %q (conch preview devices lists them)", want)
	}
	for _, d := range devs {
		if d.Booted() {
			return d, nil
		}
	}
	if len(devs) == 0 {
		return Device{}, errors.New("no simulator or emulator found")
	}
	return Device{}, fmt.Errorf("nothing is running: conch preview boot %q starts it", devs[0].Name)
}

func needBooted(d Device) error {
	if !d.Booted() {
		return fmt.Errorf("%s is %s: conch preview boot %q starts it", d.Name, d.State, d.Name)
	}
	return nil
}

// Boot starts a device. A simulator boots in the background, with no
// Simulator window; an emulator is left running as its own program.
func (t *Tools) Boot(ctx context.Context, d Device) error {
	if d.Booted() {
		return nil
	}
	switch d.Platform {
	case "ios":
		if t.Xcrun == "" {
			return errors.New("xcrun not found: install Xcode")
		}
		_, err := t.Run(ctx, t.Xcrun, "simctl", "boot", d.ID)
		if err != nil && strings.Contains(err.Error(), "current state: Booted") {
			return nil
		}
		return err
	case "android":
		name, ok := strings.CutPrefix(d.ID, "avd:")
		if !ok {
			return fmt.Errorf("%s is %s: a phone plugged in is started on the phone", d.Name, d.State)
		}
		if t.Emulator == "" {
			return errors.New("emulator not found: install it with Android Studio's SDK Manager")
		}
		return t.Start(t.Emulator, "-avd", name)
	}
	return fmt.Errorf("unknown platform %q", d.Platform)
}

// Shot writes a PNG of the device's screen to path, at its full pixels.
func (t *Tools) Shot(ctx context.Context, d Device, path string) error {
	if err := needBooted(d); err != nil {
		return err
	}
	switch d.Platform {
	case "ios":
		_, err := t.Run(ctx, t.Xcrun, "simctl", "io", d.ID, "screenshot", "--type=png", path)
		return err
	case "android":
		out, err := t.Run(ctx, t.Adb, "-s", d.ID, "exec-out", "screencap", "-p")
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(out, []byte("\x89PNG")) {
			return fmt.Errorf("adb gave no picture of %s", d.Name)
		}
		return writeFile(path, out)
	}
	return fmt.Errorf("unknown platform %q", d.Platform)
}

// writeFile writes beside and renames, so whoever is reading the last
// picture (the app, many times a second) never reads half of the next.
func writeFile(path string, b []byte) error {
	tmp := path + ".part"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// needIdb says how to get idb: simctl can look at a simulator but not
// touch it.
func (t *Tools) needIdb() error {
	if t.Idb == "" {
		return errors.New("touching an iOS simulator needs idb: brew install facebook/fb/idb-companion && pip3 install fb-idb")
	}
	return nil
}

// Tap touches the screen at x, y in the screenshot's pixels — the
// numbers an agent reads off the picture Shot gave it.
func (t *Tools) Tap(ctx context.Context, d Device, x, y int) error {
	if err := needBooted(d); err != nil {
		return err
	}
	if x < 0 || y < 0 {
		return fmt.Errorf("%d,%d is off the screen", x, y)
	}
	switch d.Platform {
	case "android":
		_, err := t.Run(ctx, t.Adb, "-s", d.ID, "shell", "input", "tap", strconv.Itoa(x), strconv.Itoa(y))
		return err
	case "ios":
		if err := t.needIdb(); err != nil {
			return err
		}
		// idb taps in points; the screenshot is in pixels.
		scale, err := t.iosScale(ctx, d)
		if err != nil {
			return err
		}
		px := strconv.FormatFloat(float64(x)/scale, 'f', 0, 64)
		py := strconv.FormatFloat(float64(y)/scale, 'f', 0, 64)
		_, err = t.Run(ctx, t.Idb, "ui", "tap", "--udid", d.ID, px, py)
		return err
	}
	return fmt.Errorf("unknown platform %q", d.Platform)
}

// iosScale is pixels per point of a simulator's screen, which idb says.
func (t *Tools) iosScale(ctx context.Context, d Device) (float64, error) {
	out, err := t.Run(ctx, t.Idb, "describe", "--udid", d.ID, "--json")
	if err != nil {
		return 0, err
	}
	var desc struct {
		Screen struct {
			Width       float64 `json:"width"`
			WidthPoints float64 `json:"width_points"`
			Density     float64 `json:"density"`
		} `json:"screen_dimensions"`
	}
	if err := json.Unmarshal(out, &desc); err != nil {
		return 0, fmt.Errorf("idb describe was not JSON: %v", err)
	}
	if desc.Screen.WidthPoints > 0 && desc.Screen.Width > 0 {
		return desc.Screen.Width / desc.Screen.WidthPoints, nil
	}
	if desc.Screen.Density > 0 {
		return desc.Screen.Density, nil
	}
	return 0, errors.New("idb did not say the screen's size")
}

// Type enters text into whatever field has the focus on the device.
func (t *Tools) Type(ctx context.Context, d Device, text string) error {
	if err := needBooted(d); err != nil {
		return err
	}
	if text == "" {
		return nil
	}
	switch d.Platform {
	case "android":
		_, err := t.Run(ctx, t.Adb, "-s", d.ID, "shell", "input", "text", adbText(text))
		return err
	case "ios":
		if err := t.needIdb(); err != nil {
			return err
		}
		_, err := t.Run(ctx, t.Idb, "ui", "text", "--udid", d.ID, text)
		return err
	}
	return fmt.Errorf("unknown platform %q", d.Platform)
}

// adbText is text for `adb shell input text`, which goes through the
// phone's shell: a space must be %s, and every character that shell
// would read is escaped, or `it's` opens a quote that never closes.
func adbText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ':
			b.WriteString("%s")
		case strings.ContainsRune(`\'"&|;<>()$`+"`"+`*?[]{}!#~%`, r):
			b.WriteRune('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Buttons are the device buttons Button presses.
var Buttons = []string{"home", "back"}

// Button presses home or back. An iPhone has no back button, so that one
// is Android's alone.
func (t *Tools) Button(ctx context.Context, d Device, name string) error {
	if err := needBooted(d); err != nil {
		return err
	}
	switch d.Platform {
	case "android":
		code := map[string]string{"home": "KEYCODE_HOME", "back": "KEYCODE_BACK"}[name]
		if code == "" {
			return fmt.Errorf("no button %q (home or back)", name)
		}
		_, err := t.Run(ctx, t.Adb, "-s", d.ID, "shell", "input", "keyevent", code)
		return err
	case "ios":
		if name != "home" {
			return fmt.Errorf("an iPhone has no %q button (home)", name)
		}
		if err := t.needIdb(); err != nil {
			return err
		}
		_, err := t.Run(ctx, t.Idb, "ui", "button", "--udid", d.ID, "HOME")
		return err
	}
	return fmt.Errorf("unknown platform %q", d.Platform)
}
