package phone

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/fleet"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// Reviewing a pane's branch from the phone: what the TUI's changes view
// and its harvest keys do — the files and commits, a file's diff hunk by
// hunk, staging, a note on a line for the agent, commit, push and a pull
// request. Every one is a call on the pane's own machine's server, the
// same calls the TUI and Conch.app make; the gateway runs no git or gh.

const (
	// capHarvest is branch.commit, branch.push and branch.pr, older than
	// the protocol's named capabilities, so proto has only the string.
	capHarvest = "branch.harvest.v1"
	// maxDiffLines bounds GET /api/diff: a phone reads a hunk at a time,
	// not a generated file.
	maxDiffLines = 2000
	// harvestTimeout covers a commit's hooks, a push and gh; the server
	// gives up on the network after two minutes.
	harvestTimeout = 150 * time.Second
)

// paneBranch is the pane a review route acts on: its machine's client, its
// ID as the phone has it, and the project and branch it is on.
type paneBranch struct {
	c               *client.Client
	id              string
	project, branch string
}

// branchOn resolves a pane to the branch it is on, after asking its
// machine's conch for caps.
func (g *Gateway) branchOn(rq *request, ref string, caps ...string) (paneBranch, *APIError) {
	if ref == "" {
		return paneBranch{}, apiErr(CodeBadRequest, "which pane? pane is missing")
	}
	c, pane, id, aerr := g.paneOn(rq.dev, ref)
	if aerr != nil {
		return paneBranch{}, aerr
	}
	if len(caps) > 0 {
		if aerr := g.lacks(fleet.MachineOf(id), c, caps...); aerr != nil {
			return paneBranch{}, aerr
		}
	}
	var list proto.PaneList
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodPaneList, nil, &list); aerr != nil {
		return paneBranch{}, aerr
	}
	i := slices.IndexFunc(list.Panes, func(p proto.PaneInfo) bool { return p.ID == pane })
	if i < 0 {
		return paneBranch{}, apiErr(CodeNotFound, "no pane "+id)
	}
	info := list.Panes[i]
	if info.ProjectID == "" || info.Branch == "" {
		return paneBranch{}, apiErr(CodeBadRequest, id+" is not on a branch of a project, so it has nothing to review")
	}
	return paneBranch{c: c, id: id, project: info.ProjectID, branch: info.Branch}, nil
}

// branchInfo is what project.list says of the pane's branch: false when
// it does not list it (yet).
func (g *Gateway) branchInfo(rq *request, pb paneBranch) (proto.BranchInfo, bool, *APIError) {
	var list proto.ProjectList
	if aerr := g.callOn(rq, pb.c, callTimeout, proto.MethodProjectList, nil, &list); aerr != nil {
		return proto.BranchInfo{}, false, aerr
	}
	for _, p := range list.Projects {
		if p.ID != pb.project {
			continue
		}
		for _, b := range p.Branches {
			if b.Name == pb.branch {
				return b, true, nil
			}
		}
	}
	return proto.BranchInfo{}, false, nil
}

