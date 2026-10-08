package tui

import (
	"path"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// Opening the file where the diff is looking. Reading a change and then
// hunting for it in an editor is the part of reviewing in a terminal that
// conch can simply remove: it knows the file, and the hunk header knows
// the line.
//
// The editor runs in a pane of its own on the machine the worktree is on,
// as the files view already opens one (filesEdit). That matters for a
// remote machine: $EDITOR is a thing on *that* machine, and so is the
// file. It also leaves the TUI running, which suspending would not.

// editAtLineScript runs the machine's own editor at a line. Which flag
// means "this line" is the editor's business and they disagree, so the
// script asks the editor's name rather than conch guessing from here —
// where the answer could be for the wrong machine entirely.
//
// An editor nobody here has heard of opens the file without a line. That
// is the honest failure: a flag it does not know would be taken for a
// file name, and it would open a new buffer called "+42".
const editAtLineScript = `ed=${VISUAL:-${EDITOR:-vi}}
case "$(basename "${ed%% *}")" in
  vi|vim|nvim|view|nano|micro|kak|emacs|emacsclient|joe|pico|ne)
    exec $ed +"$2" "$1" ;;
  code|code-insiders|codium|vscodium|cursor|windsurf|zed)
    exec $ed --goto "$1:$2" ;;
  subl|sublime_text|hx|helix)
    exec $ed "$1:$2" ;;
  idea|goland|webstorm|pycharm|rubymine|phpstorm|clion|rider)
    exec $ed --line "$2" "$1" ;;
  *)
    exec $ed "$1" ;;
esac`

// hunkLine is the line in the *new* file that hunk i starts changing: the
// header's +start, walked forward over the context git puts before the
// change, so the editor lands on what changed rather than three lines
// above it. 0 when there is no telling.
func hunkLine(hunks []string, i int) int {
	if i < 0 || i >= len(hunks) {
		return 0
	}
	lines := strings.Split(hunks[i], "\n")
	if len(lines) == 0 {
		return 0
	}
	start := newStartOf(lines[0])
	if start == 0 {
		return 0
	}
	// Context lines count towards the line number; the first + or − is
	// where the change is. A hunk of nothing but context cannot happen,
	// but would simply leave the cursor at its start.
	for _, l := range lines[1:] {
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
			break
		}
		if strings.HasPrefix(l, " ") || l == "" {
			start++
		}
	}
	return start
}

// newStartOf reads the +start of a hunk header: "@@ -3,4 +5,6 @@ func x()"
// gives 5. A header conch cannot read gives 0 rather than a guess.
func newStartOf(header string) int {
	plus := strings.Index(header, "+")
	if !strings.HasPrefix(header, "@@") || plus < 0 {
		return 0
	}
	field := header[plus+1:]
	if i := strings.IndexAny(field, ", @"); i >= 0 {
		field = field[:i]
	}
	n, err := strconv.Atoi(field)
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// editAtHunk opens the file the diff is showing, at the hunk the cursor is
// on, in a pane on that worktree's machine.
func (cv *changesView) editAtHunk(m *Model) tea.Cmd {
	if cv.diffFile == "" {
		return nil
	}
	c := m.clientOf(cv.machine)
	if c == nil {
		m.setFlash(m.offlineText(cv.machine), true)
		return nil
	}
	root := ""
	if cv.data != nil {
		root = cv.data.Worktree
	}
	if root == "" {
		// A branch that is not checked out has no file to open: its diff
		// is read from git, and there is nothing on disk to edit.
		m.setFlash("this branch has no worktree, so there is no file to open", true)
		return nil
	}
	_, hunks, _ := cv.hunksOf()
	line := hunkLine(hunks, cv.hunkSel)
	if line == 0 {
		line = 1
	}
	file := path.Join(root, cv.diffFile)
	cols, rows := m.paneArea()
	mid := cv.machine
	params := proto.PaneCreateParams{
		Name: "edit · " + path.Base(cv.diffFile), Cwd: root, Cols: cols, Rows: rows,
		Command: []string{"/bin/sh", "-lc", editAtLineScript, "sh", file, strconv.Itoa(line)},
	}
	return func() tea.Msg {
		var info proto.PaneInfo
		if err := callCtx(c, proto.MethodPaneCreate, params, &info); err != nil {
			return errMsg{err}
		}
		return createdMsg{machine: mid, info: info}
	}
}

// editFile opens a file from the list, at the top: its diff has not been
// read, so there is no hunk to aim at yet.
func (cv *changesView) editFile(m *Model, rel string) tea.Cmd {
	was := cv.diffFile
	cv.diffFile = rel
	cmd := cv.editAtHunk(m)
	cv.diffFile = was
	return cmd
}
