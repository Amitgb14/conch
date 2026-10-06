package phone

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// Gateway serves a phone: it checks who is asking and what they may do,
// and turns each request into calls on the local conch server. It connects
// to that server's socket from outside any pane, so the server doesn't
// scope it — it acts as the person, and the device's permission is its
// only limit. Every route and every socket message is checked against it.
type Gateway struct {
	store *Store
	dial  func() (*client.Client, error)
	// Logf gets one line per request and socket message: what kind and
	// how big, never what a screen or a reply said.
	logf func(format string, args ...any)
	now  func() time.Time

	// machMu guards conns, the client per machine (machines.go). It is its
	// own lock because reaching a machine takes seconds: holding mu for
	// that would stop every request the gateway is serving.
	machMu sync.Mutex
	conns  map[string]*machineConn
	machWG sync.WaitGroup

	mu       sync.Mutex
	c        *client.Client // the local server; a socket has its own
	devices  []Device
	stamp    os.FileInfo
	loaded   bool
	sockets  map[*socket]struct{}
	attempts map[string][]time.Time // pairing attempts, by address
	closed   bool

	quit chan struct{}
	done chan struct{}

	publicOrigin string // see AllowOrigin

	pushAllowed func(endpoint string) bool
	pushClient  *http.Client
	pushQueue   chan pushJob
	// pushWatching is told when the push watcher has learnt what every
	// agent is doing; a test waits for it before changing any.
	pushWatching func()
}

// revokeCheck is how often the devices file is looked at for a device
// revoked from another process (`conch web revoke`), so its open sockets
// close without waiting for them to ask for something.
const revokeCheck = 250 * time.Millisecond

// New makes a gateway over the devices in store, reaching the conch server
// through dial and logging through logf (nil for none). Close it when done.
func New(store *Store, dial func() (*client.Client, error), logf func(format string, args ...any)) *Gateway {
	return newGateway(store, dial, logf, pushOptions{})
}

// pushOptions is where pushes may go and how they are sent; a test's
// push service is on loopback, which knownPushService refuses.
type pushOptions struct {
	allowed  func(endpoint string) bool
	client   *http.Client
	watching func()
}

func newGateway(store *Store, dial func() (*client.Client, error), logf func(format string, args ...any), po pushOptions) *Gateway {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if po.allowed == nil {
		po.allowed = knownPushService
	}
	if po.client == nil {
		po.client = &http.Client{
			Timeout: 15 * time.Second,
			// A push service answers; it doesn't send the laptop elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	g := &Gateway{
		store: store, dial: dial, now: time.Now,
		pushAllowed: po.allowed, pushClient: po.client, pushWatching: po.watching,
		pushQueue: make(chan pushJob, 64),
		logf:      logf,
		sockets:   map[*socket]struct{}{},
		attempts:  map[string][]time.Time{},
		quit:      make(chan struct{}), done: make(chan struct{}),
	}
	go g.watch()
	go g.pushes()
	go g.sendPushes()
	return g
}

// watch closes the sockets of devices that have been revoked.
func (g *Gateway) watch() {
	defer close(g.done)
	t := time.NewTicker(revokeCheck)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			g.dropRevoked()
		case <-g.quit:
			return
		}
	}
}

// Close says goodbye to every socket and stops the gateway.
func (g *Gateway) Close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	socks := make([]*socket, 0, len(g.sockets))
	for s := range g.sockets {
		socks = append(socks, s)
	}
	c := g.c
	g.c = nil
	g.mu.Unlock()

	close(g.quit)
	<-g.done
	g.closeMachines()
	var wg sync.WaitGroup
	for _, s := range socks {
		wg.Add(1)
		go func() { defer wg.Done(); s.bye(ByeShuttingDown) }()
	}
	wg.Wait()
	if c != nil {
		c.Close()
	}
}

