// Package server is the conch daemon: it owns panes and serves the protocol
// on a unix socket so clients can come and go without killing agents.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/pane"
	"github.com/Amitgb14/conch/internal/proto"
)

// frameInterval caps how often frames are pushed per subscription.
const frameInterval = 33 * time.Millisecond

// Server owns all panes on this machine.
type Server struct {
	sockPath  string
	configDir string
	started   time.Time // kept across reloads, for uptime
	loaded    time.Time // this program's start

	adapters  adapter.Registry
	manifests map[string]*detect.Manifest
	projects  *projectManager
	runs      *runLog // agents running in panes, for resuming after a restart
	uploads   *uploads

	limitsMu sync.Mutex
	limits   map[string]proto.PlanLimits // by agent

	mu      sync.Mutex
	ln      net.Listener
	panes   map[string]*entry
	order   []string
	nextID  int
	clients map[*client]struct{}

	quit     chan struct{}
	quitOnce sync.Once
}

type client struct {
	conn *proto.Conn

	mu   sync.Mutex
	subs map[string]*subscription // by pane ID
}

// New creates a server that will listen on sockPath and keep integration
// files and agent manifest overrides under configDir.
func New(sockPath, configDir string) *Server {
	if abs, err := filepath.Abs(sockPath); err == nil {
		sockPath = abs // panes and hooks may run in other directories
	}
	s := &Server{
		sockPath:  sockPath,
		configDir: configDir,
		started:   time.Now(),
		loaded:    time.Now(),
		panes:     map[string]*entry{},
		clients:   map[*client]struct{}{},
		quit:      make(chan struct{}),
	}
	s.projects = newProjectManager(s, configDir)
	s.uploads = newUploads(configDir)
	// A reload keeps the panes, so their runs are not interrupted.
	s.runs = loadRunLog(configDir, os.Getenv(reloadStateEnv) != "")
	return s
}

// ErrAlreadyRunning is returned when another server owns the socket.
var ErrAlreadyRunning = errors.New("conch server is already running")

// Run listens and serves until Stop is called or the process is signalled.
func (s *Server) Run() error {
	// sockaddr_un.sun_path is 104 bytes on macOS (108 on Linux), NUL included.
	if len(s.sockPath) > 103 {
		return fmt.Errorf("socket path is %d bytes, over the 103-byte unix socket limit: %s (set CONCH_SOCKET to a shorter path)",
			len(s.sockPath), s.sockPath)
	}
	if err := os.MkdirAll(filepath.Dir(s.sockPath), 0o700); err != nil {
		return err
	}
	reloaded, err := takeReloadState()
	if err != nil {
		return fmt.Errorf("reload state: %w", err)
	}
	if reloaded == nil {
		if c, err := net.Dial("unix", s.sockPath); err == nil {
			c.Close()
			return ErrAlreadyRunning
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if s.adapters, err = adapter.New(exe, s.configDir); err != nil {
		return err
	}
	var errs []error
	s.manifests, errs = detect.LoadManifests(detect.ManifestDir(s.configDir))
	for _, err := range errs {
		log.Printf("agent manifest ignored: %v", err)
	}

	// Loading runs git per project; don't make clients wait for it.
	go func() {
		s.projects.load()
		s.projects.run()
	}()
	go s.uploads.run(s.quit)

	var ln net.Listener
	if reloaded != nil {
		lf := os.NewFile(uintptr(reloaded.ListenerFD), "listener")
		ln, err = net.FileListener(lf)
		lf.Close()
		if err != nil {
			return fmt.Errorf("reload: listener: %w", err)
		}
		if ul, ok := ln.(*net.UnixListener); ok {
			ul.SetUnlinkOnClose(true)
		}
		s.adopt(reloaded)
	} else {
		_ = os.Remove(s.sockPath) // stale socket from a crashed server
		if ln, err = net.Listen("unix", s.sockPath); err != nil {
			return err
		}
		if err := os.Chmod(s.sockPath, 0o600); err != nil {
			ln.Close()
			return err
		}
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	log.Printf("conch server %s listening on %s (pid %d)", proto.Version, s.sockPath, os.Getpid())

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	signal.Ignore(syscall.SIGHUP) // survive the launching terminal closing
	go func() {
		select {
		case sig := <-sigs:
			log.Printf("received %s, shutting down", sig)
			s.Stop()
		case <-s.quit:
		}
	}()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-s.quit:
				default:
					log.Printf("accept: %v", err)
					s.Stop()
				}
				return
			}
			go s.serve(conn)
		}
	}()

	<-s.quit
	s.shutdown()
	return nil
}

