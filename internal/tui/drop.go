package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// Terminals don't tell a program that files were dropped on it: they paste
// the files' paths. A paste into a remote pane made up only of paths to
// local files is taken as a drop, and the files are uploaded to that machine
// so the agent there can open them (a screenshot, say). Their paths there
// are pasted in place of the local ones.

// dropRoots are where dropped files are expected to live: the home folder,
// temporary folders (screenshot thumbnails) and mounted volumes. A pasted
// /etc/hosts or /bin/sh is more likely meant for the remote machine, which
// has its own.
func dropRoots() []string {
	roots := []string{os.TempDir(), "/tmp", "/private/tmp", "/var/folders", "/private/var/folders", "/Volumes"}
	if home, err := os.UserHomeDir(); err == nil && home != "/" {
		roots = append(roots, home)
	}
	return roots
}

// parseDroppedPaths splits a paste like a shell would — backslash escapes,
// single and double quotes, file:// URLs — and returns the paths only when
// every word is an absolute path to an existing regular file under
// dropRoots. quote is how the paste quoted its first path: a single or
// double quote, or 0 for backslashes.
func parseDroppedPaths(text string) (paths []string, quote byte) {
	words, quote, ok := shellWords(text)
	if !ok || len(words) == 0 {
		return nil, 0
	}
	roots := dropRoots()
	for _, w := range words {
		if strings.HasPrefix(w, "file://") {
			u, err := url.Parse(w)
			if err != nil || (u.Host != "" && u.Host != "localhost") {
				return nil, 0
			}
			w = u.Path
		}
		if !filepath.IsAbs(w) || !underAny(filepath.Clean(w), roots) {
			return nil, 0
		}
		if st, err := os.Stat(w); err != nil || !st.Mode().IsRegular() {
			return nil, 0
		}
		paths = append(paths, w)
	}
	return paths, quote
}

func underAny(path string, roots []string) bool {
	for _, r := range roots {
		r = filepath.Clean(r)
		if path == r || strings.HasPrefix(path, strings.TrimSuffix(r, "/")+"/") {
			return true
		}
	}
	return false
}

