package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// Copying files and folders between this computer and a machine from the
// Files view: U (or files dropped on it) sends them to the folder selected,
// D brings the selection back to the Downloads folder. They travel over
// the machine's own conch connection, as `conch upload` and `conch
// download` do, so an ssh host and a sandbox are the same: no address to
// find and no key to give.

// downloadsDir is where D puts what it brings back: ~/Downloads, or home
// on a computer without one. Tests point it elsewhere.
var downloadsDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	if st, err := os.Stat(filepath.Join(home, "Downloads")); err == nil && st.IsDir() {
		return filepath.Join(home, "Downloads")
	}
	return home
}

// fileCopy is one file or folder on its way. The pointer travels in
// copyStepMsg; only the command running a step touches it meanwhile.
type fileCopy struct {
	machine string
	label   string
	c       *client.Client
	ssh     string // a terminal whose ssh host it goes to or comes from
	home    string // that host's home, to say where it went

	// One of these: a copy there, or one back.
	local   string // an upload's path here
	dir     string // the folder there it goes to
	replace bool
	up      *client.Upload
	down    *client.Download
	remote  string // a download's path there
}

type copyStepMsg struct {
	fc   *fileCopy
	done bool
	err  error
}

// name is what is being copied, for messages.
func (fc *fileCopy) name() string {
	if fc.down != nil {
		return path.Base(fc.remote)
	}
	return filepath.Base(fc.local)
}

// start opens an upload — packing a folder, which takes a moment, so it is
// done here rather than in Update — and sends the first chunk.
func (fc *fileCopy) start() tea.Cmd {
	return func() tea.Msg {
		if fc.down == nil && fc.up == nil {
			up, err := client.OpenUpload(fc.c, fc.local)
			if err != nil {
				return copyStepMsg{fc: fc, err: err}
			}
			fc.up = up.Into(fc.dir, fc.replace).On(fc.ssh)
		}
		return fc.stepNow()
	}
}

func (fc *fileCopy) step() tea.Cmd {
	return func() tea.Msg { return fc.stepNow() }
}

func (fc *fileCopy) stepNow() copyStepMsg {
	var (
		done bool
		err  error
	)
	if fc.down != nil {
		ctx, cancel := context.WithTimeout(context.Background(), fc.down.Timeout())
		done, err = fc.down.Step(ctx)
		cancel()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), fc.up.Timeout())
		done, err = fc.up.Step(ctx)
		cancel()
	}
	var perr *proto.Error
	if errors.As(err, &perr) {
		err = errors.New(perr.Message)
	}
	return copyStepMsg{fc: fc, done: done, err: err}
}

// close lets go of what a copy holds here: an upload's file and a folder's
// pack, a download's partial file.
func (fc *fileCopy) close() {
	if fc.up != nil {
		fc.up.Close()
	}
	if fc.down != nil {
		fc.down.Close()
	}
}

// progress leads with the machine and percentage, as a drop's does.
func (fc *fileCopy) progress() string {
	var got, size int64
	verb := "uploading to "
	if fc.down != nil {
		verb = "downloading from "
		got, size = fc.down.Got(), fc.down.Size()
	} else if fc.up != nil {
		got, size = int64(fc.up.Sent()), int64(fc.up.Size())
	}
	pct := 100
	if size > 0 {
		pct = int(got * 100 / size)
	}
	return fmt.Sprintf("%s%s %d%% · %s", verb, fc.label, pct, fc.name())
}

// copyStep moves a copy on, and says where it landed once it has.
func (m *Model) copyStep(msg copyStepMsg) tea.Cmd {
	fc := msg.fc
	switch {
	case msg.err != nil:
		fc.close()
		m.setFlash(fc.name()+": "+msg.err.Error(), true)
		return nil
	case !msg.done:
		m.setFlash(fc.progress(), false)
		return fc.step()
	}
	fc.close()
	if fc.down != nil {
		m.setFlash("downloaded "+fc.name()+" to "+m.tildify(localMachine, fc.down.Path()), false)
		return nil
	}
	where := m.tildify(fc.machine, fc.up.Path())
	if fc.ssh != "" {
		where = tildeUnder(fc.home, fc.up.Path())
	}
	m.setFlash("uploaded "+fc.name()+" to "+fc.label+" · "+where, false)
	return m.filesRefreshMachine(fc.machine, fc.ssh)
}