// changes is GET /api/changes: project.changes, as the TUI's changes view
// reads it, with the branch's place against the base and its pull request
// from project.list.
func (g *Gateway) changes(rq *request) (any, *APIError) {
	pb, aerr := g.branchOn(rq, rq.r.URL.Query().Get("pane"))
	if aerr != nil {
		return nil, aerr
	}
	var ch proto.Changes
	if aerr := g.callOn(rq, pb.c, callTimeout, proto.MethodProjectChanges,
		proto.ChangesParams{ProjectID: pb.project, Branch: pb.branch}, &ch); aerr != nil {
		return nil, aerr
	}
	out := BranchChanges{Pane: pb.id, Branch: pb.branch, Base: ch.Base, Files: []ChangedFile{}, Commits: []CommitSummary{}}
	for _, f := range ch.Files {
		cf := ChangedFile{Path: f.Path, OldPath: f.OrigPath, Status: fileStatus(f.Code), Added: f.Added,
			Removed: f.Deleted, Staged: StagedNone, Binary: f.Binary}
		if ch.Worktree != "" { // a branch checked out nowhere has no index
			cf.Staged = stagedState(f)
		}
		out.Files = append(out.Files, cf)
	}
	for _, c := range ch.Commits {
		out.Commits = append(out.Commits, CommitSummary{SHA: c.Hash, Short: shortSHA(c.Hash), Subject: c.Subject,
			Author: c.Author, At: c.Time.UTC()})
	}
	// The rest only adds to the page: a branch project.list has not caught
	// up with is still its files and commits.
	if b, ok, _ := g.branchInfo(rq, pb); ok {
		out.Ahead, out.Behind = b.BaseAhead, b.BaseBehind
		if b.PR != nil && len(pb.c.MissingCapabilities([]string{capProjectPR})) == 0 {
			pr := pullRequest(b.Name, *b.PR)
			out.PR = &BranchPR{Number: pr.Number, URL: pr.URL, State: pr.State, Checks: pr.Checks}
		}
	}
	return out, nil
}

// fileStatus is git's status letter in words. A copy is a file added; a
// type change and a conflict are files modified.
func fileStatus(code string) string {
	switch code {
	case "A", "C":
		return FileAdded
	case "D":
		return FileDeleted
	case "R":
		return FileRenamed
	case "?":
		return FileUntracked
	}
	return FileModified
}

// stagedState is how much of an uncommitted file is in the index.
func stagedState(f proto.FileChange) string {
	switch {
	case f.Code == "?" || !f.Staged:
		return StagedNone
	case f.Unstaged:
		return StagedPartial
	}
	return StagedAll
}

func shortSHA(sha string) string { return sha[:min(len(sha), 7)] }

// checkPath refuses a path that is not a file in the worktree as
// /api/changes names one: git would take a leading - as an option and a
// leading : as pathspec magic (":/" is every file).
func checkPath(p string) *APIError {
	switch {
	case strings.TrimSpace(p) == "":
		return apiErr(CodeBadRequest, "which file? path is missing")
	case strings.HasPrefix(p, "-"), strings.HasPrefix(p, ":"), strings.HasPrefix(p, "/"),
		strings.ContainsRune(p, 0), slices.Contains(strings.Split(p, "/"), ".."):
		return apiErr(CodeBadRequest, strconv.Quote(p)+" is not a file in the worktree; send a path as /api/changes gives it")
	}
	return nil
}

var commitSHA = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

// fileDiff is GET /api/diff. Without commit it is the file's uncommitted
// work against the branch's last commit, staged and not, as the TUI's
// changes view shows it (project.diff); with commit, what that commit
// changed in it (project.commit).
func (g *Gateway) fileDiff(rq *request) (any, *APIError) {
	q := rq.r.URL.Query()
	path, commit := q.Get("path"), q.Get("commit")
	if aerr := checkPath(path); aerr != nil {
		return nil, aerr
	}
	if commit != "" && !commitSHA.MatchString(commit) {
		return nil, apiErr(CodeBadRequest, strconv.Quote(commit)+" is not a commit's SHA")
	}
	var caps []string
	if commit != "" {
		caps = []string{proto.CapCommitShow}
	}
	pb, aerr := g.branchOn(rq, q.Get("pane"), caps...)
	if aerr != nil {
		return nil, aerr
	}
	var wd worktreeDiff
	if commit != "" {
		var d proto.CommitDetail
		if aerr := g.callOn(rq, pb.c, callTimeout, proto.MethodProjectCommit,
			proto.CommitParams{ProjectID: pb.project, Hash: commit, File: path}, &d); aerr != nil {
			return nil, aerr
		}
		wd.add(path, parseDiff(d.Diff), false)
	} else if wd, aerr = g.uncommitted(rq, pb, path); aerr != nil {
		return nil, aerr
	}
	return wd.render(path), nil
}

