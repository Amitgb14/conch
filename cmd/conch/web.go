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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/phone"
	"github.com/Amitgb14/conch/internal/relay"
)

const webUsage = `usage: conch web [-listen ADDR] [-port N] [-url URL] [-cert FILE -key FILE] [-relay URL]
       conch web relay [reset]
       conch web pair [-permission view|reply|full] [-machine NAME]...
       conch web devices
       conch web revoke ID
       conch web permission ID view|reply|full [-machine NAME]...
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
		case "start":
			return webStart(args[1:])
		case "relay":
			return webRelay(args[1:])
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
	// -json is for a program pairing a phone in its own window — Conch.app
	// — which draws the QR code and says the rest itself.
	asJSON := fs.Bool("json", false, "print the code, its link and the gateway's state as JSON")
	// A device reaches this computer and the machines named here. Said at
	// pairing because that is when somebody knows what the phone is for;
	// without it they pair, look for a machine, and find it missing.
	var machines machineNames
	fs.Var(&machines, "machine", "a machine this device may reach as well as this computer; repeat for several")
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
	code, err := store.NewCode(*permission, time.Now(), machines.names...)
	if err != nil {
		return err
	}
	url := store.URL()
	secure := strings.HasPrefix(url, "https://")
	rp, err := relayPairing(store, code)
	if err != nil {
		return err
	}
	if *asJSON {
		return printPairJSON(store, code, *permission, machines.names, url, secure, rp)
	}
	link := ""
	switch {
	case rp.Link != "":
		// Through the relay works from anywhere, at home too.
		link = rp.Link
	case url != "" && secure:
		link = phone.PairLink(url, code)
	}
	drawn := false
	if link != "" && stdoutIsTerminal() {
		// The code opens the page with the pairing code filled in.
		if lines, err := phone.QRLines(link); err == nil {
			fmt.Println(strings.Join(lines, "\n"))
			drawn = true
		}
	}
	fmt.Printf("pairing code: %s\n", code)
	fmt.Printf("good for %d minutes, once, for a device with the %s permission\n", int(phone.PairTTL.Minutes()), *permission)
	// Where, as well as what: a phone that reaches this computer only is
	// the thing people are surprised by, so it is said rather than left
	// to be discovered when a machine is missing from the app.
	fmt.Println(reachWords(machines.names))
	if rp.Link != "" {
		if drawn {
			fmt.Printf("scan the code with the phone's camera: it opens %s, the relay, from any network\n", rp.Page)
		} else {
			// No QR code where this is going — `!` in an agent, a pipe — so
			// the link it would have carried, which is the only way in: the
			// relay's page alone has no key to make the session with.
			fmt.Printf("open this link on the phone: it reaches %s, the relay, from any network\n  %s\n", rp.Page, link)
		}
		fmt.Printf("the phone shows this computer's key as %q: the same words, the same key\n", rp.Words)
		if !rp.Connected {
			fmt.Println("conch web isn't connected to the relay yet: start it with `conch web`, or see `conch web relay`")
		}
		return nil
	}
	if link != "" && !drawn {
		fmt.Printf("open this link on the phone, or open %s on it and enter the code\n  %s\n", url, link)
		return nil
	}
	fmt.Println(pairWhere(url))
	return nil
}

// relayLinkFor is a gateway's link to its relay, with its room and key.
func relayLink(store *phone.Store, relayURL string, h http.Handler) (*relay.Link, error) {
	secret, key, err := store.Relay()
	if err != nil {
		return nil, err
	}
	pid := os.Getpid()
	room := relay.RoomID(secret)
	return &relay.Link{Relay: relayURL, Secret: secret, Key: key, Handler: h,
		Logf: log.New(os.Stderr, "", log.LstdFlags).Printf,
		OnStatus: func(st relay.Status) {
			_ = store.SetRelayState(&phone.RelayState{URL: relayURL, Room: room, PID: pid, Connected: st.Connected, Error: st.Error, Since: st.Since})
		}}, nil
}

// relayPairing is a pairing through the relay phones use, when there is
// one: the settings' — or, with none there, the one a running conch web
// was started with (-relay), which pairing must use as well.
func relayPairing(store *phone.Store, code string) (phone.RelayPair, error) {
	cfg, _ := config.Load()
	return store.RelayPairing(store.RelayURL(cfg.Web.Relay), code)
}

// revokedWords says how many phones a reset revoked.
func revokedWords(n int) string {
	switch n {
	case 0:
		return "no phone had paired through the relay"
	case 1:
		return "the 1 phone paired through the relay is revoked"
	}
	return fmt.Sprintf("the %d phones paired through the relay are revoked", n)
}

// webRelay is `conch web relay`: how the link is; reset makes a new key.
func webRelay(args []string) error {
	store := phone.OpenStore(config.Dir())
	switch {
	case len(args) == 1 && args[0] == "reset":
		if err := notFromAnAgent("reset the relay key"); err != nil {
			return err
		}
		revoked, err := store.ResetRelay()
		if err != nil {
			return err
		}
		fmt.Printf("a new relay key: %s, and they pair again (restart conch web to use it)\n", revokedWords(revoked))
		return nil
	case len(args) > 0:
		return errors.New(webUsage)
	}
	cfg, _ := config.Load()
	url := store.RelayURL(cfg.Web.Relay)
	if url == "" {
		fmt.Println("no relay: set [web] relay = \"wss://…\" in config.toml (a server running `conch relay`)")
		return nil
	}
	secret, key, err := store.Relay()
	if err != nil {
		return err
	}
	fmt.Printf("relay: %s\nkey:   %s\n", url, relay.Fingerprint(key.Public))
	st, running := store.RelayState()
	switch {
	case !running:
		fmt.Println("conch web isn't running: `conch web` connects to it")
	case st.URL != url:
		fmt.Printf("conch web is connected to %s, not this one: restart it\n", st.URL)
	case st.Room != "" && st.Room != relay.RoomID(secret):
		fmt.Println("conch web is using the key from before a reset: restart it")
	case st.Connected:
		fmt.Printf("connected since %s\n", st.Since.Local().Format("15:04"))
	default:
		fmt.Printf("not connected: %s\n", st.Error)
	}
	return nil
}

// pairWhere says where a phone enters the code. A gateway on plain http
// is one no phone can pair through — the browser keeps a device's key only
// over https, which conch web says when it starts — so pointing at its
// address, or putting it in a QR code, sent somebody to a page that would
// refuse them. Seen in R62: conch web on 127.0.0.1 said phones can't pair
// over plain http, and conch web pair then said to open that very address.
func pairWhere(url string) string {
	switch {
	case url == "":
		return "start the gateway with `conch web`, open its address on the phone and enter it"
	case strings.HasPrefix(url, "https://"):
		return "scan the code with the phone's camera, or open " + url + " on it and enter the code"
	}
	return "conch web is on " + url + ", which a phone can't pair over: put Tailscale's HTTPS in front " +
		"(tailscale serve, then conch web -url https://…), open that address on the phone and enter the code"
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
	fmt.Fprintln(tw, "ID\tNAME\tPERMISSION\tMACHINES\tPAIRED")
	for _, d := range devs {
		// What a device reaches is as much a permission as what it may
		// do, so it is listed beside it rather than having to be asked for.
		where := "this computer"
		if len(d.Machines) > 0 {
			where = "this computer, " + strings.Join(d.Machines, ", ")
		}
		if d.Relay {
			// What `conch web relay reset` revokes.
			where += " (through the relay)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", d.ID, d.Name, d.Permission, where,
			d.Created.Local().Format("2006-01-02 15:04"))
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

// webPermission changes what a paired device may do, and with -machine
// where: a phone paired to reply that should type into terminals, or one
// that should reach a machine as well as this computer.
//
// A device reaches this computer and nothing else until a machine is
// named here. That is on purpose: a machine is reached with your own
// credentials, so a phone gaining one is a decision, not a side effect of
// updating conch.
func webPermission(args []string) error {
	fs := flag.NewFlagSet("web permission", flag.ContinueOnError)
	var machines machineNames
	fs.Var(&machines, "machine", "a machine this device may reach as well as this computer; repeat for several, `local` for none")
	// The ID and what it may do come first, so -machine lands after them:
	// Go's flag package stops at the first argument that is not a flag,
	// so the flags are parsed from what follows the two.
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return errors.New("usage: conch web permission ID view|reply|full [-machine NAME]...")
	}
	id, permission := rest[0], rest[1]
	if err := fs.Parse(rest[2:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: conch web permission ID view|reply|full [-machine NAME]...")
	}
	if err := notFromAnAgent("change what a device may do"); err != nil {
		return err
	}
	store := phone.OpenStore(config.Dir())
	found, err := store.SetPermission(id, permission)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no device %q; `conch web devices` lists them", id)
	}
	fmt.Printf("%s may now %s; a running gateway takes it from its next request\n", id, map[string]string{
		phone.PermView: "look only", phone.PermReply: "reply and answer",
		phone.PermFull: "reply, answer, type into terminals and start, rename and close panes",
	}[permission])
	// -machine left out leaves the machines alone: changing what a device
	// may do should not quietly change where.
	if !machines.given {
		return nil
	}
	if _, err := store.SetMachines(id, machines.names); err != nil {
		return err
	}
	if len(machines.names) == 0 || len(machines.names) == 1 && machines.names[0] == "local" {
		fmt.Printf("%s reaches this computer only\n", id)
		return nil
	}
	fmt.Printf("%s also reaches %s\n", id, strings.Join(machines.names, ", "))
	fmt.Println("a machine it reaches is reached with your credentials; `conch web devices` lists what each device holds")
	return nil
}

// reachWords says where the device being paired will reach, and how to
// change it afterwards. Every device reaches this computer; anything else
// is named on purpose, because a machine is reached with your own
// credentials and conch cannot tell a test box from a production one.
func reachWords(machines []string) string {
	if len(machines) == 0 {
		return "it will reach this computer only; -machine NAME at pairing, or `conch web permission ID " +
			"PERM -machine NAME` later, lets it reach a machine"
	}
	return "it will reach this computer and " + strings.Join(machines, ", ") +
		" — reached with your own credentials, as you"
}