// currentDevices is the paired devices as the file has them now. The file
// is read again only when it has changed, so asking on every request costs
// a stat. A file that can't be read pairs nobody.
func (g *Gateway) currentDevices() []Device {
	g.mu.Lock()
	defer g.mu.Unlock()
	info, err := os.Stat(g.store.Path())
	if err != nil {
		g.devices, g.stamp, g.loaded = nil, nil, true
		return nil
	}
	if g.loaded && g.stamp != nil && os.SameFile(g.stamp, info) &&
		g.stamp.ModTime().Equal(info.ModTime()) && g.stamp.Size() == info.Size() {
		return g.devices
	}
	devs, err := g.store.Devices()
	if err != nil {
		g.logf("devices: %v", err)
		devs = nil
	}
	g.devices, g.stamp, g.loaded = devs, info, true
	return devs
}

// deviceByHash finds the device a token hash belongs to. Every device is
// compared, in constant time each.
func (g *Gateway) deviceByHash(hash string) (Device, bool) {
	var found Device
	ok := false
	for _, d := range g.currentDevices() {
		if subtle.ConstantTimeCompare([]byte(d.TokenHash), []byte(hash)) == 1 {
			found, ok = d, true
		}
	}
	return found, ok
}

// dropRevoked closes the sockets whose device is gone.
func (g *Gateway) dropRevoked() {
	g.mu.Lock()
	socks := make([]*socket, 0, len(g.sockets))
	for s := range g.sockets {
		socks = append(socks, s)
	}
	g.mu.Unlock()
	for _, s := range socks {
		if _, ok := g.deviceByHash(s.tokenHash); !ok {
			go s.bye(ByeRevoked)
		}
	}
}

// caller is the part of a server connection the handlers use.
type caller interface {
	Call(ctx context.Context, method string, params, out any) error
	MissingCapabilities(want []string) []string
}

// server is the connection HTTP requests share, made again when the last
// one has ended (the server reloading, or restarted).
func (g *Gateway) server() (*client.Client, *APIError) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, apiErr(CodeServerUnavailable, "the gateway is shutting down")
	}
	if g.c != nil && g.c.Err() == nil {
		return g.c, nil
	}
	c, err := g.dial()
	if err != nil {
		return nil, apiErr(CodeServerUnavailable, "the conch server isn't reachable")
	}
	g.c = c
	go func() { // nothing here follows events; keep the queue empty
		for range c.Events {
		}
	}()
	return c, nil
}

func apiErr(code, message string) *APIError { return &APIError{Code: code, Message: message} }

// fromServer turns a failed call into the error the phone gets. The
// server's own words are passed on for the codes the contract shares with
// it; anything else is the server being unavailable.
func fromServer(err error) *APIError {
	if err == nil {
		return nil
	}
	var perr *proto.Error
	if errors.As(err, &perr) {
		switch perr.Code {
		case proto.ErrNotFound:
			return apiErr(CodeNotFound, perr.Message)
		case proto.ErrBadRequest:
			return apiErr(CodeBadRequest, perr.Message)
		case proto.ErrAgentBlocked:
			return apiErr(CodeAgentBlocked, perr.Message)
		case proto.ErrOutOfScope:
			return apiErr(CodeForbidden, perr.Message)
		case proto.ErrUnknown:
			return apiErr(CodeServerUnavailable, "the conch server is from an older build; reload it with `conch server reload`")
		}
		return apiErr(CodeServerUnavailable, perr.Message)
	}
	return apiErr(CodeServerUnavailable, "the conch server isn't reachable")
}

// csrfToken is what hello hands a device for its state-changing requests.
// It is derived from the token, which a page on another site can't read.
func csrfToken(token string) string {
	sum := sha256.Sum256([]byte("conch-csrf\x00" + token))
	return hex.EncodeToString(sum[:16])
}

// request is one authenticated call.
type request struct {
	r     *http.Request
	dev   Device
	token string
	body  []byte
}

// decode reads the request's JSON body into v.
func (rq *request) decode(v any) *APIError {
	if len(rq.body) == 0 {
		return apiErr(CodeBadRequest, "the request has no body")
	}
	if err := json.Unmarshal(rq.body, v); err != nil {
		return apiErr(CodeBadRequest, "the request isn't valid JSON")
	}
	return nil
}

