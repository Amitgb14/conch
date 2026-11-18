package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sessions"
)

// runImport brings conversations in from another app. Only Conductor so
// far: it kept its chats in a database of its own, which goes when its
// cloud shuts down or the app is removed. A Claude conversation is written
// as a Claude Code session, which `claude --resume` carries on; a Codex
// one is Codex's own rollout, which Codex wrote as Conductor ran it and
// `codex resume` carries on; anything that cannot be — no rollout, nowhere
// to resume — is written out as a handoff document an agent can read and
// carry on from.
// What was written is kept in imports/conductor/state.json, so importing
// again adds only what is new.
func runImport(args []string) error {
	const usage = "usage: conch import conductor [-db PATH] [-out DIR] [-ids ID,… [-dir FOLDER]] [-project=false] [-resumable=false] [-markdown] [-n] [-json]\nconch import -h explains it"
	if len(args) == 0 || args[0] != "conductor" {
		return errors.New(usage)
	}
	fs := flag.NewFlagSet("import conductor", flag.ContinueOnError)
	home, _ := os.UserHomeDir()
	db := fs.String("db", sessions.ConductorDB(home), "Conductor's database")
	out := fs.String("out", filepath.Join(config.Dir(), "imports", "conductor"), "where to write the Markdown copies")
	resumable := fs.Bool("resumable", true, "write each Claude conversation as a Claude Code session it resumes; false writes only Markdown")
	markdown := fs.Bool("markdown", false, "also write each conversation as a Markdown handoff, as conch did before")
	dry := fs.Bool("n", false, "say what would be written, and write nothing")
	asJSON := fs.Bool("json", false, "what was written, as JSON")
	ids := fs.String("ids", "", "only these conversations, by id, comma-separated (as -n -json gives them)")
	dir := fs.String("dir", "", "with -ids: resume those conversations in this folder, for ones whose workspace and repository are gone")
	project := fs.Bool("project", true, "add each repository they ran in to conch as a project, so they show under its branches")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("conch import conductor takes no arguments, only flags: %q", fs.Arg(0))
	}
	statePath := filepath.Join(config.Dir(), "imports", "conductor", "state.json")
	st, err := sessions.LoadImportState(statePath)
	if err != nil {
		return fmt.Errorf("%w\nconch keeps what it imported there; move it aside to import everything again", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	all, err := sessions.ReadConductor(ctx, *db)
	if err != nil {
		if _, serr := os.Stat(*db); serr != nil {
			return fmt.Errorf("%w\nConductor keeps it there on a Mac; give another place with -db", err)
		}
		return err
	}
	if all, err = chooseImported(all, *ids); err != nil {
		return err
	}
	place := ""
	if *dir != "" {
		if place, err = resumeFolder(*dir, *ids, *resumable, all, st); err != nil {
			return err
		}
	}
	imp := importer{out: *out, dry: *dry, resumable: *resumable, markdown: *markdown, state: st, dir: place,
		claude: sessions.ClaudeWriter{Env: sessions.CurrentEnv()}}
	written, err := imp.run(ctx, all)
	if err != nil {
		return err
	}
	if !*dry && imp.changed {
		if err := st.Save(statePath); err != nil {
			return err
		}
	}
	var added []proto.ProjectInfo
	var addErr error
	if !*dry && *project {
		added, addErr = addImportedProjects(written)
	}
	if *asJSON {
		if addErr != nil {
			fmt.Fprintf(os.Stderr, "not added to conch: %v\n", addErr)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(written)
	}
	printImported(written, all, *dry)
	printAddedProjects(added, addErr, written)
	return nil
}

// addImportedProjects puts each repository the conversations ran in into
// conch as a project, so they are there afterwards — its Conductor
// workspaces are its worktrees, so they show as its branches, each with
// its conversations in its sessions. A project already in conch is
// answered as it is. Only a running server is asked: an import is no
// reason to start one, and adding the project later shows the same.
func addImportedProjects(written []importedFile) ([]proto.ProjectInfo, error) {
	var roots []string
	seen := map[string]bool{}
	for _, f := range written {
		if f.root == "" || seen[f.root] {
			continue
		}
		seen[f.root] = true
		if st, err := os.Stat(f.root); err == nil && st.IsDir() {
			roots = append(roots, f.root)
		}
	}
	if len(roots) == 0 {
		return nil, nil
	}
	c, err := connect(false)
	if err != nil {
		return nil, fmt.Errorf("no conch server is running (%v)", err)
	}
	defer c.Close()
	var added []proto.ProjectInfo
	byRoot := map[string]string{}
	for _, r := range roots {
		var info proto.ProjectInfo
		if err := call(c, proto.MethodProjectAdd, proto.ProjectAddParams{Path: r}, &info); err != nil {
			return added, fmt.Errorf("%s: %w", r, err)
		}
		added = append(added, info)
		byRoot[r] = info.ID
	}
	for i := range written {
		written[i].Project = byRoot[written[i].root]
	}
	return added, nil
}

func printAddedProjects(added []proto.ProjectInfo, err error, written []importedFile) {
	if len(added) > 0 {
		fmt.Println("\nIn conch now, with their conversations under the branches' sessions:")
		for _, p := range added {
			fmt.Printf("  %s  %s\n", p.Name, p.Path)
		}
	}
	if err != nil {
		var roots []string
		seen := map[string]bool{}
		for _, f := range written {
			if f.root != "" && f.Project == "" && !seen[f.root] {
				seen[f.root] = true
				roots = append(roots, shellWord(f.root))
			}
		}
		fmt.Printf("\nNot added to conch: %v.\n", err)
		for _, r := range roots {
			fmt.Printf("  conch project add %s\n", r)
		}
	}
}

// importedFile is one conversation imported.
type importedFile struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Agent     string `json:"agent"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Dir       string `json:"dir,omitempty"` // where to carry on: the workspace, or the repository
	Messages  int    `json:"messages"`
	// Status is a sessions.ImportStatus for a session of the agent's own,
	// or "markdown" for a conversation written only as a handoff, Why
	// saying why.
	Status      string `json:"status"`
	Why         string `json:"why,omitempty"`
	AgentName   string `json:"agentName"`         // the agent as conch names it: "claude", "codex"
	Session     string `json:"session,omitempty"` // the agent's session id, to resume
	SessionFile string `json:"sessionFile,omitempty"`
	// ResumeArgs are the agent's own words that resume it, after `conch
	// new -agent AGENT -cwd DIR --`: ["--resume", ID] for Claude Code,
	// ["resume", ID] for Codex. Older conches said none; Claude's then.
	ResumeArgs []string `json:"resumeArgs,omitempty"`
	Added      int      `json:"added,omitempty"`
	// Project is the conch project its repository was added to as.
	Project string `json:"project,omitempty"`
	root    string // the repository's folder, as Conductor knew it
	File    string `json:"file,omitempty"` // the Markdown handoff
	// Where it carries on: "workspace" while that folder is there,
	// "repository" once only the repository is, "chosen" in a folder the
	// person picked (-dir), "gone" when there is nowhere.
	Where    string    `json:"where"`
	LastUsed time.Time `json:"last_used,omitzero"`
	// Imported: its file is there, from this import or an earlier one.
	Imported bool `json:"imported"`
}

// chooseImported keeps the conversations ids names, in Conductor's order;
// all of them for "". An id Conductor does not have is an error, since
// the person chose it from a list.
func chooseImported(all []sessions.Imported, ids string) ([]sessions.Imported, error) {
	if strings.TrimSpace(ids) == "" {
		return all, nil
	}
	want := map[string]bool{}
	for _, id := range strings.Split(ids, ",") {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	var out []sessions.Imported
	for _, im := range all {
		if want[im.Session.ID] {
			out = append(out, im)
			delete(want, im.Session.ID)
		}
	}
	for id := range want {
		return nil, fmt.Errorf("no conversation %q in Conductor's database", id)
	}
	return out, nil
}

// resumeFolder checks -dir: given with -ids, a folder that is there
// (named as Claude Code will see it, links resolved, since its sessions
// are filed by that path), and none of the chosen imported already to
// resume somewhere that is still there — that session is where it carries
// on now, and a second would split it in two.
func resumeFolder(dir, ids string, resumable bool, chosen []sessions.Imported, st sessions.ImportState) (string, error) {
	if strings.TrimSpace(ids) == "" {
		return "", errors.New("-dir places only the conversations -ids names")
	}
	if !resumable {
		return "", errors.New("-dir is where a conversation resumes, and -resumable=false writes only Markdown")
	}
	abs, err := filepath.Abs(dir)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
	if err != nil {
		return "", fmt.Errorf("-dir %s: %w", dir, err)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("-dir %s is not a folder", dir)
	}
	for _, im := range chosen {
		rec, ok := st[im.Session.ID]
		if !ok || rec.Dir == abs {
			continue
		}
		// Where its session resumes now: the folder chosen for it, or,
		// for one written where it ran, that folder while it is there.
		where := rec.Dir
		if where == "" {
			where = im.Session.Dir
		}
		if where != "" && isFolder(where) {
			return "", fmt.Errorf("%q was imported already and resumes in %s; -dir places only one with nowhere to resume", im.Session.Title, where)
		}
		// Its folder is gone, so its session cannot resume anywhere: it
		// is written again in the new one, and the old file left alone.
		delete(st, im.Session.ID)
	}
	return abs, nil
}

// importer imports conversations: resumable where an agent's writer can
// take them, as Markdown otherwise or as well.
type importer struct {
	out                      string
	dir                      string // -dir: where the chosen resume, if given
	dry, resumable, markdown bool
	state                    sessions.ImportState
	claude                   sessions.ClaudeWriter
	changed                  bool // state has something to save
}

func (imp *importer) run(ctx context.Context, all []sessions.Imported) ([]importedFile, error) {
	written := []importedFile{}
	names := importedNames(all, imp.out)
	for i, im := range all {
		if len(im.Turns) == 0 {
			continue
		}
		// A folder chosen for it, now or by an earlier import, is where
		// its session is: while that folder is there, it carries on there.
		if imp.dir != "" {
			im.Session.Dir = imp.dir
		} else if rec, ok := imp.state[im.Session.ID]; ok && rec.Dir != "" && rec.Dir != im.Session.Dir && isFolder(rec.Dir) {
			im.Session.Dir = rec.Dir
		}
		f := importedFile{ID: im.Session.ID, Where: importedWhere(im), LastUsed: im.Session.Updated,
			Title: im.Session.Title, Agent: sessions.AgentName(im.Session.Agent), Repo: im.Repo,
			Branch: im.Session.Branch, Workspace: im.Workspace, Dir: im.Session.Dir, Messages: len(im.Turns),
			Status: "markdown", AgentName: im.Session.Agent, root: im.Root}
		_, seen := imp.state[im.Session.ID]
		wantMarkdown := !imp.resumable || imp.markdown
		switch {
		case !imp.resumable:
		case im.Session.Agent == "codex":
			// Conductor runs Codex itself, which keeps its own rollout:
			// nothing to write, only to find.
			rollout := sessions.CodexRollout(imp.claude.Env, im.AgentSession)
			switch {
			case rollout == "":
				f.Why = "Codex has no session of it on this Mac"
				wantMarkdown = true
			case im.Session.Dir == "":
				f.Why = "Codex keeps it, but its workspace and repository are both gone"
				f.Session = im.AgentSession // what Resume In… resumes
				wantMarkdown = true
			default:
				f.Status, f.Session, f.SessionFile = string(sessions.ImportFound), im.AgentSession, rollout
				if imp.dir != "" && !imp.dry {
					imp.state[im.Session.ID] = sessions.ImportRecord{Agent: "codex", AgentSessionID: im.AgentSession,
						Path: rollout, ImportedAt: time.Now(), Dir: imp.dir}
					imp.changed = true
				}
			}
		case im.Session.Agent != imp.claude.Agent():
			f.Why = sessions.AgentName(im.Session.Agent) + " conversations come over as Markdown for now"
			wantMarkdown = true
		case imp.original(&f, im, seen):
			// Claude Code kept it itself, where it ran: resumed from there.
		default:
			moving, err := imp.refile(im)
			if err != nil {
				f.Why = "its session is filed for a folder that is gone, and could not be moved: " + err.Error()
				wantMarkdown = true
				break
			}
			o := imp.resume(ctx, im)
			if o.Status == sessions.ImportFailed {
				f.Why = "Claude Code cannot resume it: " + o.Err.Error()
				if seen {
					f.Why = "could not add what is new to its Claude Code session: " + o.Err.Error()
				}
				wantMarkdown = true
				break
			}
			f.Status, f.Why, f.Added = string(o.Status), o.Why, o.Added
			f.Session, f.SessionFile = o.AgentSessionID, o.Path
			if moving != "" {
				f.SessionFile = moving
				if f.Why == "" && imp.dry {
					f.Why = "its worktree is gone: importing it moves its session to where it resumes now"
				} else if f.Why == "" {
					f.Why = "its worktree is gone: its session was moved to where it resumes now"
				}
			}
		}
		// Imported: there is something of it already, from an earlier
		// import, or this one wrote it — or its agent kept it itself.
		f.Imported = seen || (!imp.dry && f.Session != "") || f.Status == string(sessions.ImportFound)
		if f.Session != "" && f.Status != "markdown" {
			f.ResumeArgs = resumeWords(f.AgentName, f.Session)
		}
		if wantMarkdown {
			if _, err := os.Stat(names[i]); err == nil {
				f.Imported = true
			}
			if err := imp.writeMarkdown(im, names[i]); err != nil {
				return nil, err
			}
			f.File = names[i]
		}
		written = append(written, f)
	}
	return written, nil
}

// original uses the session Claude Code kept itself, as it ran under
// Conductor, rather than writing a second: it has everything, and is the
// one Claude Code lists. Only while the folder it ran in is there, since
// Claude Code resumes it from there and nowhere else; not for one placed
// with -dir. A copy an earlier import wrote is dropped if nobody has
// carried it on since — it is the same conversation twice in Sessions —
// and kept, and used as before, if somebody has.
func (imp *importer) original(f *importedFile, im sessions.Imported, seen bool) bool {
	if imp.dir != "" {
		return false
	}
	path, cwd := imp.claude.Original(im.AgentSession)
	if path == "" || !isFolder(cwd) {
		return false
	}
	if seen {
		rec := imp.state[im.Session.ID]
		if rec.Path == path {
			// The record is of the original itself: nothing to drop.
		} else if fi, err := os.Stat(rec.Path); err == nil && fi.Size() != rec.Size {
			return false // carried on in conch's copy: that one goes on
		} else if !imp.dry {
			if err == nil {
				if os.Remove(rec.Path) != nil {
					return false
				}
			}
			delete(imp.state, im.Session.ID)
			imp.changed = true
		}
	}
	im.Session.Dir = cwd
	f.Dir, f.Where = cwd, importedWhere(im)
	f.Status, f.Session, f.SessionFile = string(sessions.ImportFound), im.AgentSession, path
	return true
}

// refile moves a session an earlier import wrote into the project folder
// for where the conversation carries on now. Claude Code finds a session
// only in the folder of the directory it is resumed in, so one written for
// a worktree since removed resumed nowhere, though the import offered it
// in the repository. Moved whole, never rewritten: somebody may have
// carried it on. With -n nothing moves, and the path it would go to is
// returned for the output; "" when nothing needs to move.
func (imp *importer) refile(im sessions.Imported) (string, error) {
	rec, ok := imp.state[im.Session.ID]
	if !ok || rec.Agent != imp.claude.Agent() || im.Session.Dir == "" || imp.claude.Filed(rec.Path, im.Session.Dir) {
		return "", nil
	}
	if _, err := os.Stat(rec.Path); err != nil {
		return "", nil // removed since: ImportPlan says so
	}
	if imp.dry {
		return filepath.Join(imp.claude.Folder(im.Session.Dir), filepath.Base(rec.Path)), nil
	}
	to, err := imp.claude.Move(rec.Path, im.Session.Dir)
	if err != nil {
		return "", err
	}
	rec.Path, rec.Dir = to, im.Session.Dir
	imp.state[im.Session.ID] = rec
	imp.changed = true
	return to, nil
}

// resumeWords are an agent's own words that resume session id.
func resumeWords(agent, id string) []string {
	if agent == "codex" {
		return []string{"resume", id}
	}
	return []string{"--resume", id}
}

// resume writes im as a Claude Code session, or with -n says whether it
// would.
func (imp *importer) resume(ctx context.Context, im sessions.Imported) sessions.ImportOutcome {
	if !imp.dry {
		o := sessions.ImportResumable(ctx, im, imp.claude, imp.state, time.Now())
		imp.changed = imp.changed || o.Status == sessions.ImportWritten || o.Status == sessions.ImportAdded
		return o
	}
	o := sessions.ImportPlan(im, imp.state)
	if o.Status == sessions.ImportWritten {
		if err := imp.claude.Check(ctx, im, im.Session.Dir); err != nil {
			return sessions.ImportOutcome{Status: sessions.ImportFailed, Err: err}
		}
	}
	return o
}

// writeMarkdown writes im's handoff to file, named by importedNames: the
// same name every time, so importing again replaces rather than piles up.
func (imp *importer) writeMarkdown(im sessions.Imported, file string) error {
	if imp.dry {
		return nil
	}
	s := im.Session
	if s.Dir == "" {
		s.Dir = im.Workspace // only named in the document; it is gone
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	doc := "_Imported from Conductor by `conch import conductor`._\n\n" + sessions.Handoff(s, im.Turns)
	return os.WriteFile(file, []byte(doc), 0o600)
}

// importedWhere is where a conversation carries on: "workspace" while that
// folder is there, "repository" once only the repository is, "chosen" in
// a folder the person picked (-dir), "gone" when there is nowhere.
func importedWhere(im sessions.Imported) string {
	switch {
	case im.Session.Dir == "":
		return "gone"
	case im.Session.Dir == im.Workspace:
		return "workspace"
	case im.Session.Dir == im.Root:
		return "repository"
	}
	return "chosen"
}

func isFolder(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// importedNames is the file each conversation is written to:
// out/REPO/TITLE-ID8.md, ID8 the first eight characters of its id. Two
// conversations of one title can share those — Conductor's ids are
// time-ordered, so two started close together often do — and the second
// would overwrite the first. So where names collide, the conversation with
// the smallest id (the oldest) keeps the short name, as it had before the
// other existed, and the rest are named by their whole id: a name that
// stays the same from one import to the next, which re-importing relies on.
func importedNames(all []sessions.Imported, out string) []string {
	short := func(im sessions.Imported, whole bool) string {
		repo := im.Repo
		if repo == "" {
			repo = "unknown-repository"
		}
		id := im.Session.ID
		if !whole && len(id) > 8 {
			id = id[:8]
		}
		name := slugify(im.Session.Title)
		if name == "" {
			name = "conversation"
		}
		return filepath.Join(out, slugify(repo), name+"-"+slugify(id)+".md")
	}
	names := make([]string, len(all))
	first := map[string]string{} // short name → the smallest id given it
	for i, im := range all {
		names[i] = short(im, false)
		if len(im.Turns) == 0 {
			continue // never written, so it takes no name from another
		}
		if id, taken := first[names[i]]; !taken || im.Session.ID < id {
			first[names[i]] = im.Session.ID
		}
	}
	for i, im := range all {
		if id := first[names[i]]; len(im.Turns) > 0 && id != im.Session.ID {
			names[i] = short(im, true)
		}
	}
	return names
}

func printImported(written []importedFile, all []sessions.Imported, dry bool) {
	if len(written) == 0 {
		fmt.Printf("Conductor has %d conversations, and nothing was said in any of them: nothing to import.\n", len(all))
		return
	}
	verb := "imported"
	if dry {
		verb = "would import"
	}
	fmt.Printf("%s %d of Conductor's conversations:\n\n", verb, len(written))
	var handoff *importedFile
	for i, f := range written {
		facts := []string{f.Agent, fmt.Sprintf("%d message%s", f.Messages, plural(f.Messages))}
		if where := strings.Trim(f.Repo+" · "+f.Branch, " ·"); where != "" {
			facts = append(facts, where)
		}
		if w := whereWords(f.Where); w != "" {
			facts = append(facts, w)
		}
		if dry && f.Imported {
			facts = append(facts, "imported already")
		}
		fmt.Printf("  %s  (%s)\n", f.Title, strings.Join(facts, ", "))
		if dry {
			// What -ids takes: the list is what somebody chooses from.
			fmt.Printf("    id %s\n", f.ID)
		}
		words := make([]string, len(f.ResumeArgs))
		for i, w := range f.ResumeArgs {
			words[i] = shellWord(w)
		}
		resume := "conch new -agent " + f.AgentName + " -cwd " + shellWord(f.Dir) + " -- " + strings.Join(words, " ")
		switch sessions.ImportStatus(f.Status) {
		case sessions.ImportFound:
			fmt.Printf("    resumes in %s, which kept it itself: %s\n", f.Agent, resume)
		case sessions.ImportWritten:
			if dry {
				fmt.Println("    would resume in Claude Code")
			} else {
				fmt.Printf("    resumes in Claude Code: %s\n", resume)
			}
		case sessions.ImportAdded:
			if dry {
				fmt.Printf("    would add %d new message%s to its Claude Code session\n", f.Added, plural(f.Added))
			} else {
				fmt.Printf("    added %d new message%s; resumes in Claude Code: %s\n", f.Added, plural(f.Added), resume)
			}
		case sessions.ImportUnchanged:
			fmt.Printf("    already imported, nothing new; resumes in Claude Code: %s\n", resume)
		case sessions.ImportCarriedOn:
			fmt.Printf("    left alone: %s\n", f.Why)
		default:
			if f.Why != "" {
				fmt.Printf("    as Markdown: %s\n      %s\n", f.Why, f.File)
			} else {
				fmt.Printf("    as Markdown: %s\n", f.File)
			}
		}
		switch sessions.ImportStatus(f.Status) {
		case sessions.ImportWritten, sessions.ImportAdded, sessions.ImportUnchanged, sessions.ImportFound:
			if f.Why != "" {
				fmt.Printf("    (%s)\n", f.Why)
			}
		}
		if f.File != "" && f.Status != "markdown" {
			fmt.Printf("    and as Markdown: %s\n", f.File)
		}
		if f.File != "" && (handoff == nil || handoff.Dir == "" || f.Dir != "") {
			handoff = &written[i]
		}
	}
	if dry {
		fmt.Println("\nconch import conductor without -n imports them; -ids ID,… only those.")
		return
	}
	if handoff == nil {
		return
	}
	dir, agent := handoff.Dir, handoff.AgentName
	if dir == "" {
		dir = "~/src/REPO"
	}
	fmt.Printf("\nTo carry on from a Markdown copy, start an agent where it ran and point it at the file:\n\n  conch new -agent %s -cwd %s -- %s\n",
		agent, shellWord(dir), shellWord("Read "+handoff.File+" and carry on from where it leaves off."))
	fmt.Println("\nOr drag the file into the agent's prompt, in conch or Conch.app.")
}

// whereWords says where a conversation can no longer be carried on: ""
// while its worktree is there.
func whereWords(where string) string {
	switch where {
	case "workspace":
		return ""
	case "repository":
		return "worktree removed"
	case "chosen":
		return "worktree and repository gone, resumes in a folder you chose"
	}
	return "worktree and repository gone"
}

// slugify makes a file name of a title: lower case letters, digits and
// dashes, at most 60 of them.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		} else {
			dash = true
		}
		if b.Len() >= 60 {
			break
		}
	}
	return b.String()
}

// shellWord quotes a word for a shell when it needs it.
func shellWord(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("/._-~+=:@", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