// machineNames collects a repeated -machine. It remembers having been
// given at all, so `permission ID view` alone leaves the machines as they
// were while `-machine local` clears them.
type machineNames struct {
	names []string
	given bool
}

func (m *machineNames) String() string { return strings.Join(m.names, ",") }

func (m *machineNames) Set(v string) error {
	m.given = true
	for _, name := range strings.Split(v, ",") {
		if name = strings.TrimSpace(name); name != "" {
			m.names = append(m.names, name)
		}
	}
	return nil
}

func webServe(args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	listen := fs.String("listen", "", "listen on ADDR instead of this machine's Tailscale address (or loopback, with -url)")
	port := fs.Int("port", phone.DefaultPort, "port to listen on")
	cert := fs.String("cert", "", "TLS certificate, to serve HTTPS here instead of through `tailscale serve`")
	key := fs.String("key", "", "the certificate's key")
	public := fs.String("url", "", "the address phones open, when it isn't where this listens: the https://…ts.net name `tailscale serve` gives")
	relayURL := fs.String("relay", "", "a relay to connect out to, so phones reach this computer from anywhere: wss://relay.example.com")
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
		if !given["relay"] && cfg.Web.Relay != "" {
			*relayURL = cfg.Web.Relay
		}
	}
	if *relayURL != "" {
		if _, err := relay.HTTPURL(*relayURL); err != nil {
			return fmt.Errorf("-relay: %v", err)
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
		addr, warning, err = phone.ListenAddr(*listen, *port, addrs)
		if errors.Is(err, phone.ErrNoTailscale) && *relayURL != "" {
			// No Tailscale, and none needed: phones come through the
			// relay. Listening on loopback serves this computer only.
			addr, warning, err = net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)), "", nil
		}
		if err != nil {
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
	} else if *cert == "" && *relayURL == "" {
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
	if *relayURL != "" {
		link, err := relayLink(store, *relayURL, g.Handler())
		if err != nil {
			ln.Close()
			return err
		}
		fmt.Printf("phones reach this computer from anywhere through %s; its key is %q\n", *relayURL, relay.Fingerprint(link.Key.Public))
		defer store.SetRelayState(nil)
		go func() { _ = link.Run(ctx) }()
	}
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

// pairJSON is conch web pair -json: what a window needs to pair a phone.
// Link is set only where a phone can pair through it (https); Where says
// what to do otherwise, as the plain pairing does.
type pairJSON struct {
	Code       string    `json:"code"`
	Permission string    `json:"permission"`
	Expires    time.Time `json:"expires"`
	Reach      string    `json:"reach"`
	URL        string    `json:"url,omitempty"`
	Link       string    `json:"link,omitempty"`
	Where      string    `json:"where"`
	Running    bool      `json:"running"`
	CanStart   bool      `json:"can_start"`
	// Relay is the relay a phone pairs through, when one is set: the
	// link is then the relay's, which works from any network.
	Relay          string `json:"relay,omitempty"`
	RelayConnected bool   `json:"relay_connected,omitempty"`
	KeyWords       string `json:"key_words,omitempty"`
}

func printPairJSON(store *phone.Store, code, permission string, machines []string, url string, secure bool, rp phone.RelayPair) error {
	cfg, _ := config.Load()
	_, running := store.Gateway()
	_, can := phone.GatewayArgs(cfg.Web.URL, store)
	out := pairJSON{Code: code, Permission: permission, Expires: time.Now().Add(phone.PairTTL).UTC(),
		Reach: reachWords(machines), URL: url, Where: pairWhere(url), Running: running, CanStart: !running && can}
	if url != "" && secure {
		out.Link = phone.PairLink(url, code)
	}
	if rp.Link != "" {
		out.Link, out.Relay, out.RelayConnected, out.KeyWords = rp.Link, rp.Page, rp.Connected, rp.Words
		out.Where = "scan the code with the phone's camera: it works from any network, through " + rp.Page
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// startWebProcess runs the gateway's command; tests start nothing.
var startWebProcess = func(cmd *exec.Cmd) error { return cmd.Start() }

// webStart starts conch web in the background as the TUI's pair dialog
// does (s): with the settings' address, else as it was last started.
func webStart(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: conch web start")
	}
	store := phone.OpenStore(config.Dir())
	if run, running := store.Gateway(); running {
		fmt.Printf("conch web is running (pid %d)\n", run.PID)
		return nil
	}
	cfg, _ := config.Load()
	gargs, ok := phone.GatewayArgs(cfg.Web.URL, store)
	if !ok {
		return errors.New("conch web has not run here and the settings name no address: start it once with `conch web` (and -url if a phone opens it through tailscale serve)")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := phone.StartGateway(exe, gargs, startWebProcess); err != nil {
		return fmt.Errorf("conch web didn't start: %w", err)
	}
	fmt.Println("starting conch web in the background; its output is in " + filepath.Join(config.Dir(), "web.log"))
	return nil
}
