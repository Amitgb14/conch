package main

import (
	"errors"
	"fmt"
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
	var paths []string
	if runtime.GOOS == "darwin" {
		paths = []string{appMacBundle()}
	} else {
		paths = []string{appDesktopFile(), appIconFile()}
	}
	gone := 0
	for _, p := range paths {
		switch err := os.RemoveAll(p); {
		case err != nil:
			return err
		default:
			fmt.Println("removed", p)
			gone++
		}
	}
	if gone > 0 {
		appRefreshMenu()
	}
	return nil
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

// appQuoteExec is a program path as a desktop entry's Exec takes it:
// quoted when it holds a space, with the backslash and the quote escaped
// as the specification asks.
func appQuoteExec(path string) string {
	if !strings.ContainsAny(path, " \t\"'\\$`") {
		return path
	}
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", "\\$").Replace(path)
	return `"` + esc + `"`
}

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

// appInstallMac writes a bundle whose program is a script that runs
// `conch app`: the bundle is a way to be double-clicked, and the terminal
// it opens is the same one the command opens.
func appInstallMac(conch string, term appTerminal) error {
	bundle := appMacBundle()
	plist := strings.Join([]string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">`,
		`<plist version="1.0">`,
		`<dict>`,
		`	<key>CFBundleName</key><string>conch</string>`,
		`	<key>CFBundleDisplayName</key><string>conch</string>`,
		`	<key>CFBundleIdentifier</key><string>dev.conch.app</string>`,
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
