package phone

import (
	"slices"

	"github.com/Amitgb14/conch/internal/artifacts"
	"github.com/Amitgb14/conch/internal/fleet"
	"github.com/Amitgb14/conch/internal/proto"
)

// Artifact is a claude.ai artifact an agent published, as its session
// transcript records it (internal/artifacts reads them). The phone is
// never sent Panes or Project: those are the server's artifact.list.
type Artifact = proto.Artifact

// ArtifactList answers GET /api/artifacts: newest first. Pane is empty
// when it lists every artifact on this machine.
type ArtifactList struct {
	Pane      string     `json:"pane"`
	Artifacts []Artifact `json:"artifacts"`
}

// artifacts lists what the pane's Claude Code sessions published, or with
// no pane, what every Claude Code session on this machine published. The
// screen can't say: Claude Code draws full screen, and a link it printed
// has scrolled away by the time anyone looks. Its transcripts keep every
// publish, with the title it was given.
func (g *Gateway) artifacts(rq *request) (any, *APIError) {
	root := artifacts.Root()
	ref := rq.r.URL.Query().Get("pane")
	if ref == "" {
		found, err := artifacts.All(artifacts.Shared, root)
		if err != nil {
			return nil, apiErr(CodeServerUnavailable, "could not read the transcripts: "+err.Error())
		}
		return ArtifactList{Artifacts: artifacts.Plain(found)}, nil
	}
	c, pane, id, aerr := g.paneOn(rq.dev, ref)
	if aerr != nil {
		return nil, aerr
	}
	out := ArtifactList{Pane: id, Artifacts: []Artifact{}}
	if fleet.MachineOf(id) != LocalMachine {
		// The transcript is on that machine, out of this gateway's reach.
		return out, nil
	}
	var list proto.PaneList
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodPaneList, nil, &list); aerr != nil {
		return nil, aerr
	}
	i := slices.IndexFunc(list.Panes, func(p proto.PaneInfo) bool { return p.ID == pane })
	if i < 0 {
		return nil, apiErr(CodeNotFound, "no pane "+id)
	}
	a := list.Panes[i].Agent
	if a == nil || a.Name != "claude" || a.SessionID == "" {
		return out, nil
	}
	if root == "" {
		return out, nil
	}
	found, _, err := artifacts.OfSession(artifacts.Shared, root, a.SessionID)
	if err != nil {
		return nil, apiErr(CodeServerUnavailable, "could not read the agent's transcript: "+err.Error())
	}
	out.Artifacts = artifacts.Plain(found)
	return out, nil
}