// route is one row of the contract's HTTP table: what it needs and what it
// does. A nil result is a 204.
type route struct {
	method, path, need string
	handle             func(g *Gateway, rq *request) (any, *APIError)
}

// routes is every route behind a device token. /pair and the socket are
// apart: one makes the token, the other upgrades the connection.
var routes = []route{
	{"GET", "/api/hello", PermView, (*Gateway).hello},
	{"GET", "/api/agents", PermView, (*Gateway).listAgents},
	{"GET", "/api/machines", PermView, (*Gateway).listMachines},
	{"GET", "/api/projects", PermView, (*Gateway).listProjects},
	{"POST", "/api/reply", PermReply, (*Gateway).reply},
	{"POST", "/api/answer", PermReply, (*Gateway).answer},
	{"POST", "/api/task", PermFull, (*Gateway).task},
	{"GET", "/api/push/key", PermView, (*Gateway).pushKey},
	{"POST", "/api/push/subscribe", PermView, (*Gateway).pushSubscribe},
	{"DELETE", "/api/push/subscribe", PermView, (*Gateway).pushUnsubscribe},
	{"GET", "/api/panes", PermView, (*Gateway).listPanes},
	{"POST", "/api/panes", PermFull, (*Gateway).newPane},
	{"POST", "/api/close", PermFull, (*Gateway).closePane},
	{"POST", "/api/rename", PermFull, (*Gateway).renamePane},
}

// SocketPath is where the WebSocket is opened.
const SocketPath = "/api/socket"

// maxBody bounds a request: a reply or a task's prompt, not a file.
const maxBody = 64 << 10

// Handler is the gateway's HTTP handler.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	byPath := map[string][]route{}
	for _, rt := range routes {
		byPath[rt.path] = append(byPath[rt.path], rt)
	}
	for path, rts := range byPath {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { g.serveRoute(w, r, rts) })
	}
	mux.HandleFunc("/pair", g.servePair)
	mux.HandleFunc(SocketPath, g.serveSocket)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		g.fail(w, r, apiErr(CodeNotFound, "no such route"))
	})
	mux.Handle("/", uiHandler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", contentPolicy(r.Host))
		mux.ServeHTTP(w, r)
	})
}

// authenticate finds the device a request comes from, and refuses one
// whose permission is below need.
func (g *Gateway) authenticate(r *http.Request, need string) (Device, string, *APIError) {
	ck, err := r.Cookie(CookieName)
	if err != nil || ck.Value == "" {
		return Device{}, "", apiErr(CodeUnauthorized, "this device isn't paired")
	}
	dev, ok := g.deviceByHash(hashSecret(ck.Value))
	if !ok {
		return Device{}, "", apiErr(CodeUnauthorized, "this device isn't paired, or was revoked")
	}
	if !allows(dev.Permission, need) {
		return Device{}, "", apiErr(CodeForbidden, "this device has the "+dev.Permission+" permission; that needs "+need)
	}
	return dev, ck.Value, nil
}

// AllowOrigin names the address phones open the gateway at when it isn't
// the one the gateway sees in Host: `tailscale serve` in front, with its
// https://….ts.net name. Pages from there are the gateway's own. Call it
// before serving.
func (g *Gateway) AllowOrigin(publicURL string) error {
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("%q is not an http or https address", publicURL)
	}
	g.mu.Lock()
	g.publicOrigin = strings.ToLower(u.Scheme + "://" + u.Host)
	g.mu.Unlock()
	return nil
}

// sameOrigin refuses a request a browser says came from another site. A
// request with no Origin is not from a page at all.
func (g *Gateway) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	g.mu.Lock()
	public := g.publicOrigin
	g.mu.Unlock()
	return strings.EqualFold(u.Host, r.Host) || public != "" && strings.EqualFold(u.Scheme+"://"+u.Host, public)
}