// shellWords splits text into words as a shell would, without expansions.
// ok is false for an unterminated quote.
func shellWords(text string) (words []string, quote byte, ok bool) {
	var (
		cur     strings.Builder
		inWord  bool
		in      rune // the open quote, or 0
		escaped bool
	)
	for _, r := range text {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case in == '\'':
			if r == '\'' {
				in = 0
			} else {
				cur.WriteRune(r)
			}
		case in == '"':
			switch r {
			case '"':
				in = 0
			case '\\':
				escaped = true
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			escaped, inWord = true, true
		case r == '\'' || r == '"':
			if !inWord && len(words) == 0 {
				quote = byte(r)
			}
			in, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			// Only ASCII spaces: macOS names screenshots "… at 10.02.11\u202fAM.png"
			// and terminals don't escape that space.
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if in != 0 || escaped {
		return nil, 0, false
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, quote, true
}

// shellQuote writes path the way the drop quoted its paths.
func shellQuote(path string, quote byte) string {
	switch quote {
	case '\'':
		return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	case '"':
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(path) + `"`
	}
	var b strings.Builder
	for _, r := range path {
		if r < 0x80 && !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+,@%:=", r)) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// fileDrop is files on their way to a remote pane. The pointer travels in
// dropStepMsg; only the command running a step touches it meanwhile.
type fileDrop struct {
	machine string
	label   string
	pane    string
	created time.Time // the pane's start, so a restarted server's same ID doesn't get the paste
	quote   byte
	space   bool // the drop ended with a space, as Terminal.app's do
	c       *client.Client

	paths  []string
	ups    []*client.Upload
	cur    int
	remote []string
}

type dropStepMsg struct {
	d   *fileDrop
	err error
}

// dropFiles uploads the files a paste into pane mid/id names, when it is a
// drop into a remote pane. handled is false when the paste should be typed
// as it is.
func (m *Model) dropFiles(mid, id string, k tea.KeyMsg) (cmd tea.Cmd, handled bool) {
	mach := m.machine(mid)
	if !k.Paste || mach == nil || mach.target == "" || !m.cfg.Remote.UploadDrops {
		return nil, false
	}
	paths, quote := parseDroppedPaths(string(k.Runes))
	if paths == nil {
		return nil, false
	}
	if mach.c == nil {
		m.setFlash(mach.label+" is "+mach.state.String()+" · drop the files again once it's back", true)
		return nil, true
	}
	if len(mach.c.MissingCapabilities([]string{"fs.upload.v1"})) > 0 {
		m.setFlash(mach.label+" needs a conch update to take dropped files", true)
		return nil, false
	}
	limit := m.cfg.Remote.UploadLimit()
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.Size() > limit {
			m.setFlash(fmt.Sprintf("%s is %s; the limit is %s (settings)", filepath.Base(p), mbText(st.Size()), mbText(limit)), true)
			return nil, true
		}
	}
	d := &fileDrop{machine: mid, label: mach.label, pane: id, quote: quote, c: mach.c, paths: paths,
		space: strings.HasSuffix(string(k.Runes), " ")}
	if p := mach.pane(id); p != nil {
		d.created = p.Created
	}
	m.setFlash(d.progress(), false)
	return d.read(limit), true
}

// read loads every file at once: a screenshot dragged from its thumbnail
// is deleted by macOS a few seconds after the drop.
func (d *fileDrop) read(limit int64) tea.Cmd {
	return func() tea.Msg {
		for _, p := range d.paths {
			data, err := os.ReadFile(p)
			switch {
			case errors.Is(err, os.ErrNotExist):
				return dropStepMsg{d: d, err: fmt.Errorf("%s is gone; drop it again from where it was saved", filepath.Base(p))}
			case err != nil:
				return dropStepMsg{d: d, err: err}
			case int64(len(data)) > limit:
				return dropStepMsg{d: d, err: fmt.Errorf("%s is %s; the limit is %s (settings)", filepath.Base(p), mbText(int64(len(data))), mbText(limit))}
			}
			d.ups = append(d.ups, client.NewUpload(d.c, filepath.Base(p), data))
		}
		return dropStepMsg{d: d}
	}
}

// step sends the next chunk.
func (d *fileDrop) step() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		up := d.ups[d.cur]
		done, err := up.Step(ctx)
		if err != nil {
			var perr *proto.Error
			if errors.As(err, &perr) {
				err = errors.New(perr.Message)
			}
			return dropStepMsg{d: d, err: fmt.Errorf("%s: %w", up.Name(), err)}
		}
		if done {
			d.remote = append(d.remote, up.Path())
			d.cur++
		}
		return dropStepMsg{d: d}
	}
}

// progress leads with the machine and percentage: in a pane the status bar
// leaves a message a quarter of the width, and file names are long.
func (d *fileDrop) progress() string {
	name := filepath.Base(d.paths[min(d.cur, len(d.paths)-1)])
	if len(d.ups) == 0 {
		return "uploading to " + d.label + " · " + name
	}
	var sent, total int
	for _, up := range d.ups {
		sent, total = sent+up.Sent(), total+up.Size()
	}
	pct := 100
	if total > 0 {
		pct = sent * 100 / total
	}
	return fmt.Sprintf("uploading to %s %d%% · %s", d.label, pct, name)
}

// dropStep moves an upload on, and pastes the remote paths into the pane
// the files were dropped on once every file is there.
func (m *Model) dropStep(msg dropStepMsg) tea.Cmd {
	d := msg.d
	if msg.err != nil {
		m.setFlash(msg.err.Error()+" · nothing pasted", true)
		return nil
	}
	if d.cur < len(d.paths) {
		if len(d.ups) > 0 && d.ups[d.cur].Sent() > 0 {
			m.setFlash(d.progress(), false)
		}
		return d.step()
	}
	mach := m.machine(d.machine)
	var p *proto.PaneInfo
	if mach != nil {
		p = mach.pane(d.pane)
	}
	switch {
	case p == nil || p.State != proto.PaneRunning || !p.Created.Equal(d.created):
		m.setFlash("uploaded to "+d.label+", but the pane has closed · nothing pasted", true)
		return nil
	case mach.c == nil:
		m.setFlash("uploaded to "+d.label+", but it is "+mach.state.String()+" · nothing pasted", true)
		return nil
	}
	quoted := make([]string, len(d.remote))
	for i, r := range d.remote {
		quoted[i] = shellQuote(r, d.quote)
	}
	text := strings.Join(quoted, " ")
	if d.space {
		text += " "
	}
	mach.c.Notify(proto.MethodPaneSendText, proto.PaneSendTextParams{ID: d.pane, Text: text, Paste: true})
	if len(d.remote) == 1 {
		m.setFlash("uploaded to "+d.label+" · "+filepath.Base(d.remote[0]), false)
	} else {
		m.setFlash(fmt.Sprintf("uploaded %d files to %s", len(d.remote), d.label), false)
	}
	return nil
}

func mbText(n int64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d KB", (n+1023)>>10)
	}
	return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
}
