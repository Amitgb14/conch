package phone

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/fleet"
	"github.com/Amitgb14/conch/internal/proto"
)

const (
	// maxAttach bounds POST /api/attach: a photo or a file from a phone,
	// not a folder. Every other route keeps maxBody.
	maxAttach = 25 << 20
	// attachDir is where attachments go, under the pane's folder; the
	// exclude line keeps them out of the repository's commits.
	attachDir     = ".conch/attachments"
	attachExclude = ".conch/attachments/"
	// attachNameMax is in characters, the extension kept within it.
	attachNameMax = 100
	// attachTimeout is the copy itself, which over ssh to another machine
	// is 25 MB as base64.
	attachTimeout = 5 * time.Minute
	// capFSUpload is fs.upload, older than the protocol's named
	// capabilities, so proto has only the string.
	capFSUpload = "fs.upload.v1"
)

// bodyLimits is the routes whose body may be more than maxBody.
var bodyLimits = map[string]int64{"/api/attach": maxAttach}

// attach is POST /api/attach: a file from the phone saved where the agent
// in a pane can open it — under .conch/attachments in its worktree (an
// agent) or its folder (a terminal) — through that machine's own server,
// fs.upload as `conch upload -to` sends one, so a pane on another machine
// gets it on that machine. The app puts the path in its reply.
func (g *Gateway) attach(rq *request) (any, *APIError) {
	q := rq.r.URL.Query()
	if q.Get("pane") == "" {
		return nil, apiErr(CodeBadRequest, "which pane? ?pane= is missing")
	}
	if strings.TrimSpace(q.Get("name")) == "" {
		return nil, apiErr(CodeBadRequest, "the file needs a name: ?name= is missing")
	}
	c, pane, id, aerr := g.paneOn(rq.dev, q.Get("pane"))
	if aerr != nil {
		return nil, aerr
	}
	// Without fs.transfer.v1 a server ignores the folder and lists only
	// folders, so nothing here would land where it was meant to.
	if aerr := g.lacks(fleet.MachineOf(id), c, capFSUpload, proto.CapFSTransfer); aerr != nil {
		return nil, aerr
	}
	var list proto.PaneList
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodPaneList, nil, &list); aerr != nil {
		return nil, aerr
	}
	i := slices.IndexFunc(list.Panes, func(p proto.PaneInfo) bool { return p.ID == pane })
	if i < 0 {
		return nil, apiErr(CodeNotFound, "no pane "+id)
	}
	info := list.Panes[i]
	name := attachName(q.Get("name"))
	out := AttachResponse{Pane: id, Size: int64(len(rq.body))}

	dir := g.paneDir(rq, c, info)
	var into string
	var top proto.FSList
	why := "it has no folder"
	if dir != "" {
		var err error
		if into, top, err = g.attachFolder(rq, c, dir); err != nil {
			why = err.Error()
		}
	}
	if into != "" {
		var taken proto.FSList
		if aerr := g.callOn(rq, c, callTimeout, proto.MethodFSList,
			proto.FSListParams{Path: into, Files: true, Hidden: true}, &taken); aerr != nil {
			return nil, aerr
		}
		name = freeName(name, taken.Entries)
	} else {
		// The server's uploads folder there, a folder of its own per file —
		// where a file dropped on a pane in the TUI goes.
		out.Note = "the file was saved in conch's uploads folder rather than the pane's: " + why
	}
	up := client.NewUpload(c, name, rq.body)
	if into != "" {
		up.Into(into, false) // a name taken meanwhile is numbered, never written over
	}
	ctx, cancel := context.WithTimeout(rq.r.Context(), attachTimeout)
	defer cancel()
	p, err := up.Run(ctx)
	if err != nil {
		return nil, fromServer(err)
	}
	out.Path = p
	if into != "" {
		if rel, ok := strings.CutPrefix(p, dir+"/"); ok {
			out.Rel = rel
		}
		if err := g.excludeAttachments(rq, c, dir, top); err != nil {
			g.logf("attach: keeping attachments out of git: %v", err)
		}
	}
	return out, nil
}