// Stop begins shutdown; Run returns once panes are closed.
func (s *Server) Stop() {
	s.quitOnce.Do(func() { close(s.quit) })
}

func (s *Server) shutdown() {
	s.mu.Lock()
	if s.ln != nil {
		// Closing a unix listener also unlinks its socket file. Do it before
		// closing panes (which can take seconds) so a replacement server can
		// start meanwhile without us deleting its socket afterwards.
		s.ln.Close()
	}
	entries := make([]*entry, 0, len(s.panes))
	for _, e := range s.panes {
		entries = append(entries, e)
	}
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Add(1)
		go func() { defer wg.Done(); e.p.Close() }()
	}
	wg.Wait()
	for _, c := range clients {
		c.conn.Close()
	}
	log.Printf("server stopped")
}

func (s *Server) serve(nc net.Conn) {
	c := &client{conn: proto.NewConn(nc), subs: map[string]*subscription{}}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
		s.unsubscribeAll(c)
		s.uploads.dropClient(c)
		c.conn.Close()
	}()

	for {
		msg, err := c.conn.Read()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Printf("client read: %v", err)
			}
			return
		}
		if msg.Method == "" {
			continue
		}
		// Requests that run git or start processes can take a while; run
		// them aside so keystrokes on this connection are never held up.
		if slowMethods[msg.Method] && msg.ID != "" {
			go s.handle(c, msg)
			continue
		}
		if !s.handle(c, msg) {
			return
		}
	}
}

var slowMethods = map[string]bool{
	proto.MethodProjectAdd: true, proto.MethodProjectChanges: true, proto.MethodProjectDiff: true,
	proto.MethodWorktreeAdd: true, proto.MethodWorktreeRemove: true, proto.MethodTaskCreate: true,
	proto.MethodPaneCreate: true, proto.MethodPaneClose: true,
	proto.MethodAgentStatus: true, proto.MethodAgentInstall: true,
	proto.MethodProjectCreate: true, proto.MethodFSList: true, proto.MethodFSMkdir: true,
	proto.MethodShellThemes: true, proto.MethodAgentSetup: true, proto.MethodWorktreeFiles: true,
	proto.MethodProjectFiles: true, proto.MethodSessionList: true, proto.MethodSessionResume: true, proto.MethodSessionDelete: true,
	proto.MethodSessionSearch: true, proto.MethodSessionShare: true, proto.MethodFSUpload: true,
	proto.MethodBranchCommit: true, proto.MethodBranchPush: true, proto.MethodBranchPR: true,
	proto.MethodBranchMerge: true, proto.MethodBranchDiscard: true,
}

// handle dispatches one request and writes the reply. It reports false
// when the connection is broken.
func (s *Server) handle(c *client, msg proto.Message) bool {
	result, perr := s.dispatch(c, msg)
	if msg.ID == "" { // notification: no reply
		if perr != nil {
			log.Printf("%s: %v", msg.Method, perr)
		}
		return true
	}
	resp := proto.Message{ID: msg.ID}
	if perr != nil {
		resp.Error = perr
	} else {
		resp.Result = proto.Marshal(result)
		if resp.Result == nil {
			resp.Result = json.RawMessage("{}")
		}
	}
	if err := c.conn.Write(resp); err != nil {
		return false
	}
	if msg.Method == proto.MethodServerStop {
		s.Stop()
	}
	if msg.Method == proto.MethodServerReload && perr == nil {
		if r, ok := result.(proto.ServerReloadResult); ok {
			go s.reload(r.Binary)
		}
	}
	return true
}

func decode[T any](msg proto.Message) (T, *proto.Error) {
	var v T
	if len(msg.Params) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(msg.Params, &v); err != nil {
		return v, proto.Errorf(proto.ErrBadRequest, "invalid params for %s: %v", msg.Method, err)
	}
	return v, nil
}