// insecurePage reports whether a request comes from a page on plain http
// somewhere other than this computer. A browser there drops the device's
// cookie — it is Secure — so a pairing would make a device that can never
// get in. Loopback is a secure context to browsers, and keeps it.
func insecurePage(r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

func (g *Gateway) serveRoute(w http.ResponseWriter, r *http.Request, rts []route) {
	i := slices.IndexFunc(rts, func(rt route) bool { return rt.method == r.Method })
	if i < 0 {
		g.failStatus(w, r, http.StatusMethodNotAllowed, apiErr(CodeBadRequest, r.Method+" isn't supported here"))
		return
	}
	rt := rts[i]
	dev, token, aerr := g.authenticate(r, rt.need)
	if aerr != nil {
		g.fail(w, r, aerr)
		return
	}
	rq := &request{r: r, dev: dev, token: token}
	if r.Method != http.MethodGet {
		// A change needs the token hello gave out as well as the cookie a
		// browser sends by itself.
		if !g.sameOrigin(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get(CSRFHeader)), []byte(csrfToken(token))) != 1 {
			g.fail(w, r, apiErr(CodeForbidden, "the "+CSRFHeader+" header is missing or wrong; take it from /api/hello"))
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			g.fail(w, r, apiErr(CodeBadRequest, "the request is too large"))
			return
		}
		rq.body = body
	}
	res, aerr := rt.handle(g, rq)
	if aerr != nil {
		g.fail(w, r, aerr)
		return
	}
	g.respond(w, r, res)
}

