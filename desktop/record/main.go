// Command record captures the frames a conch pane would send while a busy,
// full-screen program runs in it, for the renderer benchmark. Phase 0 spike.
//
//	go run ./record -o frames.jsonl            # 160×50, 600 frames
//	go run ./record -gen                       # the program itself
//
// The program is this binary run with -gen: an agent-like screen with a
// conversation scrolling in a region, a spinner, a diff and a status line,
// styled with 16, 256 and true colours, wide characters and emoji. The pane
// is conch's own (internal/pane), so the frames are what the server renders.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/pane"
)

func main() {
	gen := flag.Bool("gen", false, "be the busy program (run inside the pane)")
	out := flag.String("o", "frames.jsonl", "where to write frames, one JSON object a line")
	cols := flag.Int("cols", 160, "pane width")
	rows := flag.Int("rows", 50, "pane height")
	n := flag.Int("n", 600, "frames to capture")
	every := flag.Duration("every", 33*time.Millisecond, "capture interval (the server's frameInterval)")
	flag.Parse()
	if *gen {
		generate(*cols, *rows)
		return
	}
	if err := record(*out, *cols, *rows, *n, *every); err != nil {
		fmt.Fprintln(os.Stderr, "record:", err)
		os.Exit(1)
	}
}

func record(out string, cols, rows, n int, every time.Duration) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	p, err := pane.Start(pane.Options{
		ID:      "p1",
		Command: []string{exe, "-gen", fmt.Sprint("-cols=", cols), fmt.Sprint("-rows=", rows)},
		Cols:    cols,
		Rows:    rows,
		BaseEnv: []string{"TERM=xterm-256color", "LANG=en_US.UTF-8", "PATH=/usr/bin:/bin"},
	})
	if err != nil {
		return err
	}
	defer p.Close()
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	time.Sleep(300 * time.Millisecond) // let it take the alternate screen
	for i := 0; i < n; i++ {
		if err := enc.Encode(p.Frame()); err != nil {
			return err
		}
		time.Sleep(every)
	}
	return w.Flush()
}

var words = strings.Fields(`the agent reads internal/server/server.go and finds where frames are
throttled then edits subscribe to coalesce kicks while tests run with race
detector enabled on darwin and linux — 変更を確認 ✅ 🚀 résumé naïve
func main() { fmt.Println("hello") } go test -race -count=1 ./... PASS ok`)

func generate(cols, rows int) {
	w := bufio.NewWriterSize(os.Stdout, 1<<16)
	r := rand.New(rand.NewSource(1))
	region := rows - 7
	fmt.Fprintf(w, "\x1b[?1049h\x1b[?25l\x1b[2J\x1b[1;%dr", region)
	spin := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	for tick := 0; ; tick++ {
		// A conversation line scrolls the region.
		fmt.Fprintf(w, "\x1b[%d;1H\n", region)
		line, width := styledLine(r, cols-2)
		fmt.Fprintf(w, "\x1b[%d;1H %s\x1b[0m%s", region, line, strings.Repeat(" ", max(0, cols-2-width)))
		// Every so often a diff hunk with full-width backgrounds.
		if tick%25 == 0 {
			for i := 0; i < 4; i++ {
				bg, sign := "48;2;20;60;30", "+"
				if i%2 == 1 {
					bg, sign = "48;2;70;20;25", "-"
				}
				fmt.Fprintf(w, "\x1b[%d;1H\n\x1b[%d;1H\x1b[%sm%s %-*s\x1b[0m", region, region, bg, sign, cols-3, fmt.Sprintf("   frame := p.FrameAt(sub.anchoredOffset(p.History())) // %d", tick+i))
			}
		}
		// The box, spinner and status line stay put below the region.
		fmt.Fprintf(w, "\x1b[%d;1H\x1b[38;5;240m╭%s╮", region+1, strings.Repeat("─", cols-2))
		fmt.Fprintf(w, "\x1b[%d;1H│\x1b[0m \x1b[1;38;2;215;119;87m%s\x1b[0m Thinking… \x1b[2m(%ds · ↑ %d tokens · esc to interrupt)\x1b[0m\x1b[%dG\x1b[38;5;240m│", region+2, spin[tick%len(spin)], tick/30, tick*37, cols)
		fmt.Fprintf(w, "\x1b[%d;1H│\x1b[0m > \x1b[7m \x1b[0m\x1b[%dG\x1b[38;5;240m│", region+3, cols)
		fmt.Fprintf(w, "\x1b[%d;1H╰%s╯\x1b[0m", region+4, strings.Repeat("─", cols-2))
		fmt.Fprintf(w, "\x1b[%d;1H\x1b[48;5;236;38;5;250m %-*s\x1b[0m", rows, cols-1, fmt.Sprintf("conch · master · ⎇ adding-gui-support · %d%% context · opus", 100-tick%100))
		w.Flush()
		time.Sleep(10 * time.Millisecond)
	}
}

// styledLine is a run of words in assorted styles, and its width in cells.
func styledLine(r *rand.Rand, room int) (string, int) {
	var b strings.Builder
	width := 0
	for {
		word := words[r.Intn(len(words))]
		ww := cells(word)
		if width+ww+1 > room {
			return b.String(), width
		}
		switch r.Intn(6) {
		case 0:
			fmt.Fprintf(&b, "\x1b[38;5;%dm", 16+r.Intn(216))
		case 1:
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm", 80+r.Intn(176), 80+r.Intn(176), 80+r.Intn(176))
		case 2:
			b.WriteString("\x1b[1;36m")
		case 3:
			b.WriteString("\x1b[3;33m")
		case 4:
			b.WriteString("\x1b[4;35m")
		default:
			b.WriteString("\x1b[0m")
		}
		b.WriteString(word)
		b.WriteString("\x1b[0m ")
		width += ww + 1
	}
}

// cells is a rough width: two for CJK and emoji, one otherwise. Close enough
// for laying out a test screen; the emulator decides the real widths.
func cells(s string) int {
	n := 0
	for _, c := range s {
		if c >= 0x1100 && (c <= 0x115f || (c >= 0x2e80 && c <= 0xa4cf) || (c >= 0xac00 && c <= 0xd7a3) || (c >= 0xf900 && c <= 0xfaff) || (c >= 0x1f300 && c <= 0x1faff) || c == 0x2705) {
			n += 2
		} else {
			n++
		}
	}
	return n
}
