// Command clicker clicks a cell of a pane through conch's protocol, for
// driving a TUI under test by hand:
//
//	go run ./internal/tools/clicker $CONCH_SOCKET p1 109 39
//
// The end-to-end plan needs clicks as well as keys — the version cell in
// the status bar, a row in a menu — and there is no other way to send one:
// `conch send` types text, and a raw mouse escape sequence is encoded as
// text rather than passed through. Coordinates are 0-based cells of the
// pane, as the protocol takes them.
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
	for _, action := range []string{proto.MousePress, proto.MouseRelease} {
		p := proto.PaneSendMouseParams{ID: id, X: x, Y: y, Button: "left", Action: action}
		if err := c.Call(ctx, proto.MethodPaneSendMouse, p, nil); err != nil {
			fmt.Fprintln(os.Stderr, action, "failed:", err)
			os.Exit(1)
		}
	}
	fmt.Println("clicked", x, y)
}
