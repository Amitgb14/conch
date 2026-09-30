package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/phone"
)

const webUsage = `usage: conch web [-listen ADDR] [-port N] [-cert FILE -key FILE]
       conch web pair [-permission view|reply|full]
       conch web devices
       conch web revoke ID`

// interfaceAddrs is this machine's addresses; a test puts its own here.
var interfaceAddrs = net.InterfaceAddrs

// runWeb is `conch web`: the gateway a phone reaches, and the commands
// that say which phones may.
func runWeb(args []string) error {
	if machineFlag != "" && machineFlag != "local" {
		return errors.New("conch web serves this machine's agents; run it on " + machineFlag + " itself")
	}
	if len(args) > 0 {
		switch args[0] {
		case "pair":
			return webPair(args[1:])
		case "devices":
			return webDevices()
		case "revoke":
			return webRevoke(args[1:])
		}
	}
	return webServe(args)
}

// notFromAnAgent refuses to decide which phones may drive conch on behalf
// of an agent the server keeps to its own work: a device it paired would
// act as the person, outside that scope. Like the scoping, it is a guard
// for an agent that asks, not a lock.
func notFromAnAgent(what string) error {
	who, err := localCaller()
	if err != nil {
		return err
	}
	if who.Scoped {
		return fmt.Errorf("the %s agent in %s may not %s: a paired device acts as you, not as the agent; "+
			"run this from the TUI or a terminal pane", who.Agent, who.Pane, what)
	}
	return nil
}

func webPair(args []string) error {
	fs := flag.NewFlagSet("web pair", flag.ContinueOnError)
	permission := fs.String("permission", phone.PermReply, "what the device may do: view, reply or full")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(webUsage)
	}
	if err := notFromAnAgent("pair a device"); err != nil {
		return err
	}
	store := phone.OpenStore(config.Dir())
	code, err := store.NewCode(*permission, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("pairing code: %s\n", code)
	fmt.Printf("good for %d minutes, once, for a device with the %s permission\n", int(phone.PairTTL.Minutes()), *permission)
	if url := store.URL(); url != "" {
		fmt.Printf("open %s on the phone and enter it\n", url)
	} else {
		fmt.Println("start the gateway with `conch web`, open its address on the phone and enter it")
	}
	return nil
}

func webDevices() error {
	devs, err := phone.OpenStore(config.Dir()).Devices()
	if err != nil {
		return err
	}
	if len(devs) == 0 {
		fmt.Println("no devices paired; pair one with `conch web pair`")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tPERMISSION\tPAIRED")
	for _, d := range devs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", d.ID, d.Name, d.Permission, d.Created.Local().Format("2006-01-02 15:04"))
	}
	return tw.Flush()
}

func webRevoke(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: conch web revoke ID")
	}
	if err := notFromAnAgent("revoke a device"); err != nil {
		return err
	}
	found, err := phone.OpenStore(config.Dir()).Revoke(args[0])
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no device %q; `conch web devices` lists them", args[0])
	}
	fmt.Printf("revoked %s: its token no longer works, and a running gateway closes its connections\n", args[0])
	return nil
}

func webServe(args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	listen := fs.String("listen", "", "listen on ADDR instead of this machine's Tailscale address")
	port := fs.Int("port", phone.DefaultPort, "port to listen on")
	cert := fs.String("cert", "", "TLS certificate, to serve HTTPS here instead of through `tailscale serve`")
	key := fs.String("key", "", "the certificate's key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(webUsage)
	}
	if (*cert == "") != (*key == "") {
		return errors.New("-cert and -key go together")
	}
	if *port < 1 || *port > 65535 {
		return fmt.Errorf("-port %d: not a port", *port)
	}
	addrs, err := interfaceAddrs()
	if err != nil {
		return err
	}
	addr, warning, err := phone.ListenAddr(*listen, *port, addrs)
	if err != nil {
		return err
	}
	if warning != "" {
		fmt.Fprintln(os.Stderr, "conch: warning:", warning)
	}

	// The gateway is a client of this machine's server, as the TUI is.
	sock := config.SocketPath()
	if err := client.EnsureServer(sock, config.ServerLogPath()); err != nil {
		return err
	}
	store := phone.OpenStore(config.Dir())
	g := phone.New(store, func() (*client.Client, error) { return client.Dial(sock, "conch-web") },
		log.New(os.Stderr, "", log.LstdFlags).Printf)
	defer g.Close()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	scheme := "http"
	if *cert != "" {
		scheme = "https"
	}
	url := scheme + "://" + ln.Addr().String()
	if err := store.SetURL(url); err != nil {
		ln.Close()
		return err
	}
	fmt.Printf("conch web: listening on %s\n", url)
	if *cert == "" {
		// The device cookie is Secure, so a phone only sends it over HTTPS.
		fmt.Printf("phones need HTTPS: put it in front with `tailscale serve --bg %s`, or pass -cert and -key\n", url)
	}
	fmt.Println("pair a phone with `conch web pair`")

	srv := &http.Server{Handler: g.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		if *cert != "" {
			errc <- srv.ServeTLS(ln, *cert, *key)
		} else {
			errc <- srv.Serve(ln)
		}
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	g.Close() // say goodbye to open sockets before the listener goes
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	return nil
}