// filesRefreshMachine lists again what every explorer on a machine shows,
// after something was copied there.
func (m *Model) filesRefreshMachine(mid, ssh string) tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.openTabs() {
		for _, l := range t.root.leaves() {
			if fv := l.files; fv != nil && fv.machine == mid && fv.ssh == ssh && fv.err == "" {
				cmds = append(cmds, fv.refresh(m))
			}
		}
	}
	return tea.Batch(cmds...)
}

// copyClient is the machine's connection for a copy, or why there is none:
// this computer needs no copying, and an older conch can't.
func (m *Model) copyClient(fv *filesView) (*client.Client, string, bool) {
	if fv.ssh != "" {
		return m.sshCopyClient(fv)
	}
	if fv.machine == localMachine {
		m.setFlash("these files are on this computer already · y copies a path", false)
		return nil, "", false
	}
	mach := m.machine(fv.machine)
	if mach == nil || mach.c == nil {
		m.setFlash(m.offlineText(fv.machine), true)
		return nil, "", false
	}
	if len(mach.c.MissingCapabilities([]string{proto.CapFSTransfer})) > 0 {
		m.setFlash(mach.label+"'s conch is too old to copy files — update it with `conch machine upgrade`", true)
		return nil, "", false
	}
	return mach.c, mach.label, true
}

// sshCopyClient is copyClient for a terminal's ssh host, which is never
// this computer's own files, whichever machine the terminal runs on. It is
// labelled by the host.
func (m *Model) sshCopyClient(fv *filesView) (*client.Client, string, bool) {
	mach := m.machine(fv.machine)
	if mach == nil || mach.c == nil {
		m.setFlash(m.offlineText(fv.machine), true)
		return nil, "", false
	}
	if len(mach.c.MissingCapabilities([]string{proto.CapFSTransfer, proto.CapFSSSH})) > 0 {
		m.setFlash(sshFilesTooOld, true)
		return nil, "", false
	}
	return mach.c, fv.host, true
}

// downloadEntry brings the selected file or folder back to this computer.
func (m *Model) downloadEntry(fv *filesView, l filesLine) tea.Cmd {
	c, label, ok := m.copyClient(fv)
	if !ok {
		return nil
	}
	fc := &fileCopy{machine: fv.machine, label: label, c: c, remote: fv.abs(l.rel), ssh: fv.ssh}
	fc.down = client.NewDownload(c, fc.remote, downloadsDir(), false).On(fv.ssh)
	m.setFlash("downloading from "+label+" · "+l.e.Name, false)
	return fc.start()
}

// uploadPaths sends files and folders here to the folder selected there.
func (m *Model) uploadPaths(fv *filesView, paths []string, replace bool) tea.Cmd {
	c, label, ok := m.copyClient(fv)
	if !ok {
		return nil
	}
	dir, rel := fv.uploadDir()
	cmds := []tea.Cmd{fv.reveal(m, rel)}
	for _, p := range paths {
		fc := &fileCopy{machine: fv.machine, label: label, c: c, local: p, dir: dir, replace: replace, ssh: fv.ssh, home: fv.home}
		cmds = append(cmds, fc.start())
	}
	m.setFlash("uploading to "+label+" · "+fv.tildify(*m, dir), false)
	return tea.Batch(cmds...)
}

// openUploadDialog asks which file or folder here to send to the folder
// selected there.
func (m *Model) openUploadDialog(fv *filesView) tea.Cmd {
	if _, _, ok := m.copyClient(fv); !ok {
		return nil
	}
	dir, _ := fv.uploadDir()
	text := []string{
		"A file or folder on this computer, copied into " + fv.tildify(*m, dir) + " there. A folder is copied whole.",
		"Dragging files onto this view does the same.",
	}
	d := newDialog(*m, " Upload to "+m.filesTitle(fv)+" ", text, []string{"Path here"}, []string{"~/"})
	d.fields[0].in.CursorEnd()
	d.addCheck("Replace", "write over what has the same name, rather than numbering the copy", false)
	d.submit = func(m *Model, v []string) tea.Cmd {
		local, err := expandHome(strings.TrimSpace(v[0]))
		if err == nil {
			_, err = os.Stat(local)
		}
		if err != nil {
			m.setFlash(err.Error(), true)
			return nil
		}
		return m.uploadPaths(fv, []string{local}, v[1] == "on")
	}
	m.overlay = d
	return d.focusCmd()
}

// expandHome turns a leading ~ into this computer's home folder.
func expandHome(p string) (string, error) {
	if p == "" {
		return "", errors.New("say which file or folder to upload")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Abs(p)
}
