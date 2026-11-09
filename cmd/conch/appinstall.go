package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Amitgb14/conch/internal/phone"
	"github.com/Amitgb14/conch/internal/proto"
)

// Putting conch in the desktop's applications, so it is started the way
// everything else is: from the launcher, with its own icon, rather than by
// typing into a terminal that somebody has to have open already.
//
// Nothing here is a package manager's job gone wrong: both files go under
// the user's own data directory, both are listed by `conch app install`
// before they are written, and `conch app remove` takes exactly those
// two away. A system-wide install is the packager's, not conch's.

// appDesktopName is the file and the icon's name, which the entry's Icon=
// line refers to by name rather than by path: that is how an icon theme
// is meant to be asked, and it then follows the theme's own sizes.
const appDesktopName = "conch"

func appInstall(args []string) error {
	if len(args) > 0 {
		return errors.New(appUsage)
	}
	conch, err := os.Executable()
	if err != nil {
		return err
	}
	// A terminal has to be there, or the entry would be a launcher that
	// does nothing when it is clicked. Said now, not on the first click.
	term, _, err := appFind("")
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return appInstallMac(conch, term)
	default:
		return appInstallXDG(conch, term)
	}
}

func appRemove(args []string) error {
	if len(args) > 0 {
		return errors.New(appUsage)
	}
	if runtime.GOOS == "darwin" {
		return appRemoveMac()
	}
	return appRemoveXDG()
}

// appRemoveMac takes the bundle away only when it is the one install
// writes. On the case-insensitive volume a Mac has by default,
// ~/Applications/conch.app is also Conch.app — the native app, which is
// somebody's own and not this command's to delete.
func appRemoveMac() error {
	bundle := appMacBundle()
	there, err := appMacOurs(bundle)
	if err != nil {
		return err
	}
	if !there {
		fmt.Println("nothing to remove:", bundle, "is not there")
		return nil
	}
	if err := os.RemoveAll(bundle); err != nil {
		return err
	}
	fmt.Println("removed", bundle)
	return nil
}

// appRemoveXDG takes away the entry and the icon that are conch's, and
// leaves — and names — any that are not.
func appRemoveXDG() error {
	entry, icon, err := appXDGOurs()
	gone := 0
	for _, p := range []struct {
		path  string
		state appOwner
	}{{appDesktopFile(), entry}, {appIconFile(), icon}} {
		if p.state != appOwnedByConch {
			continue
		}
		if err := os.Remove(p.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("removed", p.path)
		gone++
	}
	if gone > 0 {
		appRefreshMenu()
	} else if err == nil {
		fmt.Println("nothing to remove")
	}
	return err
}

// appDataDir is where a user's own desktop files go: $XDG_DATA_HOME, or
// ~/.local/share as the specification's default.
func appDataDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".local/share"
	}
	return filepath.Join(home, ".local", "share")
}

func appDesktopFile() string {
	return filepath.Join(appDataDir(), "applications", appDesktopName+".desktop")
}

// appIconFile is the scalable icon, which every icon theme reads and
// which needs no sizes generated for it.
func appIconFile() string {
	return filepath.Join(appDataDir(), "icons", "hicolor", "scalable", "apps", appDesktopName+".svg")
}