// respond writes a result: JSON, or 204 for none. The log line carries the
// size only.
func (g *Gateway) respond(w http.ResponseWriter, r *http.Request, res any) {
	w.Header().Set("Cache-Control", "no-store")
	if res == nil {
		w.WriteHeader(http.StatusNoContent)
		g.logf("%s %s 204", r.Method, r.URL.Path)
		return
	}
	b, err := json.Marshal(res)
	if err != nil {
		g.fail(w, r, apiErr(CodeServerUnavailable, "the answer couldn't be encoded"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
	g.logf("%s %s 200 %dB", r.Method, r.URL.Path, len(b))
}

func (g *Gateway) fail(w http.ResponseWriter, r *http.Request, e *APIError) {
	status, ok := httpStatus[e.Code]
	if !ok {
		status = http.StatusInternalServerError
	}
	g.failStatus(w, r, status, e)
}

func (g *Gateway) failStatus(w http.ResponseWriter, r *http.Request, status int, e *APIError) {
	b, _ := json.Marshal(ErrorBody{Error: e})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
	g.logf("%s %s %d %s", r.Method, r.URL.Path, status, e.Code)
}

// Pairing attempts allowed in pairWindow: from one address, and from
// everywhere — behind a proxy every phone has the proxy's address.
const (
	pairWindow   = time.Minute
	pairPerAddr  = 5
	pairOverall  = 20
	overallKey   = ""
	pairNameMax  = 256
	cookieMaxAge = 400 * 24 * time.Hour // what browsers cap a cookie at
)

// allowPair counts a pairing attempt from addr and says whether it may go
// on; when it may not, how long until it could.
func (g *Gateway) allowPair(addr string) (bool, time.Duration) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	recent := func(key string) []time.Time {
		ts := slices.DeleteFunc(g.attempts[key], func(t time.Time) bool { return now.Sub(t) >= pairWindow })
		if len(ts) == 0 {
			delete(g.attempts, key)
		} else {
			g.attempts[key] = ts
		}
		return ts
	}
	for key := range g.attempts { // forget addresses that went quiet
		recent(key)
	}
	for _, lim := range []struct {
		key string
		max int
	}{{addr, pairPerAddr}, {overallKey, pairOverall}} {
		if ts := g.attempts[lim.key]; len(ts) >= lim.max {
			return false, pairWindow - now.Sub(ts[0])
		}
	}
	g.attempts[addr] = append(g.attempts[addr], now)
	g.attempts[overallKey] = append(g.attempts[overallKey], now)
	return true, 0
}

// servePair exchanges a one-time code for a device token, sent back as a
// cookie a page's script can't read.
func (g *Gateway) servePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		g.failStatus(w, r, http.StatusMethodNotAllowed, apiErr(CodeBadRequest, r.Method+" isn't supported here"))
		return
	}
	if !g.sameOrigin(r) {
		g.fail(w, r, apiErr(CodeForbidden, "pairing has to come from the gateway's own page"))
		return
	}
	if insecurePage(r) {
		// Refused before the code is spent: pairing from the https
		// address still works with it.
		g.fail(w, r, apiErr(CodeBadRequest, "pairing needs HTTPS: over plain http the browser won't keep this device's key. "+
			"Open conch at its https:// address (tailscale serve gives one) and enter the code there"))
		return
	}
	addr, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		addr = r.RemoteAddr
	}
	if addr == overallKey {
		addr = "unknown"
	}
	if ok, wait := g.allowPair(addr); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		g.fail(w, r, apiErr(CodeRateLimited, "too many pairing attempts; try again in a minute"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	var req PairRequest
	if err != nil || json.Unmarshal(body, &req) != nil {
		g.fail(w, r, apiErr(CodeBadRequest, "the request isn't valid JSON"))
		return
	}
	if codeDigits(req.Code) == "" {
		g.fail(w, r, apiErr(CodeBadRequest, "a pairing code is needed"))
		return
	}
	if len(req.DeviceName) > pairNameMax {
		req.DeviceName = req.DeviceName[:pairNameMax]
	}
	dev, token, err := g.store.Redeem(req.Code, req.DeviceName, g.now())
	if errors.Is(err, ErrPairExpired) {
		g.fail(w, r, apiErr(CodePairExpired, err.Error()))
		return
	}
	if err != nil {
		g.logf("pair: %v", err)
		g.fail(w, r, apiErr(CodeServerUnavailable, "the device couldn't be saved"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
		MaxAge: int(cookieMaxAge / time.Second),
	})
	g.respond(w, r, PairResponse{DeviceID: dev.ID, Permission: dev.Permission})
}

// callTimeout bounds a call on the server; taskTimeout one that makes a
// worktree first.
const (
	callTimeout = 10 * time.Second
	taskTimeout = 60 * time.Second
)

// call makes one call on this computer's connection, for the routes that
// are about this computer (projects, pairing).
func (g *Gateway) call(rq *request, timeout time.Duration, method string, params, out any) *APIError {
	c, aerr := g.server()
	if aerr != nil {
		return aerr
	}
	return g.callOn(rq, c, timeout, method, params, out)
}

// callOn makes one call on a named machine's connection: what a route that
// acts on a pane uses, having resolved the pane's machine (paneOn).
func (g *Gateway) callOn(rq *request, c *client.Client, timeout time.Duration, method string, params, out any) *APIError {
	ctx, cancel := context.WithTimeout(rq.r.Context(), timeout)
	defer cancel()
	return fromServer(c.Call(ctx, method, params, out))
}

func (g *Gateway) hello(rq *request) (any, *APIError) {
	// The machines come with hello so the app can draw them before asking
	// for anything else; a machine still being reached says so, and the
	// socket says again when it answers.
	return Hello{APIVersion: APIVersion, ConchVersion: proto.Version, DeviceID: rq.dev.ID,
		Permission: rq.dev.Permission, CSRFToken: csrfToken(rq.token),
		Machines: machineList(g.machinesFor(rq.dev), nil)}, nil
}

func (g *Gateway) listAgents(rq *request) (any, *APIError) {
	ctx, cancel := context.WithTimeout(rq.r.Context(), callTimeout)
	defer cancel()
	agents, machines := g.everyAgent(ctx, rq.dev)
	if len(machines) > 0 && machines[0].State != MachineOnline && len(agents) == 0 {
		// Not even this computer answered: that is the gateway being
		// unavailable, not an empty list.
		return nil, apiErr(CodeServerUnavailable, "the conch server isn't reachable")
	}
	return AgentList{Agents: agents}, nil
}

func (g *Gateway) listMachines(rq *request) (any, *APIError) {
	ctx, cancel := context.WithTimeout(rq.r.Context(), callTimeout)
	defer cancel()
	_, machines := g.everyAgent(ctx, rq.dev)
	return MachineList{Machines: machines}, nil
}

func (g *Gateway) listProjects(rq *request) (any, *APIError) {
	// A project ID is a server's own, so the projects are the named
	// machine's; ?machine= left out is this computer, as it always was.
	c, _, aerr := g.machineFor(rq.dev, rq.r.URL.Query().Get("machine"))
	if aerr != nil {
		return nil, aerr
	}
	var list proto.ProjectList
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodProjectList, nil, &list); aerr != nil {
		return nil, aerr
	}
	out := ProjectList{Projects: []Project{}}
	for _, p := range list.Projects {
		out.Projects = append(out.Projects, Project{ID: p.ID, Name: p.Name, Path: p.Path, Base: p.Base})
	}
	return out, nil
}

// reply is agent.prompt: the server refuses it while the agent waits on a
// question, and that refusal is passed on.
func (g *Gateway) reply(rq *request) (any, *APIError) {
	var req ReplyRequest
	if aerr := rq.decode(&req); aerr != nil {
		return nil, aerr
	}
	if strings.TrimSpace(req.Text) == "" {
		return nil, apiErr(CodeBadRequest, "a reply needs some text")
	}
	c, pane, id, aerr := g.paneOn(rq.dev, req.Pane)
	if aerr != nil {
		return nil, aerr
	}
	// Without agent.prompt there is nothing that refuses a reply typed
	// onto a question, so there is no safe way to send one. Asked of the
	// machine the pane is on: a phone reaches machines, and each has its
	// own conch — "reload the server" was the wrong thing to tell
	// somebody about a machine they are not sitting at.
	if aerr := g.lacks(machineOf(id), c, proto.CapAgentPrompt); aerr != nil {
		return nil, aerr
	}
	var res proto.AgentPromptResult
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodAgentPrompt, proto.AgentPromptParams{ID: pane, Text: req.Text}, &res); aerr != nil {
		return nil, aerr
	}
	// The pane comes back as the phone addressed it, not as that server
	// calls it: the app sends this back.
	return ReplyResponse{Pane: id, Agent: res.Agent, Turn: res.Turn}, nil
}