// paneDir is the folder a pane works in: an agent's worktree, as the
// project has it, else the pane's own folder. "" when it has none worth
// the name.
func (g *Gateway) paneDir(rq *request, c *client.Client, info proto.PaneInfo) string {
	dir := info.Cwd
	if isAgent(info) && info.ProjectID != "" && info.Branch != "" {
		var projects proto.ProjectList
		if g.callOn(rq, c, callTimeout, proto.MethodProjectList, nil, &projects) == nil {
			for _, p := range projects.Projects {
				if p.ID != info.ProjectID {
					continue
				}
				for _, w := range p.Worktrees {
					if w.Branch == info.Branch && w.Path != "" {
						dir = w.Path
					}
				}
			}
		}
	}
	if !path.IsAbs(dir) {
		return ""
	}
	return path.Clean(dir)
}

// attachFolder makes dir's .conch/attachments, a folder at a time, and
// returns it with dir's own listing (which says whether dir is the top of a
// git checkout). A step that is a symlink, or not a folder, is refused
// rather than followed: an attachment never lands outside dir.
func (g *Gateway) attachFolder(rq *request, c *client.Client, dir string) (string, proto.FSList, error) {
	var top proto.FSList
	cur := dir
	for n, step := range strings.Split(attachDir, "/") {
		var ls proto.FSList
		if aerr := g.callOn(rq, c, callTimeout, proto.MethodFSList, proto.FSListParams{Path: cur, Files: true, Hidden: true}, &ls); aerr != nil {
			return "", top, fmt.Errorf("%s can't be read: %s", cur, aerr.Message)
		}
		if n == 0 {
			top = ls
		}
		next := path.Join(cur, step)
		i := slices.IndexFunc(ls.Entries, func(e proto.FSEntry) bool { return e.Name == step })
		switch {
		case i >= 0 && ls.Entries[i].Symlink:
			return "", top, fmt.Errorf("%s is a symlink", next)
		case i >= 0 && !ls.Entries[i].Dir:
			return "", top, fmt.Errorf("%s is not a folder", next)
		case i < 0:
			// A listing cut short may have left it out; then mkdir says
			// it is there, and it could not be looked at, so it is not
			// used.
			if aerr := g.callOn(rq, c, callTimeout, proto.MethodFSMkdir, proto.FSMkdirParams{Path: next}, nil); aerr != nil {
				return "", top, fmt.Errorf("%s couldn't be made: %s", next, aerr.Message)
			}
		}
		cur = next
	}
	return cur, top, nil
}

// excludeAttachments adds .conch/attachments/ to the exclude file of the
// git repository dir is in (the common one, for a worktree), unless a line
// there already keeps it out. A tracked .gitignore is never touched. top is
// dir's listing.
func (g *Gateway) excludeAttachments(rq *request, c *client.Client, dir string, top proto.FSList) error {
	gitDir, err := g.gitDirOf(rq, c, dir, top)
	if err != nil || gitDir == "" {
		return err
	}
	info := path.Join(gitDir, "info")
	var ls proto.FSList
	if g.callOn(rq, c, callTimeout, proto.MethodFSList, proto.FSListParams{Path: info, Files: true, Hidden: true}, &ls) != nil {
		if aerr := g.callOn(rq, c, callTimeout, proto.MethodFSMkdir, proto.FSMkdirParams{Path: info}, nil); aerr != nil {
			return aerr
		}
	}
	text := ""
	if slices.ContainsFunc(ls.Entries, func(e proto.FSEntry) bool { return e.Name == "exclude" }) {
		var rd proto.FSReadResult
		if aerr := g.callOn(rq, c, callTimeout, proto.MethodFSRead, proto.FSReadParams{Path: path.Join(info, "exclude"), Whole: true}, &rd); aerr != nil {
			return aerr
		}
		if rd.Binary || rd.Truncated {
			return fmt.Errorf("%s/exclude is not a file to add a line to", info)
		}
		text = rd.Data
	}
	if excludes(text) {
		return nil
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += attachExclude + "\n"
	ctx, cancel := context.WithTimeout(rq.r.Context(), callTimeout)
	defer cancel()
	_, err = client.NewUpload(c, "exclude", []byte(text)).Into(info, true).Run(ctx)
	return err
}

// excludes reports whether an exclude file already keeps attachments out:
// the line itself, or one for all of .conch (which conch's handoffs add).
func excludes(text string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "/")
		switch strings.TrimSuffix(line, "/") {
		case ".conch", ".conch/attachments":
			return true
		}
	}
	return false
}

