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
	"github.com/Amitgb14/conch/internal/sessions"
)

// runImport brings conversations in from another app. Only Conductor so
// far: it kept its chats in a database of its own, which goes when its
// cloud shuts down or the app is removed, so each conversation is written
// out as a handoff document an agent can read and carry on from.
func runImport(args []string) error {
	if len(args) == 0 || args[0] != "conductor" {
		return errors.New("usage: conch import conductor [-db PATH] [-out DIR] [-n] [-json]\nconch import -h explains it")
	}
	fs := flag.NewFlagSet("import conductor", flag.ContinueOnError)
	home, _ := os.UserHomeDir()
	db := fs.String("db", sessions.ConductorDB(home), "Conductor's database")
	out := fs.String("out", filepath.Join(config.Dir(), "imports", "conductor"), "where to write the conversations")
	dry := fs.Bool("n", false, "say what would be written, and write nothing")
	asJSON := fs.Bool("json", false, "what was written, as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("conch import conductor takes no arguments, only flags: %q", fs.Arg(0))
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
	written, err := writeImported(all, *out, *dry)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(written)
	}
	printImported(written, all, *dry)
	return nil
}

// importedFile is one conversation written out.
type importedFile struct {
	Title     string `json:"title"`
	Agent     string `json:"agent"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Dir       string `json:"dir,omitempty"` // where to carry on: the workspace, or the repository
	Messages  int    `json:"messages"`
	File      string `json:"file"`
}

// writeImported writes each conversation that has something said in it to
// out/REPO/TITLE-ID.md, the same name every time, so importing again
// replaces rather than piles up.
func writeImported(all []sessions.Imported, out string, dry bool) ([]importedFile, error) {
	written := []importedFile{}
	for _, im := range all {
		if len(im.Turns) == 0 {
			continue
		}
		repo := im.Repo
		if repo == "" {
			repo = "unknown-repository"
		}
		id := im.Session.ID
		if len(id) > 8 {
			id = id[:8]
		}
		name := slugify(im.Session.Title)
		if name == "" {
			name = "conversation"
		}
		file := filepath.Join(out, slugify(repo), name+"-"+slugify(id)+".md")
		s := im.Session
		if s.Dir == "" {
			s.Dir = im.Workspace // only named in the document; it is gone
		}
		f := importedFile{Title: im.Session.Title, Agent: sessions.AgentName(im.Session.Agent), Repo: im.Repo,
			Branch: im.Session.Branch, Workspace: im.Workspace, Dir: im.Session.Dir, Messages: len(im.Turns), File: file}
		if !dry {
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				return nil, err
			}
			doc := "_Imported from Conductor by `conch import conductor`._\n\n" + sessions.Handoff(s, im.Turns)
			if err := os.WriteFile(file, []byte(doc), 0o600); err != nil {
				return nil, err
			}
		}
		written = append(written, f)
	}
	return written, nil
}

func printImported(written []importedFile, all []sessions.Imported, dry bool) {
	if len(written) == 0 {
		fmt.Printf("Conductor has %d conversations, and nothing was said in any of them: nothing to import.\n", len(all))
		return
	}
	verb := "wrote"
	if dry {
		verb = "would write"
	}
	fmt.Printf("%s %d of Conductor's conversations:\n\n", verb, len(written))
	for _, f := range written {
		facts := []string{f.Agent, fmt.Sprintf("%d message%s", f.Messages, plural(f.Messages))}
		if where := strings.Trim(f.Repo+" · "+f.Branch, " ·"); where != "" {
			facts = append(facts, where)
		}
		fmt.Printf("  %s  (%s)\n    %s\n", f.Title, strings.Join(facts, ", "), f.File)
	}
	if dry {
		fmt.Println("\nconch import conductor without -n writes them.")
		return
	}
	f := written[len(written)-1]
	dir := f.Dir
	if dir == "" {
		dir = "~/src/REPO"
	}
	fmt.Printf("\nTo carry one on, start an agent where it ran and point it at the file:\n\n  conch new -agent claude -cwd %s -- %s\n",
		shellWord(dir), shellWord("Read "+f.File+" and carry on from where it leaves off."))
	fmt.Println("\nOr drag the file into the agent's prompt, in conch or Conch.app.")
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