// worktreeDiff is a file's diff read for the phone: its hunks, each
// knowing whether it is staged and the patch that moves it.
type worktreeDiff struct {
	file      *proto.FileChange // nil: nothing uncommitted (or a commit's diff)
	oldPath   string
	binary    bool
	truncated bool
	hunks     []hunk
}

// hunk is one hunk with what staging it needs: its text as a patch takes
// it, and the file header that goes before it.
type hunk struct {
	DiffHunk
	text, preamble string
}

// add takes a parsed diff's hunks, staged or not.
func (wd *worktreeDiff) add(path string, d parsedDiff, staged bool) {
	if d.oldPath != "" {
		wd.oldPath = d.oldPath
	}
	wd.binary = wd.binary || d.binary
	wd.truncated = wd.truncated || d.truncated
	for _, h := range d.hunks {
		h.Staged = staged
		h.ID = hunkID(path, staged, h.text)
		wd.hunks = append(wd.hunks, h)
	}
}

// uncommitted reads a file's uncommitted diff. It is project.diff — the
// last commit to the files on disk, staged and not, which is what the
// TUI and Conch.app show — and its hunks are staged when the whole file
// is. A file only part staged is two diffs, what is staged and what is
// not, read through branch.git as Conch.app's git panel runs it: one diff
// cannot say which of its hunks are in the index, nor give the patch that
// takes one back out.
func (g *Gateway) uncommitted(rq *request, pb paneBranch, path string) (worktreeDiff, *APIError) {
	var wd worktreeDiff
	var ch proto.Changes
	if aerr := g.callOn(rq, pb.c, callTimeout, proto.MethodProjectChanges,
		proto.ChangesParams{ProjectID: pb.project, Branch: pb.branch}, &ch); aerr != nil {
		return wd, aerr
	}
	i := slices.IndexFunc(ch.Files, func(f proto.FileChange) bool { return f.Path == path })
	if i < 0 {
		return wd, nil
	}
	f := ch.Files[i]
	wd.file, wd.oldPath, wd.binary = &f, f.OrigPath, f.Binary
	state := StagedNone
	if ch.Worktree != "" {
		state = stagedState(f)
	}
	if state == StagedPartial && len(pb.c.MissingCapabilities([]string{proto.CapBranchGit})) == 0 {
		for _, staged := range []bool{true, false} {
			d, aerr := g.gitDiff(rq, pb, path, staged)
			if aerr != nil {
				return wd, aerr
			}
			wd.add(path, d, staged)
		}
		return wd, nil
	}
	var d proto.DiffResult
	if aerr := g.callOn(rq, pb.c, callTimeout, proto.MethodProjectDiff,
		proto.DiffParams{ProjectID: pb.project, Branch: pb.branch, File: path}, &d); aerr != nil {
		return wd, aerr
	}
	wd.add(path, parseDiff(d.Diff), state == StagedAll)
	return wd, nil
}