// answer picks one of a waiting agent's choices. It is refused before
// anything is typed unless the agent is still waiting on the very question
// the phone showed, so a tap meant for one question can't answer the next.
func (g *Gateway) answer(rq *request) (any, *APIError) {
	var req AnswerRequest
	if aerr := rq.decode(&req); aerr != nil {
		return nil, aerr
	}
	if req.QuestionID == "" || req.Choice == "" {
		return nil, apiErr(CodeBadRequest, "an answer needs question_id and choice")
	}
	c, pane, id, aerr := g.paneOn(rq.dev, req.Pane)
	if aerr != nil {
		return nil, aerr
	}
	var list proto.PaneList
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodPaneList, nil, &list); aerr != nil {
		return nil, aerr
	}
	i := slices.IndexFunc(list.Panes, func(p proto.PaneInfo) bool { return p.ID == pane })
	if i < 0 {
		return nil, apiErr(CodeNotFound, "no pane "+req.Pane)
	}
	info := list.Panes[i]
	if info.State != proto.PaneRunning || info.Agent == nil || info.Agent.State != proto.AgentBlocked {
		return nil, apiErr(CodeNotWaiting, "the agent in "+req.Pane+" isn't waiting for an answer; nothing was sent")
	}
	// The screen is read once, and both the question and the keys come
	// from that one reading.
	var screen proto.PaneReadResult
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodPaneRead, proto.PaneRef{ID: pane}, &screen); aerr != nil {
		return nil, aerr
	}
	q := readQuestion(info, screen.Lines)
	if subtle.ConstantTimeCompare([]byte(q.ID), []byte(req.QuestionID)) != 1 {
		return nil, apiErr(CodeQuestionChanged, "the agent in "+req.Pane+" is asking something else now; nothing was sent")
	}
	if len(q.Choices) == 0 {
		return nil, apiErr(CodeNoChoices, "the choices couldn't be read off the screen; answer in the terminal")
	}
	keys, ok := choiceKeys(screen.Lines, req.Choice)
	if !ok {
		return nil, apiErr(CodeBadRequest, "the question has no choice "+strconv.Quote(req.Choice))
	}
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: pane, Keys: keys}, nil); aerr != nil {
		return nil, aerr
	}
	return AnswerResponse{Pane: id, Sent: true}, nil
}

