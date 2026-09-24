package pane

import (
	"fmt"
	"strings"
	"testing"
)

// Output that turns the alternate screen on and scrolls it in the same
// read — a burst the pty hands over at once, common under load — should
// leave its scrolled-off lines behind like output that arrives later.
func TestAltScrollKeepsLinesFromTheWriteThatEntersIt(t *testing.T) {
	p := startPrinter(t, `echo ready`, "ready")
	var b strings.Builder
	b.WriteString("\x1b[?1049h")
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&b, "alt-%d\r\n", i)
	}
	p.emuMu.Lock()
	p.writeToEmulator([]byte(b.String()))
	p.emuMu.Unlock()
	if h := p.History(); h == 0 {
		t.Fatal("nothing kept of the lines that scrolled off")
	}
}
