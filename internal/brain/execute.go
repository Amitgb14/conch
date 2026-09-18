package brain

import (
	"context"
	"fmt"
	"strings"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// Result is what running an action produced.
type Result struct {
	Pane    *proto.PaneInfo // a started pane
	Machine string
}

// Execute runs a validated action through the machine's client. Cols and
// rows size new panes.
func Execute(ctx context.Context, c *client.Client, a Action, cols, rows int) (Result, error) {
	res := Result{Machine: a.Machine}
	switch a.Type {
	case ActStartTask:
		var info proto.PaneInfo
		err := c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{
			ProjectID: a.Project, Prompt: a.Prompt, Branch: a.Branch, Base: a.Base, Agent: a.Agent, Cols: cols, Rows: rows,
		}, &info)
		if err != nil {
			return res, err
		}
		res.Pane = &info
	case ActStartAgent:
		var list proto.ProjectList
		if err := c.Call(ctx, proto.MethodProjectList, nil, &list); err != nil {
			return res, err
		}
		dir := ""
		for _, p := range list.Projects {
			if p.ID != a.Project {
				continue
			}
			dir = p.Path
			if a.Branch != "" {
				dir = ""
				for _, wt := range p.Worktrees {
					if wt.Branch == a.Branch {
						dir = wt.Path
					}
				}
				if dir == "" {
					var wt proto.WorktreeResult
					if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: p.ID, Branch: a.Branch, Base: a.Base}, &wt); err != nil {
						return res, err
					}
					dir = wt.Path
				}
			}
		}
		if dir == "" {
			return res, fmt.Errorf("project %s not found", a.Project)
		}
		params := proto.PaneCreateParams{Agent: a.Agent, Cwd: dir, Cols: cols, Rows: rows}
		params.Prompt = a.Prompt
		var info proto.PaneInfo
		if err := c.Call(ctx, proto.MethodPaneCreate, params, &info); err != nil {
			return res, err
		}
		res.Pane = &info
	case ActSend:
		if err := c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: a.Pane, Text: a.Text, Paste: strings.Contains(a.Text, "\n")}, nil); err != nil {
			return res, err
		}
		if err := c.Call(ctx, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: a.Pane, Keys: []string{"enter"}}, nil); err != nil {
			return res, err
		}
	case ActClose:
		if err := c.Call(ctx, proto.MethodPaneClose, proto.PaneRef{ID: a.Pane}, nil); err != nil {
			return res, err
		}
	case ActShare:
		if missing := c.MissingCapabilities([]string{"session.share.v1"}); len(missing) > 0 {
			return res, fmt.Errorf("the server there predates sharing conversations; reload it")
		}
		var list proto.PaneList
		if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil {
			return res, err
		}
		var from *proto.PaneInfo
		for i, p := range list.Panes {
			if p.ID == a.Pane {
				from = &list.Panes[i]
			}
		}
		switch {
		case from == nil:
			return res, fmt.Errorf("pane %s is gone", a.Pane)
		case from.Agent == nil:
			return res, fmt.Errorf("%s is not running an agent", from.DisplayName())
		case from.Agent.SessionID == "":
			return res, fmt.Errorf("%s hasn't saved a conversation to hand over yet", from.DisplayName())
		}
		params := proto.SessionShareParams{Agent: from.Agent.Name, ID: from.Agent.SessionID, Dir: from.Cwd,
			To: a.Agent, PaneID: a.ToPane, Cols: cols, Rows: rows}
		var out proto.SessionShareResult
		if err := c.Call(ctx, proto.MethodSessionShare, params, &out); err != nil {
			return res, err
		}
		if out.Pane.ID != "" {
			pane := out.Pane
			res.Pane = &pane
		}
	case ActBroadcast:
		if missing := c.MissingCapabilities([]string{"agent.broadcast.v1"}); len(missing) > 0 {
			return res, fmt.Errorf("the server there predates broadcasting; reload it")
		}
		var out proto.AgentBroadcastResult
		// Agents only: a terminal would run the text as a command.
		if err := c.Call(ctx, proto.MethodAgentBroadcast, proto.AgentBroadcastParams{IDs: a.Panes, Text: a.Text}, &out); err != nil {
			return res, err
		}
		var failed []string
		for _, r := range out.Results {
			if !r.Sent {
				failed = append(failed, r.ID+": "+r.Error)
			}
		}
		if len(failed) == len(out.Results) && len(failed) > 0 {
			return res, fmt.Errorf("nothing was sent — %s", strings.Join(failed, "; "))
		}
		if len(failed) > 0 {
			return res, fmt.Errorf("sent to %d of %d — %s", len(out.Results)-len(failed), len(out.Results), strings.Join(failed, "; "))
		}
	case ActFocus:
		// The client shows the pane; nothing to do on the server.
	default:
		return res, fmt.Errorf("unknown action %q", a.Type)
	}
	return res, nil
}
