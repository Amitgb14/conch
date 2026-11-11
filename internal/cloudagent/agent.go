package cloudagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/flynn/noise"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/relay"
	"github.com/Amitgb14/conch/internal/update"
)

// Options are where an agent finds things. The zero value is a cloud
// machine's (PROTOCOL §4): boot.json in /run/conch-cloud, the key in
// /var/lib/conch/cloud, and the person's side running as user conch when
// the agent is root. Tests point each somewhere of their own.
type Options struct {
	BootPath     string
	KeyPath      string
	StateDir     string
	ServerSocket string // the conch server's; default: User's
	ServerLog    string
	AgentSocket  string // the agent's own (local.go)
	User         *User  // nil: this process's own user
	Executable   string // conch, to start the server and as git's credential helper
	HTTP         *http.Client
	Logf         func(format string, args ...any)
	// GitConfig sets one system-wide git setting (git.go); nil is git
	// config --system.
	GitConfig func(ctx context.Context, args ...string) error
}

// Agent is one run of conch cloud agent.
type Agent struct {
	opts Options
	key  noise.DHKey
	api  *API
	// serverKey is what the conch server is given when the agent
	// registers and sends back to ask for env secrets (local.go).
	serverKey string

	mu        sync.Mutex
	boot      Boot
	cfg       VMConfig
	srv       *client.Client
	caps      []string
	panes     map[string]proto.PaneInfo
	projects  map[string]string
	busy      bool
	link      *relay.Link
	linkStop  context.CancelFunc
	linkDone  chan struct{}
	linkKey   string
	gitProxy  string
	helloMu   sync.Mutex
	changed   chan struct{}
	pushes    pusher
	bg        sync.WaitGroup // pushes on their way
	lastHello time.Time
}

// New is an agent with opts, its defaults filled in.
func New(opts Options) (*Agent, error) {
	if opts.BootPath == "" {
		opts.BootPath = DefaultBootPath
	}
	if opts.StateDir == "" {
		opts.StateDir = DefaultStateDir
	}
	if opts.KeyPath == "" {
		opts.KeyPath = filepath.Join(opts.StateDir, "noise.key")
	}
	if opts.User == nil {
		me, err := CurrentUser()
		if err != nil {
			return nil, err
		}
		opts.User = me
	}
	if opts.ServerSocket == "" {
		opts.ServerSocket = serverSocket(opts.User)
	}
	if opts.ServerLog == "" {
		opts.ServerLog = filepath.Join(filepath.Dir(opts.ServerSocket), "server.log")
	}
	if opts.AgentSocket == "" {
		if opts.User.isMe() && os.Geteuid() != 0 {
			opts.AgentSocket = filepath.Join(filepath.Dir(opts.ServerSocket), "cloud-agent.sock")
		} else {
			opts.AgentSocket = DefaultAgentSocket
		}
	}
	if opts.Executable == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		opts.Executable = exe
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return nil, err
	}
	return &Agent{opts: opts, serverKey: hex.EncodeToString(k[:]), panes: map[string]proto.PaneInfo{}, projects: map[string]string{},
		changed: make(chan struct{}, 1)}, nil
}

func (a *Agent) logf(format string, args ...any) { a.opts.Logf(format, args...) }

// bootWait is how often a missing boot.json is looked for again.
var bootWait = 2 * time.Second

// Run is the agent until ctx ends or something it cannot carry on from:
// a token the control plane refuses (ErrUnauthorized), a machine it
// refuses (ErrFatal), or a server that cannot be started.
func (a *Agent) Run(ctx context.Context) error {
	boot, err := a.waitBoot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil // stopped while waiting: not a failure
		}
		return err
	}
	a.mu.Lock()
	a.boot = boot
	a.mu.Unlock()
	a.api = NewAPI(boot.API, boot.MachineToken, a.opts.HTTP)
	if older(proto.Version, boot.ConchVersion) {
		// PROTOCOL §4.1 has the machine install that release first; this
		// agent does not do that yet, so it is said rather than skipped.
		a.logf("cloud: boot.json asks for conch %s and this is %s; the image should carry it (this agent does not upgrade itself yet)", boot.ConchVersion, proto.Version)
	}

	key, made, err := LoadKey(a.opts.KeyPath)
	if err != nil {
		return err
	}
	a.key = key
	woke := wokeFrom(a.opts.StateDir, made)

	// Everything started here has ended by the time Run returns.
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer a.bg.Wait()
	defer wg.Wait()
	defer cancel()
	goWait := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}

	ln, err := listenLocal(a.opts.AgentSocket, a.opts.User)
	if err != nil {
		return fmt.Errorf("the agent's socket: %w", err)
	}
	defer ln.Close()
	goWait(func() { a.serveLocal(ctx, ln) })

	if err := a.connectServer(ctx); errors.Is(err, errServerHeld) {
		a.logf("cloud: %v", err) // serverLoop tries again
	} else if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer func() {
		if c := a.server(); c != nil {
			c.Close()
		}
	}()
	goWait(func() { a.serverLoop(ctx) })

	a.logf("cloud: machine %s, saying hello to %s (woke from %s)", boot.MachineID, boot.API, woke)
	if err := a.hello(ctx, woke); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer a.stopLink()

	errc := make(chan error, 2)
	goWait(func() { errc <- a.reportLoop(ctx) })
	goWait(func() { errc <- a.watchLoop(ctx) })
	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
}

