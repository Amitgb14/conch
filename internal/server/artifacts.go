package server

import (
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/artifacts"
	"github.com/Amitgb14/conch/internal/proto"
)

// artifactList answers artifact.list: the claude.ai artifacts Claude Code
// published on this machine, read from its transcripts. With a pane, what
// that pane's agent published in its directory's sessions; without, every
// one, each named with the panes whose agent published it and the project
// it was made in, so a client can count per agent and narrow to a project
// from one answer.
func (s *Server) artifactList(p proto.ArtifactListParams) (proto.ArtifactList, *proto.Error) {
	root := artifacts.Root()
	if p.Pane != "" {
		e, perr := s.get(p.Pane)
		if perr != nil {
			return proto.ArtifactList{}, perr
		}
		info := e.info()
		out := proto.ArtifactList{Artifacts: []proto.Artifact{}}
		switch {
		case info.Agent == nil:
			out.Note = "no agent is running here"
			return out, nil
		case info.Agent.Name != "claude":
			out.Note = "only Claude Code keeps a record of the artifacts it publishes"
			return out, nil
		case info.Agent.SessionID == "":
			out.Note = "claude has not said which conversation it is in yet"
			return out, nil
		}
		found, _, err := artifacts.OfSession(artifacts.Shared, root, info.Agent.SessionID)
		if err != nil {
			return proto.ArtifactList{}, proto.Errorf(proto.ErrBadRequest, "could not read the agent's transcript: %v", err)
		}
		out.Artifacts = s.placeArtifacts(found, map[string][]string{})
		for i := range out.Artifacts {
			out.Artifacts[i].Panes = []string{p.Pane}
		}
		return out, nil
	}
	found, err := artifacts.All(artifacts.Shared, root)
	if err != nil {
		return proto.ArtifactList{}, proto.Errorf(proto.ErrBadRequest, "could not read the transcripts: %v", err)
	}
	return proto.ArtifactList{Artifacts: s.placeArtifacts(found, s.agentFolders(root))}, nil
}

// agentFolders is, for each transcript folder, the panes whose Claude Code
// saves its conversation there.
func (s *Server) agentFolders(root string) map[string][]string {
	out := map[string][]string{}
	if root == "" {
		return out
	}
	for _, info := range s.list() {
		a := info.Agent
		if a == nil || a.Name != "claude" || a.SessionID == "" {
			continue
		}
		if path := artifacts.FindTranscript(root, a.SessionID); path != "" {
			d := filepath.Dir(path)
			out[d] = append(out[d], info.ID)
		}
	}
	return out
}

// placeArtifacts names each artifact's panes (by the folders that
// published it) and project (by the checkout its directory is in).
func (s *Server) placeArtifacts(found []artifacts.Found, folders map[string][]string) []proto.Artifact {
	projects := s.projects.list()
	out := make([]proto.Artifact, 0, len(found))
	for _, f := range found {
		a := f.Artifact
		for _, d := range f.Folders {
			for _, id := range folders[d] {
				if !slices.Contains(a.Panes, id) {
					a.Panes = append(a.Panes, id)
				}
			}
		}
		slices.Sort(a.Panes)
		a.Project = projectOfDir(projects, a.Dir)
		out = append(out, a)
	}
	return out
}

// projectOfDir is the project whose folder or worktree holds dir, the
// deepest when one is inside another.
func projectOfDir(projects []proto.ProjectInfo, dir string) string {
	if dir == "" {
		return ""
	}
	best, bestLen := "", -1
	try := func(id, root string) {
		if root != "" && within(filepath.Clean(dir), filepath.Clean(root)) && len(root) > bestLen {
			best, bestLen = id, len(root)
		}
	}
	for _, p := range projects {
		try(p.ID, p.Path)
		for _, w := range p.Worktrees {
			try(p.ID, w.Path)
		}
	}
	return best
}

// published remembers each pane's artifacts as last told, so a publish
// is announced once and only what changed.
type published struct {
	mu    sync.Mutex
	panes map[string]map[string]int // pane → url → versions
}

// artifactRetries is when, after the Artifact tool's hook, the transcript
// is read for what it published. The hook can run before Claude Code has
// written the tool's result there, so it is read again a moment later.
var artifactRetries = []time.Duration{200 * time.Millisecond, time.Second, 3 * time.Second}

// artifactPublished tells clients what a pane's agent just published:
// called after its Artifact tool ran. It returns what it told them.
func (s *Server) artifactPublished(paneID, session string) []proto.ArtifactPublished {
	root := artifacts.Root()
	// A transcript's times are the agent's clock, the same machine's; the
	// slack covers a tool that ran a while before its hook.
	since := time.Now().Add(-time.Minute)
	for _, wait := range artifactRetries {
		time.Sleep(wait)
		found, ok, err := artifacts.OfSession(artifacts.Shared, root, session)
		if !ok || err != nil {
			continue
		}
		e, perr := s.get(paneID)
		if perr != nil {
			return nil
		}
		changed := s.published.changed(paneID, found, since)
		if len(changed) == 0 {
			continue
		}
		agent := ""
		if a := e.info().Agent; a != nil {
			agent = a.Name
		}
		var told []proto.ArtifactPublished
		for _, a := range s.placeArtifacts(changed, map[string][]string{}) {
			a.Panes = []string{paneID}
			ev := proto.ArtifactPublished{Pane: paneID, Agent: agent, Artifact: a}
			s.broadcast(proto.EventArtifactPublished, ev)
			told = append(told, ev)
		}
		return told
	}
	return nil
}

// changed records found as the pane's artifacts and returns those that
// are new or have more versions than last time. With nothing recorded for
// the pane yet, it returns those updated since since: what the agent
// published an hour ago is not news, and the transcript read may not hold
// the publish yet, which the next read then finds as a change.
func (p *published) changed(pane string, found []artifacts.Found, since time.Time) []artifacts.Found {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.panes == nil {
		p.panes = map[string]map[string]int{}
	}
	was, seen := p.panes[pane]
	now := make(map[string]int, len(found))
	var out []artifacts.Found
	for _, f := range found {
		now[f.URL] = f.Versions
		if seen && f.Versions > was[f.URL] || !seen && !f.Updated.Before(since) {
			out = append(out, f)
		}
	}
	p.panes[pane] = now
	return out
}

// forget drops a closed pane's record.
func (p *published) forget(pane string) {
	p.mu.Lock()
	delete(p.panes, pane)
	p.mu.Unlock()
}
