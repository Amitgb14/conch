package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/pane"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/usage"
)

// A reload replaces the server's program without stopping its panes: the
// server execs the new binary in place, so it keeps its process ID, its
// children (the programs in the panes), their terminals and the listening
// socket. What only lives in memory — screens, agent states — is written to
// a state file the new program reads back.
const reloadStateEnv = "CONCH_RELOAD_STATE"

const reloadCapability = "server.reload.v1"

type reloadState struct {
	FromBuild  string             `json:"from_build"`
	ListenerFD int                `json:"listener_fd"`
	NextID     int                `json:"next_id"`
	Panes      []reloadPane       `json:"panes"`
	Limits     []proto.PlanLimits `json:"limits,omitempty"`
}

type reloadPane struct {
	Snapshot   pane.Snapshot       `json:"snapshot"`
	FD         int                 `json:"fd"`
	Dir        string              `json:"dir"`
	Tracker    detect.TrackerState `json:"tracker"`
	Transcript string              `json:"transcript,omitempty"`
	Loose      bool                `json:"loose,omitempty"` // in no project
}

// reloadBinary resolves and checks the program to reload into: it must run
// here and know how to take the panes over.
func reloadBinary(path string) (string, *proto.Error) {
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", proto.Errorf(proto.ErrInternal, "%v", err)
		}
		path = exe
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version", "--json").Output()
	if err != nil {
		return "", proto.Errorf(proto.ErrBadRequest, "%s does not run: %v", path, err)
	}
	var info buildinfo.Info
	if json.Unmarshal(out, &info) != nil {
		return "", proto.Errorf(proto.ErrBadRequest, "%s is not a conch binary", path)
	}
	if info.Build != "" && info.Build == buildinfo.Build() {
		return "", proto.Errorf(proto.ErrBadRequest, "the server already runs build %s; rebuild or update conch first", info.Build)
	}
	for _, c := range info.Capabilities {
		if c == reloadCapability {
			return path, nil
		}
	}
	return "", proto.Errorf(proto.ErrBadRequest, "%s (build %s) can't take over running panes; restart the server instead", path, info.Build)
}

// reload hands everything over to bin. It only returns if the exec failed,
// after resuming the panes.
func (s *Server) reload(bin string) {
	s.mu.Lock()
	defer s.mu.Unlock() // held until exec: no panes come or go meanwhile

	st := reloadState{FromBuild: buildinfo.Build(), NextID: s.nextID}
	var detached []*entry
	fail := func(err error) {
		log.Printf("reload failed, carrying on: %v", err)
		for _, e := range detached {
			e.p.Resume()
		}
	}

	ul, ok := s.ln.(*net.UnixListener)
	if !ok {
		fail(fmt.Errorf("listener is %T", s.ln))
		return
	}
	lf, err := ul.File() // a duplicate descriptor that survives exec
	if err != nil {
		fail(err)
		return
	}
	defer lf.Close()
	if err := pane.KeepOnExec(lf); err != nil {
		fail(err)
		return
	}
	st.ListenerFD = int(lf.Fd())

	for _, id := range s.order {
		e := s.panes[id]
		snap, ptmx, err := e.p.Detach()
		if err == pane.ErrNotRunning {
			continue // exited panes are not carried over
		}
		if err != nil {
			fail(fmt.Errorf("pane %s: %w", id, err))
			return
		}
		detached = append(detached, e)
		if err := pane.KeepOnExec(ptmx); err != nil {
			fail(err)
			return
		}
		rp := reloadPane{Snapshot: snap, FD: int(ptmx.Fd()), Dir: e.dir}
		e.mu.Lock()
		rp.Loose = e.project == nil
		rp.Tracker = e.tracker.Export()
		e.mu.Unlock()
		if e.transcript != nil {
			rp.Transcript = e.transcript.Path()
		}
		st.Panes = append(st.Panes, rp)
	}
	st.Limits = s.allLimits().Limits

	path := filepath.Join(s.configDir, "reload-state.json")
	b, _ := json.Marshal(st)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		fail(err)
		return
	}
	log.Printf("reloading into %s with %d pane(s)", bin, len(st.Panes))
	env := config.MergeEnv(os.Environ(), reloadStateEnv+"="+path)
	err = syscall.Exec(bin, []string{bin, "server"}, env)
	os.Remove(path)
	fail(fmt.Errorf("exec %s: %w", bin, err))
}

// takeReloadState reads the state a previous program left, if this process
// was started by a reload.
func takeReloadState() (*reloadState, error) {
	path := os.Getenv(reloadStateEnv)
	if path == "" {
		return nil, nil
	}
	os.Unsetenv(reloadStateEnv) // panes started from now on must not see it
	defer os.Remove(path)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st reloadState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// adopt takes over the panes of a reload.
func (s *Server) adopt(st *reloadState) {
	s.nextID = st.NextID
	for _, l := range st.Limits {
		s.setLimits(l)
	}
	for _, rp := range st.Panes {
		ptmx := os.NewFile(uintptr(rp.FD), "ptmx")
		p, err := pane.Adopt(rp.Snapshot, ptmx)
		if err != nil {
			log.Printf("reload: pane %s lost: %v", rp.Snapshot.ID, err)
			ptmx.Close()
			continue
		}
		var proj *project
		if !rp.Loose {
			proj = s.projects.ensure(rp.Dir)
		}
		e := newEntry(p, rp.Dir, detect.RestoreTracker(s.manifests, rp.Tracker), proj)
		if rp.Transcript != "" {
			e.transcript = usage.NewTranscript(rp.Transcript)
			if tok, err := e.transcript.Update(); err == nil || tok.Output > 0 {
				pt := proto.Tokens(tok)
				e.tokens = &pt
			}
		}
		s.mu.Lock()
		s.panes[rp.Snapshot.ID] = e
		s.order = append(s.order, rp.Snapshot.ID)
		s.mu.Unlock()
		go s.watch(e)
	}
	log.Printf("reloaded from build %s: took over %d pane(s)", st.FromBuild, len(st.Panes))
}
