package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/phone"
)

const webUsage = `usage: conch web [-listen ADDR] [-port N] [-url URL] [-cert FILE -key FILE]
       conch web pair [-permission view|reply|full]
       conch web devices
       conch web revoke ID
       conch web permission ID view|reply|full
       conch web stop`

// interfaceAddrs is this machine's addresses; a test puts its own here.
var interfaceAddrs = net.InterfaceAddrs

// tailnetName is this machine's name on the tailnet, as Tailscale says
// it ("laptop.tail1234.ts.net"), or "" when that can't be learnt.
// CONCH_TAILSCALE names the tailscale program; tests put a fake there.
var tailnetName = func() string {
	bin := os.Getenv("CONCH_TAILSCALE")
	if bin == "" {
		bin = "tailscale"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "status", "--json").Output()
	if err != nil {
		return ""
	}
	var st struct {
		Self struct{ DNSName string }
	}
	if json.Unmarshal(out, &st) != nil {
		return ""
	}
	return strings.TrimSuffix(st.Self.DNSName, ".")
}

// stdoutIsTerminal says whether a QR code printed now would be seen, not
// written into a file or a pipe as escape codes.
var stdoutIsTerminal = func() bool { return term.IsTerminal(os.Stdout.Fd()) }

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
		case "permission":
			return webPermission(args[1:])
		case "stop":
			return webStop(args[1:])
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
	// full by default: a phone is paired to be a terminal as much as to
	// answer, and a pairing that can't type looked broken. -permission
	// reply or view pairs one that may do less.
	permission := fs.String("permission", phone.PermFull, "what the device may do: view, reply or full")
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
	url := store.URL()
	if url != "" && stdoutIsTerminal() {
		// The code opens the page with the pairing code filled in.
		if lines, err := phone.QRLines(phone.PairLink(url, code)); err == nil {
			fmt.Println(strings.Join(lines, "\n"))
		}
	}
	fmt.Printf("pairing code: %s\n", code)
	fmt.Printf("good for %d minutes, once, for a device with the %s permission\n", int(phone.PairTTL.Minutes()), *permission)
	if url != "" {
		fmt.Printf("scan the code with the phone's camera, or open %s on it and enter the code\n", url)
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

// webStop stops the conch web running in the background — one the TUI
// started, with no terminal of its own to press ctrl+c in.
func webStop(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: conch web stop")
	}
	store := phone.OpenStore(config.Dir())
	run, running := store.Gateway()
	if !running {
		fmt.Println("conch web is not running")
		return nil
	}
	if err := syscall.Kill(run.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stopping conch web (pid %d): %w", run.PID, err)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, running := store.Gateway(); !running {
			fmt.Printf("stopped conch web (pid %d)\n", run.PID)
			return nil
		}
	}
	return fmt.Errorf("conch web (pid %d) did not stop within 5s", run.PID)
}

// webPermission changes what a paired device may do, without pairing it
// again: a phone paired to reply that should type into terminals.
func webPermission(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: conch web permission ID view|reply|full")
	}
	if err := notFromAnAgent("change what a device may do"); err != nil {
		return err
	}
	found, err := phone.OpenStore(config.Dir()).SetPermission(args[0], args[1])
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no device %q; `conch web devices` lists them", args[0])
	}
	fmt.Printf("%s may now %s; a running gateway takes it from its next request\n", args[0], map[string]string{
		phone.PermView: "look only", phone.PermReply: "reply and answer",
		phone.PermFull: "reply, answer, type into terminals and start, rename and close panes",
	}[args[1]])
	return nil
}

func webServe(args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	listen := fs.String("listen", "", "listen on ADDR instead of this machine's Tailscale address (or loopback, with -url)")
	port := fs.Int("port", phone.DefaultPort, "port to listen on")
	cert := fs.String("cert", "", "TLS certificate, to serve HTTPS here instead of through `tailscale serve`")
	key := fs.String("key", "", "the certificate's key")
	public := fs.String("url", "", "the address phones open, when it isn't where this listens: the https://…ts.net name `tailscale serve` gives")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(webUsage)
	}
	// What isn't given here comes from [web] in config.toml — what the
	// TUI's settings set — so `conch web` alone, or the TUI starting it,
	// listens and pairs as it was set up.
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if cfg, err := config.Load(); err == nil {
		if !given["url"] && cfg.Web.URL != "" {
			*public = cfg.Web.URL
		}
		if !given["port"] {
			*port = cfg.Web.PortOrDefault()
		}
	}
	if *public != "" {
		u, err := neturl.Parse(*public)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return fmt.Errorf("-url %q: an http or https address, like https://laptop.tail1234.ts.net", *public)
		}
	}
	if (*cert == "") != (*key == "") {
		return errors.New("-cert and -key go together")
	}
	if *port < 1 || *port > 65535 {
		return fmt.Errorf("-port %d: not a port", *port)
	}
	var addr, warning string
	if *public != "" && *listen == "" {
		// Behind tailscale serve, which reaches the gateway on loopback:
		// the Tailscale app on macOS can't proxy back to the machine's own
		// tailnet address, and nothing on the tailnet should reach the
		// plain-http port directly anyway.
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(*port))
	} else {
		addrs, err := interfaceAddrs()
		if err != nil {
			return err
		}
		if addr, warning, err = phone.ListenAddr(*listen, *port, addrs); err != nil {
			return err
		}
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
	open := url
	if *public != "" {
		open = strings.TrimRight(*public, "/")
	}
	// What `conch web pair` and the TUI put in the QR code.
	if err := store.SetURL(open); err != nil {
		ln.Close()
		return err
	}
	// And how this gateway was started, so the TUI can tell it runs and
	// start it again the same way when it doesn't.
	if err := store.GatewayStarted(os.Getpid(), args, time.Now()); err != nil {
		ln.Close()
		return err
	}
	defer store.GatewayStopped(os.Getpid())
	fmt.Printf("conch web: listening on %s\n", url)
	if *public != "" {
		if err := g.AllowOrigin(open); err != nil {
			ln.Close()
			return err
		}
		fmt.Printf("phones open %s\n", open)
		if host, p, _ := net.SplitHostPort(ln.Addr().String()); net.ParseIP(host).IsLoopback() {
			fmt.Printf("with Tailscale's HTTPS in front of it: tailscale serve --bg http://127.0.0.1:%s\n", p)
		}
	} else if *cert == "" {
		// The device cookie is Secure, so a browser keeps it only over
		// HTTPS: pairing at this address is refused, and says why.
		name := "<this machine>.<tailnet>.ts.net"
		if n := tailnetName(); n != "" {
			name = n
		}
		fmt.Printf("phones can't pair over plain http. Put Tailscale's HTTPS in front: stop this, then run\n"+
			"  tailscale serve --bg http://127.0.0.1:%d\n  conch web -url https://%s\n(or pass -cert and -key)\n", *port, name)
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