// appInstallXDG writes the desktop entry and the icon. Exec is the conch
// that is running: an entry pointing at a name on PATH would follow
// whatever that name later means, and a conch started from the launcher
// has no PATH of a shell's to look in.
func appInstallXDG(conch string, term appTerminal) error {
	// Checked before anything is written, so a refusal leaves no half.
	if _, _, err := appXDGOurs(); err != nil {
		return err
	}
	entry := strings.Join([]string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=conch",
		"Comment=Terminal orchestrator for AI coding agents",
		fmt.Sprintf("Exec=%s app", appQuoteExec(conch)),
		"Icon=" + appDesktopName,
		// conch is a terminal program, but this entry starts a terminal
		// of its own: the desktop must not open another one around it.
		"Terminal=false",
		// One main category, or the entry can appear twice in the menu —
		// which is what desktop-file-validate warns about, and both
		// Development and Utility are main ones.
		"Categories=Development;",
		"Keywords=agent;claude;codex;terminal;",
		"StartupNotify=true",
		// How remove and a later install know the file is conch's to
		// replace. The specification leaves X- keys to applications.
		appDesktopMark,
		"",
	}, "\n")
	if err := appWrite(appDesktopFile(), []byte(entry), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", appDesktopFile())
	if err := appWrite(appIconFile(), phone.Icon(), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", appIconFile())
	appRefreshMenu()
	fmt.Printf("conch is in your applications, and opens in %s.\n", term.name)
	return nil
}

// appQuoteExec is a program path as a desktop entry's Exec takes it. The
// specification layers three escapes, read in this order: the value is a
// string (\\, \n, \t, \r, \s), then Exec splits it into quoted words
// (inside double quotes, " ` $ and \ take a backslash), then % starts a
// field code, so a literal one is %%. Written, they go on in reverse. A
// path with any reserved character is quoted; a newline in one is legal
// on both systems, if unlikely, and must not end the line.
func appQuoteExec(path string) string {
	arg := path
	if strings.ContainsAny(path, appExecReserved) {
		arg = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", `\$`).Replace(path) + `"`
	}
	arg = strings.ReplaceAll(arg, "%", "%%")
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\t", `\t`, "\r", `\r`).Replace(arg)
}

// appExecReserved are the characters the specification says make an Exec
// argument need quoting. The carriage return is not among them, but a
// quoted one is the safer reading of a path that holds it.
const appExecReserved = " \t\n\r\"'\\><~|&;$*?#()`"

// appDesktopMark is the line that says a desktop entry was written by
// `conch app install`.
const appDesktopMark = "X-Conch-App=install"

// appOwner is what is at a path conch installs to.
type appOwner int

const (
	appMissing      appOwner = iota // nothing: conch may write it
	appOwnedByConch                 // conch's own: conch may replace or remove it
	appNotConchs                    // somebody else's: conch touches nothing
)

// appXDGOurs says, for the entry and the icon, whether each is missing,
// conch's or not, and an error naming those that are not. An entry is
// conch's when it carries the mark, or — written before there was one —
// the comment and Exec conch wrote. The icon is conch's when it is conch's
// icon, or sits beside an entry of conch's: an older conch may have had
// another picture.
func appXDGOurs() (entry, icon appOwner, err error) {
	entry = appXDGOwner(appDesktopFile(), func(b []byte) bool {
		lines := strings.Split(string(b), "\n")
		has := func(want func(string) bool) bool {
			for _, l := range lines {
				if want(strings.TrimRight(l, "\r")) {
					return true
				}
			}
			return false
		}
		if has(func(l string) bool { return l == appDesktopMark }) {
			return true
		}
		return has(func(l string) bool { return l == "Comment=Terminal orchestrator for AI coding agents" }) &&
			has(func(l string) bool { return strings.HasPrefix(l, "Exec=") && strings.HasSuffix(l, " app") })
	})
	icon = appXDGOwner(appIconFile(), func(b []byte) bool {
		return entry == appOwnedByConch || string(b) == string(phone.Icon())
	})
	var not []string
	for _, p := range []struct {
		path  string
		state appOwner
	}{{appDesktopFile(), entry}, {appIconFile(), icon}} {
		if p.state == appNotConchs {
			not = append(not, p.path)
		}
	}
	switch len(not) {
	case 0:
	case 1:
		err = fmt.Errorf("%s was not written by conch app install, so conch leaves it alone; move it aside for conch to use the name", not[0])
	default:
		err = fmt.Errorf("%s were not written by conch app install, so conch leaves them alone; move them aside for conch to use the names", strings.Join(not, " and "))
	}
	return entry, icon, err
}

// appXDGOwner reads one installed file. Only a regular file can be
// conch's: install never writes a link, and following one would write
// wherever it points.
func appXDGOwner(path string, ours func([]byte) bool) appOwner {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return appMissing
	}
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > appMaxOwnedFile {
		return appNotConchs
	}
	b, err := os.ReadFile(path)
	if err != nil || !ours(b) {
		return appNotConchs
	}
	return appOwnedByConch
}

// appMaxOwnedFile is more than anything conch writes, so a larger file is
// somebody else's without reading it.
const appMaxOwnedFile = 1 << 20

// appRefreshMenu tells the desktop there is a new entry, where there is a
// tool for it. Most desktops notice by themselves, so its absence is not
// worth a word, and its failure is not worth stopping for.
func appRefreshMenu() {
	path, err := exec.LookPath("update-desktop-database")
	if err != nil {
		return
	}
	dir := filepath.Join(appDataDir(), "applications")
	_ = exec.Command(path, dir).Run()
}

// appMacBundle is the application bundle in the user's own Applications,
// which needs no administrator and is what Spotlight and the Dock read.
func appMacBundle() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "Applications/conch.app"
	}
	return filepath.Join(home, "Applications", "conch.app")
}

// appMacID is the bundle identifier install writes, and how a bundle is
// known to be conch's. It is not the native app's (dev.conch.mac).
const appMacID = "dev.conch.app"

// appMacOurs says whether there is a bundle at path, and refuses — with
// an error that says what is there — one install did not write. A link,
// a file, a bundle with no Info.plist, an unreadable or binary one, and
// one with another identifier are all left alone: guessing wrong here
// overwrites or deletes somebody's application.
func appMacOurs(bundle string) (bool, error) {
	fi, err := os.Lstat(bundle)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	refuse := func(why string) (bool, error) {
		return true, fmt.Errorf("%s is not the launcher conch app install writes (%s), so conch leaves it alone. "+
			"On a Mac that is also the name of Conch.app, the native app: keep that in /Applications, "+
			"or move it aside, for conch app install to add the launcher", bundle, why)
	}
	if !fi.IsDir() {
		return refuse("it is not a folder")
	}
	plist, err := os.Open(filepath.Join(bundle, "Contents", "Info.plist"))
	if err != nil {
		return refuse("it has no Info.plist conch can read")
	}
	defer plist.Close()
	body, err := io.ReadAll(io.LimitReader(plist, appMaxOwnedFile))
	if err != nil {
		return refuse("it has no Info.plist conch can read")
	}
	switch id := appPlistID(body); id {
	case appMacID:
		return true, nil
	case "":
		return refuse("its Info.plist names no bundle identifier conch can read")
	default:
		return refuse("its bundle identifier is " + id)
	}
}