// task is task.create, and on a server that drops the name, pane.rename
// afterwards — as `conch task -name` does.
func (g *Gateway) task(rq *request) (any, *APIError) {
	var req TaskRequest
	if aerr := rq.decode(&req); aerr != nil {
		return nil, aerr
	}
	if req.Project == "" || strings.TrimSpace(req.Prompt) == "" {
		return nil, apiErr(CodeBadRequest, "a task needs project and prompt")
	}
	c, machine, aerr := g.machineFor(rq.dev, req.Machine)
	if aerr != nil {
		return nil, aerr
	}
	var info proto.PaneInfo
	aerr = g.callOn(rq, c, taskTimeout, proto.MethodTaskCreate, proto.TaskCreateParams{
		ProjectID: req.Project, Prompt: req.Prompt, Agent: req.Agent, Name: req.Name, Cols: 120, Rows: 40}, &info)
	if aerr != nil {
		return nil, aerr
	}
	if req.Name != "" && len(c.MissingCapabilities([]string{proto.CapTaskName})) > 0 {
		// The task runs either way, so a name that did not take is not
		// worth failing it for; the machine list says that machine's conch
		// is behind (Machine.Behind), which is where the person reads it.
		_ = g.callOn(rq, c, callTimeout, proto.MethodPaneRename, proto.PaneRenameParams{ID: info.ID, Name: req.Name}, nil)
	}
	return TaskResponse{Pane: composePaneID(machine, info.ID), Worktree: info.Cwd, Branch: info.Branch}, nil
}

func (g *Gateway) pushKey(rq *request) (any, *APIError) {
	key, err := g.store.VAPIDPublicKey()
	if err != nil {
		g.logf("push key: %v", err)
		return nil, apiErr(CodeServerUnavailable, "the push key couldn't be made")
	}
	return PushKey{VAPIDPublicKey: key}, nil
}

// pushEvents are the transitions a device can ask to be told about.
var pushEvents = []string{StateWaiting, StateDone}

// pushSubscribe stores a device's push subscription. Sending is not built
// yet (phase 4 of the plan); this keeps what it will send to.
func (g *Gateway) pushSubscribe(rq *request) (any, *APIError) {
	var sub PushSubscription
	if aerr := rq.decode(&sub); aerr != nil {
		return nil, aerr
	}
	// Pushes are POSTed to this address from the laptop, so it has to be
	// a push service's, and nothing else.
	if !g.pushAllowed(sub.Endpoint) {
		return nil, apiErr(CodeBadRequest, "endpoint has to be a browser push service's https address")
	}
	if sub.Keys.P256dh == "" || sub.Keys.Auth == "" {
		return nil, apiErr(CodeBadRequest, "a subscription needs keys.p256dh and keys.auth")
	}
	if len(sub.On) == 0 {
		sub.On = []string{StateWaiting}
	}
	for _, on := range sub.On {
		if !slices.Contains(pushEvents, on) {
			return nil, apiErr(CodeBadRequest, "on takes waiting and done")
		}
	}
	if err := g.store.Subscribe(rq.dev.ID, sub); err != nil {
		g.logf("push subscribe: %v", err)
		return nil, apiErr(CodeServerUnavailable, "the subscription couldn't be saved")
	}
	return nil, nil
}

func (g *Gateway) pushUnsubscribe(rq *request) (any, *APIError) {
	var sub PushSubscription
	if aerr := rq.decode(&sub); aerr != nil {
		return nil, aerr
	}
	if sub.Endpoint == "" {
		return nil, apiErr(CodeBadRequest, "endpoint is needed")
	}
	if err := g.store.Unsubscribe(rq.dev.ID, sub.Endpoint); err != nil {
		g.logf("push unsubscribe: %v", err)
		return nil, apiErr(CodeServerUnavailable, "the subscription couldn't be removed")
	}
	return nil, nil
}

