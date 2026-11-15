package server_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestPaneTeam: pane.team puts a pane in a team by hand, says so in its
// info and to every client, and is undone with "" — "-" keeps it out of
// every team whatever config.toml's rules say. A name is cleaned of
// control characters and kept short; a pane that is not there is not found.
func TestPaneTeam(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	c, dir := startServer(t)
	info := createCat(t, c, dir)
	if info.Team != "" {
		t.Fatalf("a new pane is in no team: %q", info.Team)
	}
	set := func(team string) proto.PaneInfo {
		t.Helper()
		var got proto.PaneInfo
		if err := call(t, c, proto.MethodPaneTeam, proto.PaneTeamParams{ID: info.ID, Team: team}, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := set("  Backend "); got.Team != "Backend" {
		t.Fatalf("set: %q", got.Team)
	}
	waitEvent(t, c, func(m proto.Message) bool {
		var p proto.PaneInfo
		return m.Event == proto.EventPaneUpdated && json.Unmarshal(m.Data, &p) == nil && p.ID == info.ID && p.Team == "Backend"
	})
	var list proto.PaneList
	if err := call(t, c, proto.MethodPaneList, nil, &list); err != nil || len(list.Panes) != 1 || list.Panes[0].Team != "Backend" {
		t.Fatalf("listed: %+v %v", list, err)
	}
	if got := set("-"); got.Team != "-" {
		t.Fatalf("kept out: %q", got.Team)
	}
	if got := set("UI\x1b]52;c;aGk=\x07"); got.Team != "UI·]52;c;aGk=·" {
		t.Fatalf("control characters are not a team's: %q", got.Team)
	}
	if got := set(""); got.Team != "" {
		t.Fatalf("back to the rules: %q", got.Team)
	}
	badRequest(t, c.Call(t.Context(), proto.MethodPaneTeam, proto.PaneTeamParams{ID: info.ID, Team: strings.Repeat("x", 65)}, nil), "64 characters")
	a5Code(t, c.Call(t.Context(), proto.MethodPaneTeam, proto.PaneTeamParams{ID: "p404", Team: "UI"}, nil), proto.ErrNotFound)
	if !slices.Contains(proto.Capabilities, proto.CapPaneTeam) {
		t.Fatal("pane.team.v1 is not announced")
	}
}