// appPlistID is CFBundleIdentifier from an XML property list, read as
// text: a <key> and the <string> after it. That is the form install
// writes, which is all this has to recognise; a binary plist reads as no
// identifier, and so as not conch's.
func appPlistID(body []byte) string {
	s := string(body)
	const key = "<key>CFBundleIdentifier</key>"
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	s = strings.TrimLeft(s[i+len(key):], " \t\r\n")
	if !strings.HasPrefix(s, "<string>") {
		return ""
	}
	s = s[len("<string>"):]
	end := strings.Index(s, "</string>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(s[:end])
}

// appInstallMac writes a bundle whose program is a script that runs
// `conch app`: the bundle is a way to be double-clicked, and the terminal
// it opens is the same one the command opens.
func appInstallMac(conch string, term appTerminal) error {
	bundle := appMacBundle()
	if _, err := appMacOurs(bundle); err != nil {
		return err
	}
	plist := strings.Join([]string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">`,
		`<plist version="1.0">`,
		`<dict>`,
		`	<key>CFBundleName</key><string>conch</string>`,
		`	<key>CFBundleDisplayName</key><string>conch</string>`,
		`	<key>CFBundleIdentifier</key><string>` + appMacID + `</string>`,
		`	<key>CFBundleExecutable</key><string>conch</string>`,
		`	<key>CFBundleIconFile</key><string>conch</string>`,
		`	<key>CFBundlePackageType</key><string>APPL</string>`,
		`	<key>CFBundleShortVersionString</key><string>` + proto.Version + `</string>`,
		// It opens a terminal and exits; without this it would keep a
		// bouncing Dock icon for a program that is no longer running.
		`	<key>LSUIElement</key><true/>`,
		`</dict>`,
		`</plist>`,
		``,
	}, "\n")
	script := "#!/bin/sh\n# Written by conch (conch app install).\nexec " + appShellQuote(conch) + " app \"$@\"\n"
	if err := appWrite(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		return err
	}
	if err := appWrite(filepath.Join(bundle, "Contents", "MacOS", "conch"), []byte(script), 0o755); err != nil {
		return err
	}
	fmt.Println("wrote", bundle)
	if err := appMacIcon(bundle); err != nil {
		// The bundle works without it, showing the system's blank icon,
		// so this is said rather than failed.
		fmt.Printf("without an icon: %v\n", err)
	}
	fmt.Printf("conch is in ~/Applications, and opens in %s.\n", term.name)
	return nil
}

// appShellQuote is a path as /bin/sh takes it inside single quotes.
func appShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appMacIcon makes the bundle's .icns from conch's own icon, with the
// tools macOS ships (sips rescales, iconutil packs). Neither is conch's
// to install, so a computer without them gets a bundle and no icon.
func appMacIcon(bundle string) error {
	sips, err := exec.LookPath("sips")
	if err != nil {
		return errors.New("sips is not here to rescale the icon")
	}
	iconutil, err := exec.LookPath("iconutil")
	if err != nil {
		return errors.New("iconutil is not here to pack the icon")
	}
	dir, err := os.MkdirTemp("", "conchicon")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	set := filepath.Join(dir, "conch.iconset")
	if err := os.MkdirAll(set, 0o700); err != nil {
		return err
	}
	src := filepath.Join(dir, "icon.png")
	if err := os.WriteFile(src, phone.IconPNG(), 0o600); err != nil {
		return err
	}
	// The sizes a .icns is expected to carry; iconutil names them.
	for _, size := range []int{16, 32, 128, 256, 512} {
		for _, scale := range []int{1, 2} {
			name := fmt.Sprintf("icon_%dx%d.png", size, size)
			if scale == 2 {
				name = fmt.Sprintf("icon_%dx%d@2x.png", size, size)
			}
			px := fmt.Sprint(size * scale)
			out := filepath.Join(set, name)
			if err := exec.Command(sips, "-z", px, px, src, "--out", out).Run(); err != nil {
				return fmt.Errorf("sips could not make %s: %w", name, err)
			}
		}
	}
	icns := filepath.Join(bundle, "Contents", "Resources", "conch.icns")
	if err := os.MkdirAll(filepath.Dir(icns), 0o700); err != nil {
		return err
	}
	if err := exec.Command(iconutil, "-c", "icns", set, "-o", icns).Run(); err != nil {
		return fmt.Errorf("iconutil could not pack the icon: %w", err)
	}
	return nil
}

// appWrite writes a file and the directories above it.
func appWrite(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, mode)
}
