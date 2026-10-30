package main

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/desktop"
	"github.com/Amitgb14/conch/internal/fleet"
)

// runApp is the window: internal/desktop's host and page, the page loaded
// from the webview's own scheme and reaching the host on loopback.
func runApp() error {
	sock := config.SocketPath()
	exe, err := bundledConch()
	if err != nil {
		return err
	}
	// The server is started from the conch the app carries, never from
	// the app: that would be a server with the app's build hash, which
	// every TUI calls stale (docs/plans/desktop-gui.md). One already
	// running is used as it is.
	if err := client.EnsureServerWith(sock, config.ServerLogPath(), exe); err != nil {
		return err
	}
	dial := func() (*client.Client, error) { return client.Dial(sock, "conch-desktop") }
	logf := log.New(os.Stderr, "", log.LstdFlags).Printf
	var h *desktop.Host
	pool := fleet.New(fleet.Options{
		Local:     desktop.SharedClient(dial),
		DialLocal: dial,
		Changed:   func() { h.MachinesChanged() },
		Logf:      func(f string, a ...any) { logf("desktop: "+f, a...) },
	})
	defer pool.Close()
	h = desktop.New(desktop.Options{Pool: pool, StatePath: filepath.Join(config.Dir(), "desktop.json"), Conch: exe, Logf: logf})
	defer h.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h.Handler(nil), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	defer srv.Close()

	// config.json is answered on the webview's own scheme, which nothing
	// outside this process can reach: the one place the token may be
	// handed over as data.
	cfg, _ := json.Marshal(map[string]string{"socket": "ws://" + ln.Addr().String() + "/socket", "token": h.Token()})
	webview := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(cfg)
	})
	native := newApp()
	return wails.Run(&options.App{
		Title:            "conch",
		Width:            1400,
		Height:           900,
		MinWidth:         480,
		MinHeight:        320,
		BackgroundColour: &options.RGBA{R: 10, G: 10, B: 11, A: 255},
		AssetServer:      &assetserver.Options{Assets: desktop.Assets(), Handler: webview},
		Menu:             appMenu(native),
		Bind:             []interface{}{native},
		OnStartup:        native.startup,
		// One window: opening conch again brings it forward.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "dev.conch.desktop",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				if ctx := native.context(); ctx != nil {
					wruntime.WindowUnminimise(ctx)
					wruntime.WindowShow(ctx)
				}
			},
		},
	})
}

// appMenu is the menu bar. The Edit menu is not decoration: without it
// macOS gives ⌘C and ⌘V nowhere to go in the webview, and copy and paste
// stop working. The rest send the page what the shortcut means.
func appMenu(a *App) *menu.Menu {
	m := menu.NewMenu()
	m.Append(menu.AppMenu())
	m.Append(menu.EditMenu())
	send := func(action string) func(*menu.CallbackData) {
		return func(*menu.CallbackData) {
			if ctx := a.context(); ctx != nil {
				wruntime.EventsEmit(ctx, "menu", action)
			}
		}
	}
	task := m.AddSubmenu("Task")
	task.AddText("New task", keys.CmdOrCtrl("n"), send("new"))
	task.AddText("Next waiting on you", keys.CmdOrCtrl("j"), send("waiting"))
	task.AddSeparator()
	task.AddText("Review changes", keys.Combo("r", keys.CmdOrCtrlKey, keys.ShiftKey), send("review"))
	view := m.AddSubmenu("View")
	view.AddText("Fold the sidebar", keys.CmdOrCtrl("b"), send("rail"))
	view.AddText("Show changes", keys.Combo("i", keys.CmdOrCtrlKey, keys.OptionOrAltKey), send("inspector"))
	view.AddSeparator()
	view.AddText("Settings…", keys.CmdOrCtrl(","), send("settings"))
	m.Append(menu.WindowMenu())
	return m
}

// bundledConch is the conch CLI the app carries — beside the window on
// Linux, in Contents/Resources in a macOS bundle — or, for a window run
// straight out of a build, the conch on PATH.
func bundledConch() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	self, _ = filepath.EvalSymlinks(self)
	dir := filepath.Dir(self)
	candidates := []string{filepath.Join(dir, "conch")}
	if runtime.GOOS == "darwin" {
		candidates = append([]string{filepath.Join(dir, "..", "Resources", "conch")}, candidates...)
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return c, nil
		}
	}
	if p, err := exec.LookPath("conch"); err == nil {
		return p, nil
	}
	return "", errors.New("no conch beside the app or on PATH to run the server with")
}
