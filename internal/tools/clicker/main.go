// Command clicker clicks or drags a cell of a pane through conch's
// protocol, for driving a TUI under test by hand:
//
//	go run ./internal/tools/clicker $CONCH_SOCKET p1 109 39        # click
//	go run ./internal/tools/clicker $CONCH_SOCKET p1 10 3 60 0     # drag
//
// The end-to-end plan needs clicks as well as keys — the version cell in
// the status bar, a row in a menu — and there is no other way to send one:
// `conch send` types text, and a raw mouse escape sequence is encoded as
// text rather than passed through. With a second pair of coordinates it
// presses at the first, moves to the second and lets go there, which is
// what the rows for dragging a tab, a split or a scrollbar need: those are
// three events, and a press and a release alone do not make a drag.
// Coordinates are 0-based cells of the pane, as the protocol takes them.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

func main() {
	sock, id := os.Args[1], os.Args[2]
	x, _ := strconv.Atoi(os.Args[3])
	y, _ := strconv.Atoi(os.Args[4])
	c, err := client.Dial(sock, "clicker")
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A drag when a second cell is given: press, move, let go there. The
	// motion carries no button, as a terminal reports it.
	type step struct {
		action, button string
		x, y           int
	}
	steps := []step{{proto.MousePress, "left", x, y}, {proto.MouseRelease, "left", x, y}}
	if len(os.Args) >= 7 {
		x2, _ := strconv.Atoi(os.Args[5])
		y2, _ := strconv.Atoi(os.Args[6])
		steps = []step{
			{proto.MousePress, "left", x, y},
			{proto.MouseMotion, "none", x2, y2},
			{proto.MouseRelease, "left", x2, y2},
		}
	}
	for _, s := range steps {
		p := proto.PaneSendMouseParams{ID: id, X: s.x, Y: s.y, Button: s.button, Action: s.action}
		if err := c.Call(ctx, proto.MethodPaneSendMouse, p, nil); err != nil {
			fmt.Fprintln(os.Stderr, s.action, "failed:", err)
			os.Exit(1)
		}
	}
	if len(steps) == 3 {
		fmt.Println("dragged", x, y, "->", steps[2].x, steps[2].y)
		return
	}
	fmt.Println("clicked", x, y)
}