// waitBoot reads boot.json, waiting for it on a machine that has just
// started and has not been given one yet. Anything wrong with a file
// that is there is an error at once.
func (a *Agent) waitBoot(ctx context.Context) (Boot, error) {
	said := false
	for {
		b, err := ReadBoot(a.opts.BootPath)
		if err == nil {
			return b, nil
		}
		if !errors.Is(err, errBootMissing) {
			return Boot{}, err
		}
		if !said {
			a.logf("cloud: waiting for %s", a.opts.BootPath)
			said = true
		}
		select {
		case <-ctx.Done():
			return Boot{}, ctx.Err()
		case <-time.After(bootWait):
		}
	}
}

// hello says hello and puts its answer to use. A refused token is looked
// up again first: a new inject may have replaced it since this process
// read it.
func (a *Agent) hello(ctx context.Context, woke string) error {
	a.helloMu.Lock()
	defer a.helloMu.Unlock()
	h := VMHello{ConchVersion: proto.Version, Capabilities: a.capabilities(), ServerKey: relay.EncodeKey(a.key.Public), WokeFrom: woke}
	var cfg VMConfig
	var err error
	for {
		sent := a.api.Token()
		cfg, err = sayHello(ctx, a.api, h, a.logf)
		// A refused token waits for an inject's new one, however hello
		// came to be said: at start, after a pause, for a new boot.json,
		// or for the control plane's own hello command.
		if !errors.Is(err, ErrUnauthorized) || a.awaitNewToken(ctx, sent) == tokenNone {
			break
		}
	}
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.lastHello = time.Now()
	a.mu.Unlock()
	a.logf("cloud: hello answered: running as %s, relay %s, sleeps after %ds idle", cfg.MachineID, cfg.RelayURL, cfg.Sleep.IdleSeconds)
	a.apply(ctx, cfg)
	a.kick()
	return nil
}

// newToken reads boot.json again and takes its token if it is not the one
// in use, reporting whether it did.
func (a *Agent) newToken() bool {
	b, err := ReadBoot(a.opts.BootPath)
	if err != nil {
		return false
	}
	a.mu.Lock()
	same := b.MachineToken == a.boot.MachineToken
	sameMachine := b.MachineID == a.boot.MachineID
	if !same && sameMachine {
		a.boot = b
	}
	a.mu.Unlock()
	if same || !sameMachine {
		return false
	}
	a.api.SetToken(b.MachineToken)
	a.logf("cloud: boot.json has a new machine token; using it")
	return true
}

// capabilities is what hello reports: the server's own, as it announced
// them once the agent registered, and the contract this agent speaks.
func (a *Agent) capabilities() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	caps := append([]string{}, a.caps...)
	return append(caps, ContractCapability)
}

// apply puts a hello's answer to use: git through the proxy, the relay.
func (a *Agent) apply(ctx context.Context, cfg VMConfig) {
	if cfg.GitProxy != "" {
		a.mu.Lock()
		done := a.gitProxy == cfg.GitProxy
		a.mu.Unlock()
		if !done {
			if err := a.configureGit(ctx, cfg.GitProxy); err != nil {
				a.logf("cloud: git through %s: %v", cfg.GitProxy, err)
			} else {
				a.mu.Lock()
				a.gitProxy = cfg.GitProxy
				a.mu.Unlock()
			}
		}
	}
	a.startLink(ctx, cfg)
}

// kick asks for an activity report: something changed.
func (a *Agent) kick() {
	select {
	case a.changed <- struct{}{}:
	default:
	}
}

// ---- the local conch server ----

// errServerHeld is a conch server that has another cloud agent registered.
var errServerHeld = errors.New("the conch server has another cloud agent registered")

