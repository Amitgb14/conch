package server

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sandbox"
	"github.com/Amitgb14/conch/internal/tracker"
)

// A repository's .conch/integrations.toml is somebody else's word about
// what this person's keys are used for, so it is asked about once per
// repository before detection takes it — and again only when it names
// something new. The answers are kept here, by the repository's root.

type teamAnswer struct {
	Answer string   `json:"answer"`
	Names  []string `json:"names"`
}

var teamMu sync.Mutex

func teamAnswersPath() string { return filepath.Join(integration.Dir(), "team.json") }

func readTeamAnswers() map[string]teamAnswer {
	out := map[string]teamAnswer{}
	data, err := os.ReadFile(teamAnswersPath())
	if err == nil {
		// A damaged file asks again rather than failing detection.
		_ = json.Unmarshal(data, &out)
	}
	return out
}

func writeTeamAnswer(root, answer string, names []string) error {
	teamMu.Lock()
	defer teamMu.Unlock()
	all := readTeamAnswers()
	all[root] = teamAnswer{Answer: answer, Names: names}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(integration.Dir(), 0o755); err != nil {
		return err
	}
	tmp := teamAnswersPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, teamAnswersPath())
}

// teamOf is the repository's team file and what the person said about it;
// nil when it has none.
func teamOf(root string) *proto.IntegrationTeam {
	names, err := integration.ReadTeam(root)
	if err != nil {
		return &proto.IntegrationTeam{Path: integration.TeamFile, State: "ignored", Problem: err.Error()}
	}
	if names == nil {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(integration.TeamFile))); err != nil {
			return nil
		}
	}
	t := &proto.IntegrationTeam{Path: integration.TeamFile, Names: names, State: "ask"}
	teamMu.Lock()
	a, ok := readTeamAnswers()[root]
	teamMu.Unlock()
	if ok && integration.Subset(names, a.Names) {
		switch a.Answer {
		case proto.TeamUse:
			t.State = "used"
		case proto.TeamIgnore:
			t.State = "ignored"
		}
	}
	return t
}

// integrationTeam answers a repository's team file, or writes or removes
// it, and says the project's integrations afresh.
func (pm *projectManager) integrationTeam(p proto.IntegrationTeamParams) (proto.IntegrationDetect, *proto.Error) {
	proj, perr := pm.gitProject(p.ProjectID)
	if perr != nil {
		return proto.IntegrationDetect{}, perr
	}
	root := proj.root
	var err error
	switch p.Action {
	case proto.TeamUse, proto.TeamIgnore:
		names, rerr := integration.ReadTeam(root)
		if rerr != nil {
			return proto.IntegrationDetect{}, proto.Errorf(proto.ErrBadRequest, "%v", rerr)
		}
		if names == nil {
			return proto.IntegrationDetect{}, proto.Errorf(proto.ErrBadRequest, "%s has no %s", proj.snapshot().Name, integration.TeamFile)
		}
		err = writeTeamAnswer(root, p.Action, names)
	case proto.TeamSave:
		if err = integration.WriteTeam(root, p.Names); err == nil {
			// Their own word needs no asking.
			names, _ := integration.ReadTeam(root)
			err = writeTeamAnswer(root, proto.TeamUse, names)
		}
	case proto.TeamRemove:
		err = integration.RemoveTeam(root)
	default:
		return proto.IntegrationDetect{}, proto.Errorf(proto.ErrBadRequest, "integration.team: no action %q (use, ignore, save or remove)", p.Action)
	}
	if err != nil {
		return proto.IntegrationDetect{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	return pm.integrationsOf(p.ProjectID)
}

// settingsUses are what Settings ties to a project rather than its files
// show: the tracker its tickets come from, and the sandbox providers set
// up for tasks run off this machine.
func (pm *projectManager) settingsUses(name, root string) []proto.IntegrationUse {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	var out []proto.IntegrationUse
	if which, scope, err := tracker.Choose(cfg.Trackers, name, root, "", pm.s.trackerSecret); err == nil {
		u := proto.IntegrationUse{Name: which, Label: trackerLabel(which), Kind: integration.KindTickets, Installed: true, Why: "set up in Settings"}
		if scope != "" {
			u.Label += " (" + scope + ")"
		}
		if _, ok := cfg.Trackers.ForProject(name, root); ok {
			u.Why = "chosen in Settings"
		}
		switch which {
		case tracker.Linear:
			u.KeyEnv = firstNonEmpty(cfg.Trackers.Linear.APIKeyEnv, tracker.LinearKeyEnv)
			u.Connected = cfg.Trackers.Linear.APIKey != "" || pm.s.trackerSecret(u.KeyEnv) != ""
		case tracker.Jira:
			j := cfg.Trackers.Jira
			u.KeyEnv = firstNonEmpty(j.APITokenEnv, tracker.JiraTokenEnv)
			u.Connected = j.Email != "" && (j.APIToken != "" || pm.s.trackerSecret(u.KeyEnv) != "")
			if h, err := url.Parse(j.URL); err == nil && h.Host != "" {
				u.Why += " · " + h.Host
			}
		}
		out = append(out, u)
	}
	var boxes []string
	installed := sandbox.Names()
	for n := range cfg.Sandbox {
		// Installed: a table in config.toml for a provider with no file
		// (boat.dev's, from before providers were files) sets up nothing.
		if slices.Contains(installed, n) {
			boxes = append(boxes, n)
		}
	}
	sort.Strings(boxes)
	for _, n := range boxes {
		// Whether its key is set is the provider's to say, and each keeps
		// it its own way: the sheet shows it as set up, not connected.
		out = append(out, proto.IntegrationUse{Name: n, Label: sandbox.ProviderLabel(n), Kind: integration.KindSandbox, Installed: true,
			Why: "for tasks run off this machine"})
	}
	return out
}

func trackerLabel(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