func (g *Gateway) listPanes(rq *request) (any, *APIError) {
	ctx, cancel := context.WithTimeout(rq.r.Context(), callTimeout)
	defer cancel()
	panes, machines := g.everyPane(ctx, rq.dev)
	if len(machines) > 0 && machines[0].State != MachineOnline && len(panes) == 0 {
		return nil, apiErr(CodeServerUnavailable, "the conch server isn't reachable")
	}
	return PaneList{Panes: panes}, nil
}

// newPane is pane.create: a login shell, or an agent launched the way
// conch launches it, in a project's folder or the home folder.
func (g *Gateway) newPane(rq *request) (any, *APIError) {
	var req NewPaneRequest
	if aerr := rq.decode(&req); aerr != nil {
		return nil, aerr
	}
	// A name of a pane ID's shape would stand for another pane wherever
	// panes are named; pane.create doesn't refuse one, so this does.
	if proto.IsPaneID(req.Name) {
		return nil, apiErr(CodeBadRequest, "a pane can't be named like a pane ID")
	}
	c, machine, aerr := g.machineFor(rq.dev, req.Machine)
	if aerr != nil {
		return nil, aerr
	}
	params := proto.PaneCreateParams{Name: req.Name, Cols: 120, Rows: 40}
	switch req.Kind {
	case KindTerminal:
		if req.Agent != "" || req.Prompt != "" {
			return nil, apiErr(CodeBadRequest, "a terminal takes no agent or prompt")
		}
	case KindAgent:
		if req.Agent == "" {
			return nil, apiErr(CodeBadRequest, "an agent pane needs agent")
		}
		params.Agent, params.Prompt = req.Agent, req.Prompt
	default:
		return nil, apiErr(CodeBadRequest, "kind is terminal or agent")
	}
	if req.Project == "" {
		params.NoProject = true // the home folder, in no project, as the TUI's machine-level panes
	} else {
		// A project ID is the machine's own, so it is looked up there.
		var list proto.ProjectList
		if aerr := g.callOn(rq, c, callTimeout, proto.MethodProjectList, nil, &list); aerr != nil {
			return nil, aerr
		}
		i := slices.IndexFunc(list.Projects, func(p proto.ProjectInfo) bool { return p.ID == req.Project })
		if i < 0 {
			return nil, apiErr(CodeNotFound, "no project "+req.Project)
		}
		params.Cwd = list.Projects[i].Path
	}
	var info proto.PaneInfo
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodPaneCreate, params, &info); aerr != nil {
		return nil, aerr
	}
	return NewPaneResponse{Pane: composePaneID(machine, info.ID)}, nil
}

func (g *Gateway) closePane(rq *request) (any, *APIError) {
	var req CloseRequest
	if aerr := rq.decode(&req); aerr != nil {
		return nil, aerr
	}
	c, pane, id, aerr := g.paneOn(rq.dev, req.Pane)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodPaneClose, proto.PaneRef{ID: pane}, nil); aerr != nil {
		return nil, aerr
	}
	return CloseResponse{Pane: id, Closed: true}, nil
}

func (g *Gateway) renamePane(rq *request) (any, *APIError) {
	var req RenameRequest
	if aerr := rq.decode(&req); aerr != nil {
		return nil, aerr
	}
	c, pane, id, aerr := g.paneOn(rq.dev, req.Pane)
	if aerr != nil {
		return nil, aerr
	}
	req.Name = strings.TrimSpace(req.Name)
	if proto.IsPaneID(req.Name) {
		return nil, apiErr(CodeBadRequest, "a pane can't be named like a pane ID")
	}
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodPaneRename, proto.PaneRenameParams{ID: pane, Name: req.Name}, nil); aerr != nil {
		return nil, aerr
	}
	req.Pane = id // as the phone should address it from now on
	return req, nil
}
