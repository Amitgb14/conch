// Command conch-desktop is conch's desktop window (docs/plans/desktop-gui.md).
// Run with no flags it is the app (app.go); the flags are phase 0's
// measurements, kept while the window is young.
//
//	conch-desktop                                # the app
//	conch-desktop -bench frames.jsonl [-quit]    # renderer benchmark
//	conch-desktop -socket PATH [-autotest -quit] # one live pane on a conch server
//	conch-desktop -browser ...                   # the same, in a browser
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed ui
var uiDir embed.FS

func main() {
	benchFile := flag.String("bench", "", "replay frames recorded by ./record")
	sock := flag.String("socket", "", "a conch server's socket, for the live pane")
	browser := flag.Bool("browser", false, "serve the page to a browser instead of opening a window")
	autotest := flag.Bool("autotest", false, "live: type by itself and measure echo")
	quit := flag.Bool("quit", false, "exit after the first report")
	flag.Parse()
	if flag.NFlag() == 0 {
		if err := runApp(); err != nil {
			fail(err)
		}
		return
	}

	h := &host{token: newToken(), sock: *sock, autotest: *autotest, reports: make(chan json.RawMessage, 1)}
	if *benchFile != "" {
		frames, err := loadFrames(*benchFile)
		if err != nil {
			fail(err)
		}
		h.frames = frames
	} else if *sock == "" {
		fail(fmt.Errorf("give -bench FILE or -socket PATH"))
	}
	assets, _ := fs.Sub(uiDir, "ui")
	ln, err := h.listen()
	if err != nil {
		fail(err)
	}

	if *browser {
		go serve(ln, h.handler(assets))
		fmt.Printf("open http://%s/#token=%s\n", h.addr, h.token)
		r := <-h.reports
		fmt.Println(string(r))
		if !*quit {
			select {}
		}
		return
	}

	go serve(ln, h.handler(nil))
	err = wails.Run(&options.App{
		Title:            "conch",
		Width:            1400,
		Height:           900,
		BackgroundColour: &options.RGBA{R: 30, G: 30, B: 30, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets, Handler: h.webviewHandler()},
		OnStartup: func(ctx context.Context) {
			go func() {
				for r := range h.reports {
					fmt.Println(string(r))
					if *quit {
						runtime.Quit(ctx)
						return
					}
				}
			}()
		},
	})
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "conch-desktop:", err)
	os.Exit(1)
}
