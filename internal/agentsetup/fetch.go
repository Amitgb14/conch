package agentsetup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A skill fetched from GitHub: what people share skills as, and what the
// library could only take as a folder already on the machine. It is
// downloaded with a shallow `git clone` — nothing in it runs — and the
// skill's folder copied into conch's library, where each agent gets a link
// to it as with any library skill. The source is kept beside it, so the
// same skill can be fetched again.

// SkillSource is where on GitHub a skill is: a repository, a ref in it,
// and the folder holding its SKILL.md ("" for the repository's own, or for
// whichever single one it has).
type SkillSource struct {
	Owner, Repo, Ref, Path string
}

// String is the source as conch writes it: owner/repo[/path][@ref].
func (s SkillSource) String() string {
	out := s.Owner + "/" + s.Repo
	if s.Path != "" {
		out += "/" + s.Path
	}
	if s.Ref != "" {
		out += "@" + s.Ref
	}
	return out
}

var (
	ghName    = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	ghRef     = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	fetchName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// ParseSkillSource reads a skill's place on GitHub: owner/repo, with a
// folder after it and @ref at the end, or the same as a github.com URL —
// a repository's, or a folder's (…/tree/REF/PATH). A path on this machine
// (~/…, /…, ./…) is not one; ok is false for it and for anything else.
func ParseSkillSource(s string) (SkillSource, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, ".") {
		return SkillSource{}, false
	}
	url := false
	for _, p := range []string{"https://github.com/", "http://github.com/", "github.com/"} {
		if strings.HasPrefix(s, p) {
			s, url = strings.TrimPrefix(s, p), true
			break
		}
	}
	if strings.Contains(s, "://") {
		return SkillSource{}, false
	}
	s = strings.TrimSuffix(s, "/")
	ref := ""
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s, ref = s[:i], s[i+1:]
		if ref == "" || !ghRef.MatchString(ref) || strings.Contains(ref, "..") {
			return SkillSource{}, false
		}
	}
	parts := strings.Split(s, "/")
	if len(parts) < 2 || !ghName.MatchString(parts[0]) || !ghName.MatchString(parts[1]) {
		return SkillSource{}, false
	}
	src := SkillSource{Owner: parts[0], Repo: strings.TrimSuffix(parts[1], ".git"), Ref: ref}
	rest := parts[2:]
	// github.com/o/r/tree/REF/PATH (or blob/REF/PATH/SKILL.md): the URL a
	// browser shows for a folder.
	if url && len(rest) >= 2 && (rest[0] == "tree" || rest[0] == "blob") {
		if ref == "" {
			src.Ref = rest[1]
		}
		rest = rest[2:]
		if len(rest) > 0 && rest[len(rest)-1] == "SKILL.md" {
			rest = rest[:len(rest)-1]
		}
	}
	for _, p := range rest {
		if p == "" || p == "." || p == ".." {
			return SkillSource{}, false
		}
	}
	src.Path = strings.Join(rest, "/")
	return src, true
}

// FetchedSkill is a skill brought into the library from GitHub.
type FetchedSkill struct {
	Name   string
	Path   string // its folder in conch's library, on this machine
	Source string // owner/repo[/path][@ref], to fetch it again
}

// FetchError is a repository that holds several skills, none named: the
// person picks one of Found.
type FetchError struct {
	Source string
	Found  []string
}

func (e *FetchError) Error() string {
	return fmt.Sprintf("%s holds %d skills; name one: %s", e.Source, len(e.Found), strings.Join(e.Found, ", "))
}

// githubBase is where repositories are cloned from; tests point it at
// local ones (CONCH_GITHUB_URL), so no test reaches the network.
func githubBase() string {
	if u := os.Getenv("CONCH_GITHUB_URL"); u != "" {
		return strings.TrimSuffix(u, "/")
	}
	return "https://github.com"
}

// What a fetched skill may be: a folder of instructions and the odd
// script, not a repository's worth of files.
const (
	fetchMaxFiles = 500
	fetchMaxBytes = 20 << 20
	fetchTimeout  = 2 * time.Minute
)

// SkillsDir is where fetched skills are kept.
func SkillsDir() string { return filepath.Join(libraryDir(), "skills") }

