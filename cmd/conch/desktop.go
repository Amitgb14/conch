package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/desktop"
	"github.com/Amitgb14/conch/internal/fleet"
)

// runDesktop serves the desktop window's page to a browser. The window
// itself is the conch desktop app, a separate build (desktop/); this is
// the same page and host without it, for using it before the app is
// installed and for developing it.
func runDesktop(args []string) error {
	fs := flag.NewFlagSet("desktop", flag.ContinueOnError)
	browser := fs.Bool("browser", false, "open the window's page in a browser")
	listen := fs.String("listen", "127.0.0.1:0", "where to serve it: an address on this computer")
	noOpen := fs.Bool("no-open", false, "print the address instead of opening it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("conch desktop: unexpected %q", fs.Arg(0))
	}
	if !*browser {
		return errors.New("conch desktop: the window is the conch desktop app; `conch desktop -browser` opens the same page in a browser")
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		return fmt.Errorf("-listen %q: %v", *listen, err)
	}
	// The page acts as you on every machine: it is served to this
	// computer and nobody else, token or not.
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("-listen %q: only a loopback address (127.0.0.1 or ::1)", *listen)
	}

	sock := config.SocketPath()
	if err := client.EnsureServer(sock, config.ServerLogPath()); err != nil {
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
	self, _ := buildinfo.Executable()
	h = desktop.New(desktop.Options{Pool: pool, StatePath: filepath.Join(config.Dir(), "desktop.json"), Conch: self, Logf: logf})
	defer h.Close()

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h.Handler(desktop.Assets()), ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	defer srv.Close()

	// The token goes in the fragment, which a browser never sends: it
	// reaches the page and nothing on the way.
	url := fmt.Sprintf("http://%s/#token=%s", ln.Addr(), h.Token())
	fmt.Printf("conch desktop: %s\n", url)
	if !*noOpen {
		if err := openInBrowser(url); err != nil {
			fmt.Fprintln(os.Stderr, "conch desktop: could not open a browser:", err)
		}
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	select {
	case <-stop:
		return nil
	case err := <-served:
		return err
	}
}
