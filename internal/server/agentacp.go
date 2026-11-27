package server

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
)

// The agents this server can start used to be read once, when it started:
// adding one meant a reload. Settings adds an ACP agent while panes run
// (agent.acp.add), so the registry and the detection manifests are read
// again then, and every reader takes them through registry() and
// agentManifests() rather than the fields. A tracker made before keeps the
// map it was given, which is right: it is watching an agent that was.

func (s *Server) registry() adapter.Registry {
	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()
	return s.adapters
}

func (s *Server) agentManifests() map[string]*detect.Manifest {
	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()
	return s.manifests
}

// loadAgents reads every agent conch can start, and how to recognise
// each, from the adapters and the manifests on this machine.
func (s *Server) loadAgents(exe string) error {
	ads, err := adapter.New(exe, s.configDir)
	if err != nil {
		return err
	}
	ms, errs := detect.LoadManifests(detect.ManifestDir(s.configDir))
	for _, err := range errs {
		log.Printf("agent manifest ignored: %v", err)
	}
	s.agentsMu.Lock()
	s.adapters, s.manifests, s.exe = ads, ms, exe
	s.agentsMu.Unlock()
	return nil
}

// reloadAgents reads them again after a manifest changed, and forgets what
// was found installed so the next agent.status looks afresh.
func (s *Server) reloadAgents() *proto.Error {
	s.agentsMu.RLock()
	exe := s.exe
	s.agentsMu.RUnlock()
	if err := s.loadAgents(exe); err != nil {
		return proto.Errorf(proto.ErrInternal, "reading the agents again: %v", err)
	}
	return nil
}

// supportedAgent is whether name is an agent conch has an adapter for,
// which a manifest may not take.
func (s *Server) supportedAgent(name string) bool {
	ad, ok := s.registry().Get(name)
	return ok && ad.Tier() == proto.TierSupported
}

func acpManifest(p proto.AgentACPSpec) adapter.ACPManifest {
	return adapter.ACPManifest{Name: p.Name, Label: p.Label, Command: p.Command, Dirs: p.Dirs, Env: p.Env}
}

// checkACP starts the command once in the home folder and says what it
// offers. Nothing is written.
func (s *Server) checkACP(p proto.AgentACPSpec) (proto.AgentACPCheck, *proto.Error) {
	m := acpManifest(p)
	if m.Name == "" {
		m.Name = "acp" // the check does not need one; the add does
	}
	if err := m.Check(nil); err != nil {
		return proto.AgentACPCheck{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	home, _ := os.UserHomeDir()
	return adapter.CheckACP(context.Background(), config.DefaultShell(), home, m), nil
}

// addACP writes the agent's manifest here and offers it at once.
func (s *Server) addACP(p proto.AgentACPAddParams) (proto.AgentAvailability, *proto.Error) {
	path, err := adapter.WriteACPManifest(adapter.ManifestDir(s.configDir), acpManifest(p.AgentACPSpec), p.Replace, s.supportedAgent)
	if err != nil {
		code := proto.ErrBadRequest
		if errors.Is(err, adapter.ErrManifestExists) {
			err = errors.New("an agent called " + p.Name + " was added here already: pick another name, or change that one")
		}
		return proto.AgentAvailability{}, proto.Errorf(code, "%v", err)
	}
	log.Printf("agent %s added over ACP: %s", p.Name, path)
	s.acp.forget(p.Name)
	if perr := s.reloadAgents(); perr != nil {
		return proto.AgentAvailability{}, perr
	}
	ad, ok := s.registry().Get(p.Name)
	aa, isACP := adapter.AsACP(ad)
	if !ok || !isACP {
		// Written, but not offered: the file did not load, which the log says.
		return proto.AgentAvailability{}, proto.Errorf(proto.ErrInternal, "%s was written but conch could not read it back; see the server log", path)
	}
	// Said from a look at whether it is installed, not from asking it: an
	// agent that never answers would hold the add for the probe's whole
	// timeout. What it offers is asked in the background, for the next
	// agent.status.
	found := ad.Detect(context.Background(), config.DefaultShell())
	av := proto.AgentAvailability{Name: ad.Name(), Label: ad.Label(), Installed: found.Installed, Path: found.Path,
		Version: found.Version, Tier: ad.Tier(), NoInstaller: strings.TrimSpace(ad.InstallScript()) == "", SwitchTo: switchTo(ad),
		Protocol: proto.ProtocolACP, Command: aa.Spec().Command, Removable: aa.Own()}
	if init, known := s.acp.known(acpKey(aa)); known {
		av.Sessions = init.CanList()
	} else if found.Installed {
		goSafe("acp capabilities", func() { s.acpCapabilities(aa) })
	}
	return av, nil
}

// removeACP removes an ACP agent added here: its manifest, never the
// program or its sessions, and never a manifest conch ships or one that
// runs an agent another way.
func (s *Server) removeACP(p proto.AgentACPRemoveParams) *proto.Error {
	ad, ok := s.registry().Get(p.Name)
	if aa, isACP := adapter.AsACP(ad); !ok || !isACP || !aa.Own() {
		return proto.Errorf(proto.ErrBadRequest, "%q is not an ACP agent added on this machine", p.Name)
	}
	if err := adapter.RemoveACPManifest(adapter.ManifestDir(s.configDir), p.Name); err != nil {
		return proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	log.Printf("agent %s removed", p.Name)
	s.acp.forget(p.Name)
	return s.reloadAgents()
}