// FetchSkill downloads the skill at source into the library, named name
// (or its folder's name). A skill already there by that name is replaced,
// so fetching again updates it. Nothing in the download is run, and only
// regular files and folders are copied: a link could point anywhere.
func FetchSkill(ctx context.Context, source, name string) (FetchedSkill, error) {
	src, ok := ParseSkillSource(source)
	if !ok {
		return FetchedSkill{}, fmt.Errorf("%q isn't a GitHub skill: owner/repo, owner/repo/path/to/skill, @ref at the end, or a github.com link", source)
	}
	if name != "" && !fetchName.MatchString(name) {
		return FetchedSkill{}, fmt.Errorf("skill name %q: letters, digits, dots, dashes and underscores", name)
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	tmp, err := os.MkdirTemp("", "conch-skill-")
	if err != nil {
		return FetchedSkill{}, err
	}
	defer os.RemoveAll(tmp)
	clone := filepath.Join(tmp, "repo")
	args := []string{"clone", "--quiet", "--depth", "1", "--no-recurse-submodules", "--single-branch"}
	if src.Ref != "" {
		args = append(args, "--branch", src.Ref)
	}
	args = append(args, githubBase()+"/"+src.Owner+"/"+src.Repo+".git", clone)
	cmd := exec.CommandContext(ctx, "git", args...)
	// Never wait on a password prompt nobody can see; no hooks of the
	// person's own either, which a clone would otherwise run.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return FetchedSkill{}, fmt.Errorf("fetching %s took too long", src)
		}
		msg := strings.TrimSpace(string(out))
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		return FetchedSkill{}, fmt.Errorf("fetching %s: %s", src, strings.TrimPrefix(msg, "fatal: "))
	}
	dir := filepath.Join(clone, filepath.FromSlash(src.Path))
	if src.Path == "" {
		if _, err := os.Stat(filepath.Join(clone, "SKILL.md")); err != nil {
			found := findSkills(clone)
			switch len(found) {
			case 0:
				return FetchedSkill{}, fmt.Errorf("%s has no SKILL.md", src)
			case 1:
				src.Path = found[0]
				dir = filepath.Join(clone, filepath.FromSlash(found[0]))
			default:
				names := make([]string, len(found))
				for i, f := range found {
					names[i] = SkillSource{Owner: src.Owner, Repo: src.Repo, Ref: src.Ref, Path: f}.String()
				}
				return FetchedSkill{}, &FetchError{Source: src.String(), Found: names}
			}
		}
	}
	if fi, err := os.Lstat(filepath.Join(dir, "SKILL.md")); err != nil || !fi.Mode().IsRegular() {
		return FetchedSkill{}, fmt.Errorf("%s has no SKILL.md", src)
	}
	if name == "" {
		name = path.Base(src.Path)
		if src.Path == "" {
			name = src.Repo
		}
		if !fetchName.MatchString(name) {
			return FetchedSkill{}, fmt.Errorf("give the skill a name: %q won't do as one", name)
		}
	}
	dest := filepath.Join(SkillsDir(), name)
	stage := dest + ".fetching"
	os.RemoveAll(stage)
	if err := copySkill(dir, stage); err != nil {
		os.RemoveAll(stage)
		return FetchedSkill{}, err
	}
	// Replace the old copy only once the new one is whole.
	old := dest + ".old"
	os.RemoveAll(old)
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, old); err != nil {
			os.RemoveAll(stage)
			return FetchedSkill{}, err
		}
	}
	if err := os.Rename(stage, dest); err != nil {
		os.Rename(old, dest)
		return FetchedSkill{}, err
	}
	os.RemoveAll(old)
	return FetchedSkill{Name: name, Path: dest, Source: src.String()}, nil
}

// findSkills lists the folders under root holding a SKILL.md, as paths
// from root, a few levels deep and never inside .git.
func findSkills(root string) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if d.Name() == ".git" || strings.Count(rel, string(filepath.Separator)) >= 4 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "SKILL.md" && d.Type().IsRegular() && rel != "SKILL.md" {
			out = append(out, filepath.ToSlash(filepath.Dir(rel)))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// copySkill copies a skill's folder: regular files and folders only, and
// no more than a skill should hold.
func copySkill(from, to string) error {
	files, size := 0, int64(0)
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		target := filepath.Join(to, rel)
		switch {
		case d.IsDir():
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return nil // a link could point anywhere on the machine
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if files++; files > fetchMaxFiles {
			return errors.New("that skill holds too many files to be one")
		}
		if size += info.Size(); size > fetchMaxBytes {
			return errors.New("that skill is too big to be one")
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		mode := os.FileMode(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755 // a script the skill runs stays runnable
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
