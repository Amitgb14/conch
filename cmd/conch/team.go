package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

const agentTeamUsage = "usage: conch agent team ID [TEAM | - | -clear]"

// agentTeam says which team a pane is in, or puts it in one: a name, "-"
// for no team whatever the rules say, or -clear to leave it to the rules.
func agentTeam(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New(agentTeamUsage)
	}
	ref, team := args[0], ""
	set := len(args) == 2
	if set {
		team = strings.TrimSpace(args[1])
		switch team {
		case "-clear":
			team = ""
		case "":
			return errors.New(agentTeamUsage)
		}
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	if missing := c.MissingCapabilities([]string{proto.CapPaneTeam}); len(missing) > 0 {
		return fmt.Errorf("the conch server (pid %d) is from an older build without teams; reload it with `conch server reload`", c.Server.PID)
	}
	id, err := resolvePane(c, ref)
	if err != nil {
		return err
	}
	var info proto.PaneInfo
	if set {
		if err := call(c, proto.MethodPaneTeam, proto.PaneTeamParams{ID: id, Team: team}, &info); err != nil {
			return err
		}
	} else {
		var list proto.PaneList
		if err := call(c, proto.MethodPaneList, nil, &list); err != nil {
			return err
		}
		for _, p := range list.Panes {
			if p.ID == id {
				info = p
			}
		}
	}
	teams := paneTeams(c, []proto.PaneInfo{info})
	switch {
	case teams[info.ID] != "":
		how := "by hand"
		if info.Team == "" {
			how = "by its rule in config.toml"
		}
		fmt.Printf("%s is in %s (%s)\n", info.ID, teams[info.ID], how)
	case info.Team == config.NoTeam:
		fmt.Printf("%s is kept out of every team\n", info.ID)
	default:
		fmt.Printf("%s is in no team\n", info.ID)
	}
	return nil
}

// paneTeams is each pane's team, by hand or by config.toml's rules (an
// agent's project and branch); "" for none.
func paneTeams(c *client.Client, panes []proto.PaneInfo) map[string]string {
	var teams []config.Team
	if cfg, err := config.Load(); err == nil {
		teams = cfg.Teams
	}
	names := map[string]string{}
	if len(teams) > 0 {
		var projects proto.ProjectList
		if call(c, proto.MethodProjectList, nil, &projects) == nil {
			for _, p := range projects.Projects {
				names[p.ID] = p.Name
			}
		}
	}
	out := map[string]string{}
	for _, p := range panes {
		out[p.ID] = config.TeamFor(teams, p.Team, names[p.ProjectID], p.Branch)
	}
	return out
}

// onlyTeam keeps the panes in team (any case).
func onlyTeam(c *client.Client, panes []proto.PaneInfo, team string) []proto.PaneInfo {
	teams := paneTeams(c, panes)
	var out []proto.PaneInfo
	for _, p := range panes {
		if strings.EqualFold(teams[p.ID], team) {
			out = append(out, p)
		}
	}
	return out
}