// gitDirOf finds the repository dir is in, from dir up, and returns its
// common git folder: .git itself, or for a worktree (.git a file naming its
// own) the repository's that it shares. "" when dir is in none.
func (g *Gateway) gitDirOf(rq *request, c *client.Client, dir string, ls proto.FSList) (string, error) {
	for d := dir; ; {
		i := slices.IndexFunc(ls.Entries, func(e proto.FSEntry) bool { return e.Name == ".git" })
		if i >= 0 {
			dotGit := path.Join(d, ".git")
			if ls.Entries[i].Dir {
				return dotGit, nil
			}
			own, err := g.readLine(rq, c, dotGit)
			if err != nil {
				return "", err
			}
			own, ok := strings.CutPrefix(own, "gitdir:")
			if !ok {
				return "", fmt.Errorf("%s doesn't name a git folder", dotGit)
			}
			own = strings.TrimSpace(own)
			if !path.IsAbs(own) {
				own = path.Join(d, own)
			}
			// A linked worktree's folder says where the shared one is;
			// a submodule's has no commondir and is its own.
			common, err := g.readLine(rq, c, path.Join(own, "commondir"))
			if err != nil || common == "" {
				return own, nil
			}
			if !path.IsAbs(common) {
				common = path.Join(own, common)
			}
			return common, nil
		}
		up := path.Dir(d)
		if up == d {
			return "", nil
		}
		d = up
		ls = proto.FSList{}
		if g.callOn(rq, c, callTimeout, proto.MethodFSList, proto.FSListParams{Path: d, Files: true, Hidden: true}, &ls) != nil {
			return "", nil // a folder above that can't be read: as far as can be told, no repository
		}
	}
}

// readLine is the first line of a small file there.
func (g *Gateway) readLine(rq *request, c *client.Client, p string) (string, error) {
	var rd proto.FSReadResult
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodFSRead, proto.FSReadParams{Path: p, Max: 4096}, &rd); aerr != nil {
		return "", aerr
	}
	line, _, _ := strings.Cut(rd.Data, "\n")
	return strings.TrimSpace(line), nil
}

// attachName makes the phone's file name one to save under: its last part
// only, without control or formatting characters (a right-to-left override
// would make a name read as another) or leading dots, at most attachNameMax
// characters with its extension kept. Nothing left is "attachment".
func attachName(name string) string {
	name = name[strings.LastIndexAny(name, `/\`)+1:]
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(name), "."))
	if name == "" {
		return "attachment"
	}
	if utf8.RuneCountInString(name) <= attachNameMax {
		return name
	}
	ext := path.Ext(name)
	if utf8.RuneCountInString(ext) > 16 {
		ext = ""
	}
	stem := []rune(strings.TrimSuffix(name, ext))
	return strings.TrimSpace(string(stem[:attachNameMax-utf8.RuneCountInString(ext)])) + ext
}

// freeName is name, or name-2, name-3 … before its extension, whichever is
// not among entries. Letter case is ignored, as macOS ignores it.
func freeName(name string, entries []proto.FSEntry) string {
	taken := map[string]bool{}
	for _, e := range entries {
		taken[strings.ToLower(e.Name)] = true
	}
	if !taken[strings.ToLower(name)] {
		return name
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 2; ; n++ {
		try := fmt.Sprintf("%s-%d%s", stem, n, ext)
		if !taken[strings.ToLower(try)] {
			return try
		}
	}
}