// withPane decodes params naming a pane and looks the pane up.
func withPane[T any](s *Server, msg proto.Message, id func(T) string) (T, *entry, *proto.Error) {
	params, perr := decode[T](msg)
	if perr != nil {
		return params, nil, perr
	}
	e, perr := s.get(id(params))
	return params, e, perr
}

func (s *Server) dispatch(c *client, msg proto.Message) (any, *proto.Error) {
	switch msg.Method {
	case proto.MethodHello:
		hp, perr := decode[proto.HelloParams](msg)
		if perr != nil {
			return nil, perr
		}
		if hp.Protocol != 0 && hp.Protocol != proto.ProtocolVersion {
			return nil, proto.Errorf(proto.ErrBadRequest,
				"protocol mismatch: client %d, server %d", hp.Protocol, proto.ProtocolVersion)
		}
		host, _ := os.Hostname()
		home, _ := os.UserHomeDir()
		return proto.HelloResult{
			Version:      proto.Version,
			Protocol:     proto.ProtocolVersion,
			Capabilities: proto.Capabilities,
			PID:          os.Getpid(),
			Started:      s.started,
			LoadedAt:     s.loaded,
			Build:        buildinfo.Build(),
			BuildID:      buildinfo.ID(),
			Platform:     buildinfo.Platform(),
			Hostname:     host,
			Home:         home,
		}, nil

	case proto.MethodPing:
		return map[string]string{"type": "pong"}, nil

	case proto.MethodServerStop:
		return nil, nil // Stop runs after the reply is written.

	case proto.MethodServerReload:
		rp, perr := decode[proto.ServerReloadParams](msg)
		if perr != nil {
			return nil, perr
		}
		bin, perr := reloadBinary(rp.Binary)
		if perr != nil {
			return nil, perr
		}
		return proto.ServerReloadResult{Binary: bin}, nil // the reload runs after the reply

	case proto.MethodPaneList:
		return proto.PaneList{Panes: s.list()}, nil

	case proto.MethodPaneCreate:
		cp, perr := decode[proto.PaneCreateParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.create(cp)

	case proto.MethodPaneClose:
		ref, perr := decode[proto.PaneRef](msg)
		if perr != nil {
			return nil, perr
		}
		return nil, s.close(ref.ID)

	case proto.MethodPaneResize:
		rp, e, perr := withPane(s, msg, func(p proto.PaneResizeParams) string { return p.ID })
		if perr != nil {
			return nil, perr
		}
		if err := e.p.Resize(rp.Cols, rp.Rows); err != nil {
			return nil, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		return nil, nil

	case proto.MethodPaneSendText:
		tp, e, perr := withPane(s, msg, func(p proto.PaneSendTextParams) string { return p.ID })
		if perr != nil {
			return nil, perr
		}
		if err := e.p.SendText(tp.Text, tp.Paste); err != nil {
			return nil, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		s.userInput(e)
		return nil, nil

	case proto.MethodPaneSendKeys:
		kp, e, perr := withPane(s, msg, func(p proto.PaneSendKeysParams) string { return p.ID })
		if perr != nil {
			return nil, perr
		}
		if err := e.p.SendKeys(kp.Keys); err != nil {
			return nil, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		s.userInput(e)
		return nil, nil

	case proto.MethodPaneRead:
		_, e, perr := withPane(s, msg, func(p proto.PaneRef) string { return p.ID })
		if perr != nil {
			return nil, perr
		}
		return proto.PaneReadResult{Lines: e.p.PlainLines()}, nil

	case proto.MethodPaneSubscribe:
		_, e, perr := withPane(s, msg, func(p proto.PaneRef) string { return p.ID })
		if perr != nil {
			return nil, perr
		}
		s.subscribe(c, e)
		return nil, nil

	case proto.MethodPaneUnsubscribe:
		ref, perr := decode[proto.PaneRef](msg)
		if perr != nil {
			return nil, perr
		}
		s.unsubscribe(c, ref.ID)
		return nil, nil

	case proto.MethodPaneMarkSeen:
		_, e, perr := withPane(s, msg, func(p proto.PaneRef) string { return p.ID })
		if perr != nil {
			return nil, perr
		}
		s.markSeen(e)
		return nil, nil

	case proto.MethodAgentReport:
		rp, e, perr := withPane(s, msg, func(p proto.AgentReportParams) string { return p.ID })
		if perr != nil {
			return nil, perr
		}
		s.report(e, rp)
		return nil, nil

	case proto.MethodPaneRedraw:
		_, e, perr := withPane(s, msg, func(p proto.PaneRef) string { return p.ID })
		if perr != nil {
			return nil, perr
		}
		if err := e.p.Repaint(); err != nil {
			return nil, &proto.Error{Code: proto.ErrInternal, Message: err.Error()}
		}
		return e.info(), nil

	case proto.MethodPaneRename:
		rp, e, perr := withPane(s, msg, func(p proto.PaneRenameParams) string { return p.ID })
		if perr != nil {
			return nil, perr
		}
		s.rename(e, rp.Name)
		return e.info(), nil

	case proto.MethodPaneScroll:
		sp, e, perr := withPane(s, msg, func(p proto.PaneScrollParams) string { return p.ID })
		if perr != nil {
			return nil, perr
		}
		s.scroll(c, e, sp.Offset)
		return nil, nil

	case proto.MethodPaneSendMouse:
		mp, e, perr := withPane(s, msg, func(p proto.PaneSendMouseParams) string { return p.ID })
		if perr != nil {
			return nil, perr
		}
		if err := e.p.SendMouse(mp); err != nil {
			return nil, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		return nil, nil

	case proto.MethodProjectList:
		return proto.ProjectList{Projects: s.projects.list()}, nil

	case proto.MethodProjectAdd:
		ap, perr := decode[proto.ProjectAddParams](msg)
		if perr != nil {
			return nil, perr
		}
		p, err := s.projects.add(ap.Path, true)
		if err != nil {
			return nil, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		return p.snapshot(), nil

	case proto.MethodProjectCreate:
		cp, perr := decode[proto.ProjectCreateParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.createProject(cp)

	case proto.MethodShellThemes:
		return s.shellThemes(), nil

	case proto.MethodFSList:
		lp, perr := decode[proto.FSListParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.listDir(lp)

	case proto.MethodFSMkdir:
		mp, perr := decode[proto.FSMkdirParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.mkdir(mp)

	case proto.MethodFSUpload:
		up, perr := decode[proto.FSUploadParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.uploads.chunk(c, up)

	case proto.MethodProjectRemove:
		ref, perr := decode[proto.ProjectRef](msg)
		if perr != nil {
			return nil, perr
		}
		return nil, s.projects.remove(ref.ID)

	case proto.MethodProjectRefresh:
		ref, perr := decode[proto.ProjectRef](msg)
		if perr != nil {
			return nil, perr
		}
		p, perr := s.projects.get(ref.ID)
		if perr != nil {
			return nil, perr
		}
		s.projects.request(p)
		s.projects.requestPRs(p)
		return nil, nil

	case proto.MethodProjectChanges:
		cp, perr := decode[proto.ChangesParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.projects.changes(cp)

	case proto.MethodProjectDiff:
		dp, perr := decode[proto.DiffParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.projects.diff(dp)

	case proto.MethodWorktreeAdd:
		wp, perr := decode[proto.WorktreeAddParams](msg)
		if perr != nil {
			return nil, perr
		}
		p, perr := s.projects.get(wp.ProjectID)
		if perr != nil {
			return nil, perr
		}
		path, copied, perr := s.projects.addWorktree(p, wp.Branch, wp.Base)
		return proto.WorktreeResult{Path: path, Copied: copied}, perr

	case proto.MethodWorktreeRemove:
		wp, perr := decode[proto.WorktreeRemoveParams](msg)
		if perr != nil {
			return nil, perr
		}
		return nil, s.projects.removeWorktree(wp)

	case proto.MethodTaskCreate:
		tp, perr := decode[proto.TaskCreateParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.createTask(tp)

	case proto.MethodAgentStatus:
		var res proto.AgentStatusResult
		avs := make([]adapter.Availability, len(s.adapters))
		var wg sync.WaitGroup
		for i, ad := range s.adapters {
			wg.Add(1)
			go func() { // each check starts a login shell; run them together
				defer wg.Done()
				avs[i] = ad.Detect(context.Background(), config.DefaultShell())
			}()
		}
		wg.Wait()
		for i, ad := range s.adapters {
			res.Agents = append(res.Agents, proto.AgentAvailability{Name: ad.Name(), Label: ad.Label(),
				Installed: avs[i].Installed, Path: avs[i].Path, Version: avs[i].Version})
		}
		return res, nil

	case proto.MethodAgentInstall:
		ip, perr := decode[proto.AgentInstallParams](msg)
		if perr != nil {
			return nil, perr
		}
		ad, ok := s.adapters.Get(ip.Agent)
		if !ok {
			return nil, proto.Errorf(proto.ErrBadRequest, "conch can't install %q", ip.Agent)
		}
		home, _ := os.UserHomeDir()
		return s.create(proto.PaneCreateParams{
			Name:    "install " + ip.Agent,
			Command: []string{config.DefaultShell(), "-lc", ad.InstallScript()},
			Cwd:     home,
			Cols:    ip.Cols,
			Rows:    ip.Rows,
		})

	case proto.MethodAgentExplain:
		_, e, perr := withPane(s, msg, func(p proto.PaneRef) string { return p.ID })
		if perr != nil {
			return nil, perr
		}
		s.observe(e) // explain the current screen, not the last tick's
		return e.explain(), nil

	case proto.MethodAgentLimits:
		return s.allLimits(), nil

	case proto.MethodSessionList:
		lp, perr := decode[proto.SessionListParams](msg)
		if perr != nil {
			return nil, perr
		}
		lp.Dir = realDir(lp.Dir)
		return s.listSessions(lp)

	case proto.MethodSessionResume:
		rp, perr := decode[proto.SessionRef](msg)
		if perr != nil {
			return nil, perr
		}
		rp.Dir = realDir(rp.Dir)
		return s.resumeSession(rp)

	case proto.MethodSessionDelete:
		rp, perr := decode[proto.SessionRef](msg)
		if perr != nil {
			return nil, perr
		}
		rp.Dir = realDir(rp.Dir)
		return nil, s.deleteSession(rp)

	case proto.MethodAgentBroadcast:
		bp, perr := decode[proto.AgentBroadcastParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.broadcastMessage(bp)

	case proto.MethodSessionSearch:
		sp, perr := decode[proto.SessionSearchParams](msg)
		if perr != nil {
			return nil, perr
		}
		sp.Dir = realDir(sp.Dir)
		return s.searchSessions(sp)

	case proto.MethodSessionShare:
		sp, perr := decode[proto.SessionShareParams](msg)
		if perr != nil {
			return nil, perr
		}
		sp.Dir = realDir(sp.Dir)
		return s.shareSession(sp)

	case proto.MethodSessionDismiss:
		rp, perr := decode[proto.SessionRef](msg)
		if perr != nil {
			return nil, perr
		}
		rp.Dir = realDir(rp.Dir)
		s.runs.resolve(rp.Agent, rp.ID, rp.Dir)
		return nil, nil

	case proto.MethodAgentSetup:
		ap, perr := decode[proto.AgentSetupParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.agentSetup(ap)

	case proto.MethodProjectFiles:
		fp, perr := decode[proto.ProjectFilesParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.projects.setLocalFiles(fp)

	case proto.MethodBranchCommit:
		cp, perr := decode[proto.BranchCommitParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.projects.commitBranch(cp)

	case proto.MethodBranchPush:
		br, perr := decode[proto.BranchRef](msg)
		if perr != nil {
			return nil, perr
		}
		return nil, s.projects.pushBranch(br)

	case proto.MethodBranchPR:
		pp, perr := decode[proto.BranchPRParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.projects.openPR(pp)

	case proto.MethodBranchMerge:
		mp, perr := decode[proto.BranchMergeParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.projects.mergeBranch(mp)

	case proto.MethodBranchDiscard:
		dp, perr := decode[proto.BranchDiscardParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.projects.discardBranch(dp)

	case proto.MethodWorktreeFiles:
		wp, perr := decode[proto.WorktreeFilesParams](msg)
		if perr != nil {
			return nil, perr
		}
		return s.projects.copyFiles(wp)
	}
	return nil, proto.Errorf(proto.ErrUnknown, "unknown method %q", msg.Method)
}

func (s *Server) list() []proto.PaneInfo {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.order))
	for _, id := range s.order {
		entries = append(entries, s.panes[id])
	}
	s.mu.Unlock()
	infos := make([]proto.PaneInfo, 0, len(entries))
	for _, e := range entries {
		infos = append(infos, e.info())
	}
	return infos
}

func (s *Server) get(id string) (*entry, *proto.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.panes[id]
	if !ok {
		return nil, proto.Errorf(proto.ErrNotFound, "no pane %q", id)
	}
	return e, nil
}

func (s *Server) create(cp proto.PaneCreateParams) (proto.PaneInfo, *proto.Error) {
	if cp.Agent != "" {
		if _, known := s.manifests[cp.Agent]; !known {
			return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "unknown agent %q", cp.Agent)
		}
	}
	if len(cp.Command) == 0 && cp.Agent != "" {
		ad, ok := s.adapters.Get(cp.Agent)
		if !ok {
			return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "agent %q cannot be launched by conch", cp.Agent)
		}
		args := cp.AgentArgs
		if p := ad.PromptArgs(cp.Prompt); p != "" {
			args = strings.TrimSpace(args + " " + p)
		}
		cp.Command = ad.Command(config.DefaultShell(), args)
		cp.Env = append(cp.Env, ad.Env()...)
		if cp.Name == "" {
			cp.Name = cp.Agent
		}
	}
	if len(cp.Command) == 0 {
		// No command: this machine's login shell. Clients can't know which
		// shell a remote machine uses.
		cp.Command = []string{config.DefaultShell(), "-l"}
		cp.Env = append(cp.Env, s.shellThemeEnv(cp.ShellTheme)...)
	}
	if cp.Cwd == "" {
		cp.Cwd, _ = os.UserHomeDir()
	}
	if st, err := os.Stat(cp.Cwd); err != nil || !st.IsDir() {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "directory %q does not exist", cp.Cwd)
	}
	dir, err := filepath.EvalSymlinks(cp.Cwd)
	if err != nil {
		dir = cp.Cwd
	}
	var proj *project
	if !cp.NoProject {
		proj = s.projects.ensure(dir)
	}

	s.mu.Lock()
	s.nextID++
	id := "p" + strconv.Itoa(s.nextID)
	s.mu.Unlock()

	env := append([]string{"CONCH_SOCKET=" + s.sockPath}, cp.Env...)
	p, err := pane.Start(pane.Options{
		ID: id, Name: cp.Name, Command: cp.Command, Cwd: cp.Cwd,
		BaseEnv: adapter.SanitizeEnv(os.Environ()), Env: env,
		Cols: cp.Cols, Rows: cp.Rows,
	})
	if err != nil {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "start %q: %v", cp.Command[0], err)
	}
	e := newEntry(p, dir, detect.NewTracker(s.manifests, cp.Agent), proj)

	s.mu.Lock()
	s.panes[id] = e
	s.order = append(s.order, id)
	s.mu.Unlock()

	info := e.info()
	log.Printf("pane %s created: %v in %s", id, cp.Command, cp.Cwd)
	s.broadcast(proto.EventPaneCreated, info)
	go s.watch(e)
	return info, nil
}

// createTask starts an agent on its own branch and worktree.
func (s *Server) createTask(tp proto.TaskCreateParams) (proto.PaneInfo, *proto.Error) {
	if strings.TrimSpace(tp.Prompt) == "" {
		return proto.PaneInfo{}, proto.Errorf(proto.ErrBadRequest, "a task needs a prompt")
	}
	p, perr := s.projects.get(tp.ProjectID)
	if perr != nil {
		return proto.PaneInfo{}, perr
	}
	if tp.Agent == "" {
		tp.Agent = s.adapters[0].Name()
	}
	if tp.Branch == "" {
		tp.Branch = gitx.BranchFromPrompt(tp.Prompt)
	}
	path, _, perr := s.projects.addWorktree(p, tp.Branch, tp.Base)
	if perr != nil {
		return proto.PaneInfo{}, perr
	}
	return s.create(proto.PaneCreateParams{
		Agent:  tp.Agent,
		Prompt: tp.Prompt,
		Cwd:    path,
		Cols:   tp.Cols,
		Rows:   tp.Rows,
	})
}

func (s *Server) close(id string) *proto.Error {
	s.mu.Lock()
	e, ok := s.panes[id]
	if ok {
		delete(s.panes, id)
		for i, oid := range s.order {
			if oid == id {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	}
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	if !ok {
		return proto.Errorf(proto.ErrNotFound, "no pane %q", id)
	}
	for _, c := range clients {
		s.unsubscribe(c, id)
	}
	s.runs.forget(id)
	e.p.Close()
	log.Printf("pane %s closed", id)
	s.broadcast(proto.EventPaneClosed, proto.PaneRef{ID: id})
	return nil
}

// alive reports whether the pane is still registered (not closed).
func (s *Server) alive(e *entry) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.panes[e.p.ID()] == e
}

func (s *Server) broadcast(event string, data any) {
	msg := proto.Message{Event: event, Data: proto.Marshal(data)}
	s.mu.Lock()
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	for _, c := range clients {
		_ = c.conn.Write(msg)
	}
}

// subscribe streams frames of the pane to the client until unsubscribed. It
// sends one frame immediately, then at most one per frameInterval while the
// screen keeps changing.
func (s *Server) subscribe(c *client, e *entry) {
	p := e.p
	c.mu.Lock()
	if _, ok := c.subs[p.ID()]; ok {
		c.mu.Unlock()
		return
	}
	sub := &subscription{stop: make(chan struct{}), kick: make(chan struct{}, 1)}
	c.subs[p.ID()] = sub
	c.mu.Unlock()
	s.setWatchers(e, +1)

	go func() {
		for {
			changed := p.Changed() // grab before rendering so no change is missed
			frame := p.FrameAt(sub.anchoredOffset(p.History()))
			sub.settle(frame)
			if err := c.conn.Write(proto.Message{Event: proto.EventPaneFrame, Data: proto.Marshal(frame)}); err != nil {
				return
			}
			select {
			case <-changed:
			case <-sub.kick:
				continue // scrolling should feel immediate
			case <-sub.stop:
				return
			}
			select {
			case <-time.After(frameInterval):
			case <-sub.stop:
				return
			}
		}
	}()
}

// subscription is one client's live view of a pane.
type subscription struct {
	stop chan struct{}
	kick chan struct{} // re-render now

	mu      sync.Mutex
	offset  int // lines scrolled back into history
	history int // history length when offset was last applied
}

// anchoredOffset keeps a scrolled-back view on the same text while new
// output pushes more lines into history.
func (sub *subscription) anchoredOffset(history int) int {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.offset > 0 && history > sub.history {
		sub.offset += history - sub.history
	}
	sub.history = history
	return sub.offset
}

func (sub *subscription) settle(f proto.Frame) {
	sub.mu.Lock()
	sub.offset = f.Offset // clamped by the pane
	sub.mu.Unlock()
}

func (s *Server) scroll(c *client, e *entry, offset int) {
	c.mu.Lock()
	sub := c.subs[e.p.ID()]
	c.mu.Unlock()
	if sub == nil {
		return
	}
	sub.mu.Lock()
	sub.offset, sub.history = max(offset, 0), e.p.History()
	sub.mu.Unlock()
	select {
	case sub.kick <- struct{}{}:
	default:
	}
}

func (s *Server) unsubscribe(c *client, id string) {
	c.mu.Lock()
	sub, ok := c.subs[id]
	if ok {
		close(sub.stop)
		delete(c.subs, id)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	s.mu.Lock()
	e := s.panes[id]
	s.mu.Unlock()
	if e != nil {
		s.setWatchers(e, -1)
	}
}

func (s *Server) unsubscribeAll(c *client) {
	c.mu.Lock()
	ids := make([]string, 0, len(c.subs))
	for id := range c.subs {
		ids = append(ids, id)
	}
	c.mu.Unlock()
	for _, id := range ids {
		s.unsubscribe(c, id)
	}
}