// gitDiff is the file's staged diff (the index against the last commit)
// or its unstaged one (the files against the index), through branch.git.
// The prefixes are given because a person's diff.noprefix would make a
// patch git apply cannot take.
func (g *Gateway) gitDiff(rq *request, pb paneBranch, path string, staged bool) (parsedDiff, *APIError) {
	args := []string{"diff", "--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/"}
	if staged {
		args = append(args, "--cached")
	}
	var res proto.BranchGitResult
	if aerr := g.callOn(rq, pb.c, callTimeout, proto.MethodBranchGit, proto.BranchGitParams{ProjectID: pb.project,
		Branch: pb.branch, Commands: [][]string{append(args, "--", path)}}, &res); aerr != nil {
		return parsedDiff{}, aerr
	}
	if res.ExitCode != 0 {
		return parsedDiff{}, apiErr(CodeServerUnavailable, "git diff failed there: "+firstLine(res.Output))
	}
	// What it printed comes under a "$ git …" line, with anything git
	// warned about; the diff starts at its first file header.
	out := res.Output
	if i := strings.Index(out, "\ndiff "); i >= 0 {
		out = out[i+1:]
	} else {
		out = ""
	}
	d := parseDiff(out)
	d.truncated = d.truncated || res.Truncated
	return d, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// hunkID names a hunk by its file, its side of the index and its text, so
// it names the same hunk until the file or the index changes under it.
func hunkID(path string, staged bool, text string) string {
	side := "u"
	if staged {
		side = "s"
	}
	sum := sha256.Sum256([]byte(path + "\x00" + side + "\x00" + text))
	return "h" + hex.EncodeToString(sum[:8])
}

// render is the diff as the phone gets it, cut at maxDiffLines: whole
// hunks only, unless the first alone is longer.
func (wd worktreeDiff) render(path string) FileDiff {
	out := FileDiff{Path: path, OldPath: wd.oldPath, Binary: wd.binary, Truncated: wd.truncated, Hunks: []DiffHunk{}}
	n := 0
	for i, h := range wd.hunks {
		if n+len(h.Lines) > maxDiffLines {
			out.Truncated = true
			if i == 0 {
				h.Lines = h.Lines[:maxDiffLines]
				out.Hunks = append(out.Hunks, h.DiffHunk)
			}
			break
		}
		n += len(h.Lines)
		out.Hunks = append(out.Hunks, h.DiffHunk)
	}
	return out
}

// parsedDiff is a unified diff of one file cut into hunks.
type parsedDiff struct {
	oldPath           string
	binary, truncated bool
	hunks             []hunk
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// serverCut is the line gitx.Diff ends a diff it cut short with.
const serverCut = "… diff truncated"

// parseDiff cuts a unified diff into hunks, each with the file header
// before it. A hunk's lines are counted off its @@ line, so a removed
// line that reads like a header is still a line; one cut short at the
// end is dropped (unless it is all there is) and the diff marked cut.
func parseDiff(diff string) parsedDiff {
	var d parsedDiff
	text := strings.TrimSuffix(diff, "\n")
	if rest, ok := strings.CutSuffix(text, serverCut); ok {
		text, d.truncated = strings.TrimSuffix(rest, "\n"), true
	}
	if text == "" {
		return d
	}
	var (
		preamble         string
		cur              *hunk
		body             strings.Builder // cur's text: a big file's hunk is thousands of lines
		oldN, newN       int
		oldLeft, newLeft int
	)
	finish := func() {
		if cur != nil {
			cur.text = body.String()
			d.hunks = append(d.hunks, *cur)
			cur = nil
		}
	}
	for _, l := range strings.Split(text, "\n") {
		if cur != nil && (oldLeft > 0 || newLeft > 0 || strings.HasPrefix(l, `\`)) && hunkLine(l) {
			t := ""
			if len(l) > 1 {
				t = l[1:]
			}
			switch {
			case l == "" || l[0] == ' ':
				cur.Lines = append(cur.Lines, DiffLine{K: " ", Old: oldN, New: newN, T: t})
				oldN, newN, oldLeft, newLeft = oldN+1, newN+1, oldLeft-1, newLeft-1
			case l[0] == '+':
				cur.Lines = append(cur.Lines, DiffLine{K: "+", New: newN, T: t})
				newN, newLeft = newN+1, newLeft-1
			case l[0] == '-':
				cur.Lines = append(cur.Lines, DiffLine{K: "-", Old: oldN, T: t})
				oldN, oldLeft = oldN+1, oldLeft-1
			}
			// "\ No newline at end of file": the patch needs it, the phone
			// does not.
			body.WriteString(l + "\n")
			continue
		}
		if m := hunkHeader.FindStringSubmatch(l); m != nil {
			finish()
			cur = &hunk{DiffHunk: DiffHunk{Header: l, Lines: []DiffLine{}}, preamble: preamble}
			body.Reset()
			body.WriteString(l + "\n")
			oldN, oldLeft = atoiOr(m[1], 0), atoiOr(m[2], 1)
			newN, newLeft = atoiOr(m[3], 0), atoiOr(m[4], 1)
			continue
		}
		// A file header line: a new file's starts again.
		if cur != nil || strings.HasPrefix(l, "diff ") {
			finish()
			if strings.HasPrefix(l, "diff ") {
				preamble = ""
			}
		}
		preamble += l + "\n"
		switch {
		case strings.HasPrefix(l, "rename from "):
			d.oldPath = strings.TrimPrefix(l, "rename from ")
		case strings.HasPrefix(l, "Binary files "), l == "GIT binary patch":
			d.binary = true
		}
	}
	if cur != nil && (oldLeft > 0 || newLeft > 0) {
		// The diff ended inside it: cut short.
		d.truncated = true
		if len(d.hunks) > 0 {
			cur = nil
		}
	}
	finish()
	return d
}

// hunkLine is a line a hunk can hold: context (an empty one too, as some
// editors leave it), added, removed, or git's note about a last newline.
func hunkLine(l string) bool { return l == "" || strings.ContainsRune(" +-\\", rune(l[0])) }

func atoiOr(s string, or int) int {
	if s == "" {
		return or
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return or
	}
	return n
}

// stage is POST /api/stage: branch.stage with the file, or with the patch
// of one hunk — as read, so a hunk that changed since the phone read it is
// hunk_changed rather than something else staged.
func (g *Gateway) stage(rq *request) (any, *APIError) {
	var req StageRequest
	if aerr := rq.decode(&req); aerr != nil {
		return nil, aerr
	}
	if aerr := checkPath(req.Path); aerr != nil {
		return nil, aerr
	}
	pb, aerr := g.branchOn(rq, req.Pane, proto.CapBranchStage)
	if aerr != nil {
		return nil, aerr
	}
	wd, aerr := g.uncommitted(rq, pb, req.Path)
	if aerr != nil {
		return nil, aerr
	}
	if wd.file == nil {
		return nil, apiErr(CodeNotFound, req.Path+" has no uncommitted changes on "+pb.branch)
	}
	params := proto.BranchStageParams{ProjectID: pb.project, Branch: pb.branch, Unstage: req.Unstage}
	if req.Hunk == "" {
		params.Files = []string{req.Path}
		if wd.file.OrigPath != "" { // a rename is both its names
			params.Files = append(params.Files, wd.file.OrigPath)
		}
	} else {
		i := slices.IndexFunc(wd.hunks, func(h hunk) bool { return h.ID == req.Hunk })
		if i < 0 {
			return nil, apiErr(CodeHunkChanged, "that hunk of "+req.Path+" is not there any more; read the diff again")
		}
		h := wd.hunks[i]
		switch {
		case h.Staged == !req.Unstage:
			// Already where it was asked to go.
		case wd.file.Code == "?":
			params.Files = []string{req.Path} // a new file is one hunk
		default:
			params.Patch = h.preamble + h.text
		}
	}
	if len(params.Files) > 0 || params.Patch != "" {
		if aerr := g.callOn(rq, pb.c, callTimeout, proto.MethodBranchStage, params, nil); aerr != nil {
			if aerr.Code == CodeBadRequest && strings.Contains(aerr.Message, gitx.ErrPatchStale.Error()) {
				return nil, apiErr(CodeHunkChanged, "the file changed since its diff was read; nothing was staged")
			}
			return nil, aerr
		}
	}
	out := StageResponse{Pane: pb.id, Path: req.Path, Staged: StagedNone}
	var ch proto.Changes
	if aerr := g.callOn(rq, pb.c, callTimeout, proto.MethodProjectChanges,
		proto.ChangesParams{ProjectID: pb.project, Branch: pb.branch}, &ch); aerr != nil {
		return nil, aerr
	}
	if i := slices.IndexFunc(ch.Files, func(f proto.FileChange) bool { return f.Path == req.Path }); i >= 0 {
		out.Staged = stagedState(ch.Files[i])
	}
	return out, nil
}

// reviewNote is POST /api/review/note: review.set, as the TUI's changes
// view and Conch.app write one, so it waits in the branch's review for
// POST /api/review/send. The hunk the line is in, as the file is now, goes
// with it, so the note still says what it was about once the agent has
// rewritten those lines. A note on the old side is the hunk's with the
// line named in it: a note keeps a line only in the new file.
func (g *Gateway) reviewNote(rq *request) (any, *APIError) {
	var req NoteRequest
	if aerr := rq.decode(&req); aerr != nil {
		return nil, aerr
	}
	if aerr := checkPath(req.Path); aerr != nil {
		return nil, aerr
	}
	note := strings.TrimSpace(req.Note)
	switch {
	case req.Line < 1:
		return nil, apiErr(CodeBadRequest, "a note needs the line it is on, from 1")
	case req.Side != "" && req.Side != "new" && req.Side != "old":
		return nil, apiErr(CodeBadRequest, "side is new or old")
	case note == "":
		return nil, apiErr(CodeBadRequest, "a note needs some text")
	}
	pb, aerr := g.branchOn(rq, req.Pane, proto.CapReviewComments)
	if aerr != nil {
		return nil, aerr
	}
	old := req.Side == "old"
	rc := proto.ReviewComment{ProjectID: pb.project, Branch: pb.branch, File: req.Path, Note: note}
	// Only to quote: a diff that can't be read leaves the note without it.
	if wd, aerr := g.uncommitted(rq, pb, req.Path); aerr == nil {
		for _, h := range wd.hunks {
			if slices.ContainsFunc(h.Lines, func(l DiffLine) bool {
				return old && l.K != "+" && l.Old == req.Line || !old && l.K != "-" && l.New == req.Line
			}) {
				rc.Hunk = proto.QuoteHunk(h.text) // as the server keeps it
				break
			}
		}
	}
	if old {
		rc.Note = fmt.Sprintf("(line %d before the change) %s", req.Line, note)
	} else {
		rc.Line = req.Line
	}
	var list proto.ReviewList
	if aerr := g.callOn(rq, pb.c, callTimeout, proto.MethodReviewSet, rc, &list); aerr != nil {
		return nil, aerr
	}
	for _, got := range slices.Backward(list.Comments) {
		if got.File == rc.File && got.Line == rc.Line && got.Hunk == rc.Hunk && got.Note == rc.Note {
			return reviewNote(got), nil
		}
	}
	return reviewNote(rc), nil
}

// commit is POST /api/commit: branch.commit of what is staged, as git
// commit does, or with all of every change.
func (g *Gateway) commit(rq *request) (any, *APIError) {
	var req CommitRequest
	if aerr := rq.decode(&req); aerr != nil {
		return nil, aerr
	}
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		return nil, apiErr(CodeBadRequest, "a commit needs a message")
	}
	caps := []string{capHarvest}
	if !req.All {
		caps = append(caps, proto.CapBranchStage)
	}
	pb, aerr := g.branchOn(rq, req.Pane, caps...)
	if aerr != nil {
		return nil, aerr
	}
	var res proto.CommitResult
	if aerr := g.callOn(rq, pb.c, harvestTimeout, proto.MethodBranchCommit, proto.BranchCommitParams{
		ProjectID: pb.project, Branch: pb.branch, Message: msg, Staged: !req.All}, &res); aerr != nil {
		return nil, aerr
	}
	return CommitResponse{Pane: pb.id, SHA: res.Hash, Short: shortSHA(res.Hash), Subject: firstLine(msg)}, nil
}

// push is POST /api/push: branch.push, setting its upstream the first time.
// A remote with commits the branch lacks is bad_request, in the server's
// words; nothing is rebased from a phone.
func (g *Gateway) push(rq *request) (any, *APIError) {
	var req PushRequest
	if aerr := rq.decode(&req); aerr != nil {
		return nil, aerr
	}
	pb, aerr := g.branchOn(rq, req.Pane, capHarvest)
	if aerr != nil {
		return nil, aerr
	}
	// Where it goes is worked out as the server does, from before: the
	// push's answer does not say, and project.list catches up later.
	b, _, _ := g.branchInfo(rq, pb)
	var remotes []string
	if len(pb.c.MissingCapabilities([]string{proto.CapProjectLog})) == 0 {
		var log proto.LogResult
		if g.callOn(rq, pb.c, callTimeout, proto.MethodProjectLog, proto.LogParams{ProjectID: pb.project, Limit: 1}, &log) == nil {
			remotes = log.Remotes
		}
	}
	var res proto.BranchPushResult
	if aerr := g.callOn(rq, pb.c, harvestTimeout, proto.MethodBranchPush,
		proto.BranchPushParams{ProjectID: pb.project, Branch: pb.branch}, &res); aerr != nil {
		return nil, aerr
	}
	return PushResponse{Pane: pb.id, Branch: pb.branch, Remote: pushRemote(b.Upstream, remotes)}, nil
}

// pushRemote is the remote gitx.Push takes: the upstream's, else origin
// or the only one there is.
func pushRemote(upstream string, remotes []string) string {
	if upstream != "" {
		best := ""
		for _, r := range remotes {
			if strings.HasPrefix(upstream, r+"/") && len(r) > len(best) {
				best = r
			}
		}
		if best != "" {
			return best
		}
		if r, _, ok := strings.Cut(upstream, "/"); ok && len(remotes) == 0 {
			return r
		}
	}
	if len(remotes) == 1 {
		return remotes[0]
	}
	return "origin"
}

var (
	prNumber      = regexp.MustCompile(`/pull/(\d+)`)
	prOpenAlready = regexp.MustCompile(`pull request #(\d+) is already open for .*: (\S+)$`)
)

// openPR is POST /api/pr: branch.pr, which pushes the branch and opens a
// pull request into the base with gh — or the one already open, which
// the server would refuse to open again.
func (g *Gateway) openPR(rq *request) (any, *APIError) {
	var req PRRequest
	if aerr := rq.decode(&req); aerr != nil {
		return nil, aerr
	}
	pb, aerr := g.branchOn(rq, req.Pane, capHarvest)
	if aerr != nil {
		return nil, aerr
	}
	if len(pb.c.MissingCapabilities([]string{capProjectPR})) == 0 {
		if b, ok, _ := g.branchInfo(rq, pb); ok && b.PR != nil && b.PR.State == "OPEN" {
			return PRResponse{Pane: pb.id, Number: b.PR.Number, URL: b.PR.URL}, nil
		}
	}
	var res proto.BranchPRResult
	if aerr := g.callOn(rq, pb.c, harvestTimeout, proto.MethodBranchPR, proto.BranchPRParams{ProjectID: pb.project,
		Branch: pb.branch, Title: strings.TrimSpace(req.Title), Body: strings.TrimSpace(req.Body), Draft: req.Draft}, &res); aerr != nil {
		// Opened meanwhile, from somewhere the gateway had not heard of yet.
		if m := prOpenAlready.FindStringSubmatch(aerr.Message); m != nil && aerr.Code == CodeBadRequest {
			return PRResponse{Pane: pb.id, Number: atoiOr(m[1], 0), URL: m[2]}, nil
		}
		return nil, aerr
	}
	out := PRResponse{Pane: pb.id, URL: res.URL}
	if m := prNumber.FindStringSubmatch(res.URL); m != nil {
		out.Number = atoiOr(m[1], 0)
	}
	return out, nil
}