// connectServer starts the server if it is not running, connects, and
// registers this agent with it.
func (a *Agent) connectServer(ctx context.Context) error {
	if err := ensureServer(a.opts.Executable, a.opts.ServerSocket, a.opts.ServerLog, a.opts.User); err != nil {
		return err
	}
	c, err := client.Dial(a.opts.ServerSocket, "conch-cloud-agent")
	if err != nil {
		return fmt.Errorf("connect to the conch server: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	caps := c.Server.Capabilities
	var reg proto.CloudAgentRegisterResult
	if err := c.Call(cctx, proto.MethodCloudAgentRegister, proto.CloudAgentRegisterParams{Socket: a.opts.AgentSocket, Key: a.serverKey}, &reg); err != nil {
		var perr *proto.Error
		if errors.As(err, &perr) && perr.Code == proto.ErrCloudAgentHeld {
			// Something else got there first — a cloud agent that has not
			// gone yet, or a process pretending to be one. Agents must not
			// take secrets from it, so try again until it is gone.
			c.Close()
			return fmt.Errorf("%w (%s); trying again", errServerHeld, perr.Message)
		}
		// A server from before cloud.agent.v1 runs agents without their
		// secrets; everything else works.
		a.logf("cloud: the conch server (%s) did not take this agent (%v): agents start without env secrets", c.Server.Version, err)
	} else {
		caps = reg.Capabilities
	}
	var list proto.PaneList
	if err := c.Call(cctx, proto.MethodPaneList, nil, &list); err != nil {
		c.Close()
		return fmt.Errorf("list the conch server's panes: %w", err)
	}
	names, _ := projectNames(cctx, c)
	a.mu.Lock()
	a.srv, a.caps = c, caps
	a.panes = map[string]proto.PaneInfo{}
	for _, p := range list.Panes {
		a.panes[p.ID] = p
	}
	if names != nil {
		a.projects = names
	}
	a.mu.Unlock()
	a.kick()
	return nil
}

func projectNames(ctx context.Context, c *client.Client) (map[string]string, error) {
	var list proto.ProjectList
	if err := c.Call(ctx, proto.MethodProjectList, nil, &list); err != nil {
		return nil, err
	}
	names := make(map[string]string, len(list.Projects))
	for _, p := range list.Projects {
		names[p.ID] = p.Name
	}
	return names, nil
}

// server is the connection to the conch server, nil while there is none.
func (a *Agent) server() *client.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.srv
}

// serverRetry is the wait before connecting to the server again.
var serverRetry = time.Second

// serverLoop follows the server's events, and connects again — starting
// the server again if it has gone — whenever the connection ends.
func (a *Agent) serverLoop(ctx context.Context) {
	for ctx.Err() == nil {
		c := a.server()
		if c == nil {
			if err := a.connectServer(ctx); err != nil {
				a.logf("cloud: %v", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(serverRetry):
				}
				continue
			}
			c = a.server()
			a.logf("cloud: connected to the conch server again")
			a.bg.Add(1) // Run waits for it, so nothing it does comes after Run
			go func() { defer a.bg.Done(); a.rehelloForCaps(ctx) }()
		}
		a.follow(ctx, c)
		c.Close()
		a.mu.Lock()
		if a.srv == c {
			a.srv = nil
			a.panes = map[string]proto.PaneInfo{}
		}
		a.mu.Unlock()
		a.kick()
	}
}

// rehelloForCaps says hello again when the server's capabilities changed
// — a server reloaded onto a new build — so the control plane knows them.
func (a *Agent) rehelloForCaps(ctx context.Context) {
	a.mu.Lock()
	said := a.lastHello
	a.mu.Unlock()
	if said.IsZero() {
		return // the first hello has not been said; it will say them
	}
	if err := a.hello(ctx, ""); err != nil && ctx.Err() == nil {
		a.logf("cloud: hello after the server came back: %v", err)
	}
}

// follow takes the server's events until the connection ends.
func (a *Agent) follow(ctx context.Context, c *client.Client) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.Events:
			if !ok {
				return
			}
			a.event(ctx, c, msg)
		}
	}
}

// stateFor is an agent's state as the contract names it (AgentActivity):
// conch's blocked is the contract's waiting.
func stateFor(s string) string {
	switch s {
	case proto.AgentWorking:
		return "working"
	case proto.AgentBlocked:
		return "waiting"
	case proto.AgentDone:
		return "done"
	case proto.AgentIdle:
		return "idle"
	}
	return "unknown"
}

// errExit is what Run's caller reads to choose an exit status.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUnauthorized):
		return 3
	case errors.Is(err, ErrFatal):
		return 4
	}
	return 1
}

// older says whether have is an older conch release than want; a want
// that is not a release ("dev", "") is older than nothing.
func older(have, want string) bool {
	if !releaseVersion.MatchString(want) {
		return false
	}
	return update.Compare(have, want) < 0
}

var releaseVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// bootIDPath is the kernel's id for this boot; a variable so a test can
// give one.
var bootIDPath = "/proc/sys/kernel/random/boot_id"

// wokeFrom is what hello's woke_from says for a process just started: a
// first boot when the key was made just now; a wake from disk when the
// kernel has booted since an agent last ran here (a disk sleep restarts
// the machine); otherwise the machine kept running, and the agent alone
// was started again — its processes are where they were, as after a
// memory wake.
func wokeFrom(stateDir string, keyMade bool) string {
	b, _ := os.ReadFile(bootIDPath)
	now := strings.TrimSpace(string(b))
	file := filepath.Join(stateDir, "boot-id")
	last, _ := os.ReadFile(file)
	if now != "" {
		_ = os.MkdirAll(stateDir, 0o700)
		_ = os.WriteFile(file, []byte(now+"\n"), 0o600)
	}
	switch {
	case keyMade:
		return WokeBoot
	case now == "" || strings.TrimSpace(string(last)) != now:
		return WokeDisk
	}
	return WokeMemory
}
