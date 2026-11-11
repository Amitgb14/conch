package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// fs.search and fs.replace: Conch.app's search across a checkout — text in
// its files and files by name — and the replacing of what it found. Both
// read what git lists, as fs.find does, so node_modules and build output
// stay out unless asked for; and a replace is checked file by file
// against the content that was searched, so it never writes over a file
// an agent changed meanwhile.

// searchTimeout bounds a search of a whole checkout; what was found by
// then comes back marked Truncated.
const searchTimeout = 15 * time.Second

// compilePattern makes a pattern a regular expression. One that matches
// the empty string is refused: it would match every line, and a replace
// would insert between every letter.
func compilePattern(p proto.FSPattern) (*regexp.Regexp, *proto.Error) {
	if p.Query == "" {
		return nil, proto.Errorf(proto.ErrBadRequest, "nothing to search for")
	}
	expr := p.Query
	if !p.Regex {
		expr = regexp.QuoteMeta(expr)
	}
	if p.Word {
		expr = `\b(?:` + expr + `)\b`
	}
	if !p.Case {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if re.MatchString("") {
		return nil, proto.Errorf(proto.ErrBadRequest, "%q matches an empty line: it would match everywhere", p.Query)
	}
	return re, nil
}

// globRegexp makes a glob a test of a path relative to the checkout:
// `*.go` and `node_modules` match a name anywhere, a glob with a slash in
// it (`cmd/conch`, `internal/`) matches from the top, `**` crosses
// folders, and a folder matched takes everything in it.
func globRegexp(g string) (*regexp.Regexp, error) {
	g = strings.TrimSpace(g)
	anchored := strings.Contains(strings.Trim(g, "/"), "/") || strings.HasPrefix(g, "/")
	g = strings.Trim(g, "/")
	if g == "" {
		return nil, errors.New("an empty glob")
	}
	var b strings.Builder
	for i := 0; i < len(g); i++ {
		switch c := g[i]; {
		case c == '*' && i+1 < len(g) && g[i+1] == '*':
			i++
			if i+1 < len(g) && g[i+1] == '/' {
				i++
				b.WriteString(`(?:.*/)?`)
			} else {
				b.WriteString(`.*`)
			}
		case c == '*':
			b.WriteString(`[^/]*`)
		case c == '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	start := `(?:^|/)`
	if anchored {
		start = `^`
	}
	return regexp.Compile(start + b.String() + `(?:/.*)?$`)
}

// pathFilter is Include and Exclude together.
type pathFilter struct{ include, exclude []*regexp.Regexp }

func newPathFilter(include, exclude []string) (pathFilter, *proto.Error) {
	var f pathFilter
	for _, list := range []struct {
		globs []string
		into  *[]*regexp.Regexp
	}{{include, &f.include}, {exclude, &f.exclude}} {
		for _, g := range list.globs {
			if strings.TrimSpace(g) == "" {
				continue
			}
			re, err := globRegexp(g)
			if err != nil {
				return f, proto.Errorf(proto.ErrBadRequest, "glob %q: %v", g, err)
			}
			*list.into = append(*list.into, re)
		}
	}
	return f, nil
}

func (f pathFilter) takes(rel string) bool {
	for _, re := range f.exclude {
		if re.MatchString(rel) {
			return false
		}
	}
	if len(f.include) == 0 {
		return true
	}
	for _, re := range f.include {
		if re.MatchString(rel) {
			return true
		}
	}
	return false
}

// checkoutFiles is every file of a checkout a search reads: git's list,
// or with ignored all of them but .git's.
func checkoutFiles(ctx context.Context, root string, ignored bool) []string {
	if !ignored {
		if files, err := gitx.Files(ctx, root); err == nil {
			return files
		}
		return walkFiles(root, findWalkMax)
	}
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(root, p); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		if len(out) >= findWalkMax {
			return filepath.SkipAll
		}
		return nil
	})
	return out
}

func (s *Server) searchFiles(sp proto.FSSearchParams) (proto.FSSearchResult, *proto.Error) {
	root, perr := s.checkoutRoot(sp.Root)
	if perr != nil {
		return proto.FSSearchResult{}, perr
	}
	limit := sp.Limit
	if limit <= 0 {
		limit = proto.FSSearchDefault
	}
	limit = min(limit, proto.FSSearchMax)
	if sp.Path != "" {
		return searchOne(root, sp, limit)
	}
	if !sp.Names && !sp.Text {
		return proto.FSSearchResult{}, proto.Errorf(proto.ErrBadRequest, "fs.search needs names, text or both")
	}
	filter, perr := newPathFilter(sp.Include, sp.Exclude)
	if perr != nil {
		return proto.FSSearchResult{}, perr
	}
	var re *regexp.Regexp
	if sp.Text {
		if re, perr = compilePattern(sp.FSPattern); perr != nil {
			return proto.FSSearchResult{}, perr
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
	defer cancel()
	var files []string
	for _, f := range checkoutFiles(ctx, root, sp.Ignored) {
		if filter.takes(f) {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	res := proto.FSSearchResult{Root: root}
	if sp.Names && strings.TrimSpace(sp.Query) != "" {
		res.Names, res.Truncated = matchFiles(files, sp.Query, proto.FSFindMax)
	}
	if !sp.Text {
		return res, nil
	}
	found := 0
	for _, rel := range files {
		if ctx.Err() != nil || found >= limit {
			res.Truncated = true
			break
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > proto.FSSearchFileMax {
			res.Skipped++
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if isBinary(data, false) {
			res.Skipped++
			continue
		}
		res.Searched++
		lines, more := searchData(data, re, limit-found)
		if len(lines) == 0 {
			continue
		}
		found += len(lines)
		res.Truncated = res.Truncated || more
		res.Files = append(res.Files, proto.FSSearchFile{Path: rel, Hash: contentHash(data), Lines: lines})
	}
	return res, nil
}

// searchData is the lines of data that match, at most limit, and whether
// there were more.
func searchData(data []byte, re *regexp.Regexp, limit int) ([]proto.FSSearchLine, bool) {
	var out []proto.FSSearchLine
	var off int64
	for n := 1; off < int64(len(data)); n++ {
		rest := data[off:]
		end, next := len(rest), int64(len(rest))
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			end, next = i, int64(i+1)
		}
		line := strings.TrimSuffix(string(rest[:end]), "\r")
		if m := matches(re, line); len(m) > 0 {
			if len(out) >= limit {
				return out, true
			}
			out = append(out, searchLine(n, off, line, m))
		}
		off += next
	}
	return out, false
}

// matches is where re matches in line, leaving out empty matches.
func matches(re *regexp.Regexp, line string) [][]int {
	var out [][]int
	for _, m := range re.FindAllStringIndex(line, -1) {
		if m[1] > m[0] {
			out = append(out, m)
		}
	}
	return out
}

// searchLine is a matching line as fs.search returns it: a long line is
// cut to FSSearchLineMax around its first match, on whole characters.
func searchLine(n int, off int64, line string, ms [][]int) proto.FSSearchLine {
	col := 0
	if len(line) > proto.FSSearchLineMax {
		col = max(0, ms[0][0]-80)
		for col > 0 && !utf8.RuneStart(line[col]) {
			col--
		}
	}
	end := min(len(line), col+proto.FSSearchLineMax)
	for end < len(line) && end > col && !utf8.RuneStart(line[end]) {
		end--
	}
	text := line[col:end]
	ranges := [][2]int{}
	for _, m := range ms {
		a, b := m[0]-col, min(m[1]-col, len(text))
		if a < 0 || a >= len(text) {
			continue
		}
		ranges = append(ranges, [2]int{a, b})
	}
	if !utf8.ValidString(text) {
		// A stray byte in a big file's line: shown, not trusted for ranges.
		text = strings.ToValidUTF8(text, "�")
		ranges = [][2]int{}
	}
	return proto.FSSearchLine{Line: n, Offset: off, Col: col, Text: text, Ranges: ranges}
}

// searchLineMax is the longest line a piece-at-a-time search holds; the
// rest of a longer one is passed over unread for matches.
const searchLineMax = 1 << 20

// searchOne searches one file from an offset for at most FSSearchBudget,
// which is how a file too big to read whole is searched: the client asks
// again from Next until Done.
func searchOne(root string, sp proto.FSSearchParams, limit int) (proto.FSSearchResult, *proto.Error) {
	re, perr := compilePattern(sp.FSPattern)
	if perr != nil {
		return proto.FSSearchResult{}, perr
	}
	path, perr := confine(root, sp.Path)
	if perr != nil {
		return proto.FSSearchResult{}, perr
	}
	f, info, perr := openRegular(path, sp.Path)
	if perr != nil {
		return proto.FSSearchResult{}, perr
	}
	defer f.Close()
	if sp.Offset < 0 || sp.Offset > info.Size() {
		return proto.FSSearchResult{}, proto.Errorf(proto.ErrBadRequest, "offset %d is outside the file", sp.Offset)
	}
	line := max(sp.Line, 1)
	res := proto.FSSearchResult{Root: root, Size: info.Size(), Searched: 1}
	r := bufio.NewReaderSize(io.NewSectionReader(f, sp.Offset, info.Size()-sp.Offset), 256<<10)
	off := sp.Offset
	deadline := time.Now().Add(proto.FSSearchBudget)
	var lines []proto.FSSearchLine
	for {
		text, n, err := readLine(r)
		if n == 0 {
			res.Done = true
			break
		}
		if m := matches(re, text); len(m) > 0 {
			lines = append(lines, searchLine(line, off, text, m))
		}
		off += int64(n)
		line++
		if err != nil {
			res.Done = true
			break
		}
		if len(lines) >= limit || time.Now().After(deadline) {
			res.Truncated = len(lines) >= limit
			break
		}
	}
	res.Next, res.NextLine = off, line
	if off >= info.Size() {
		res.Done = true
	}
	if len(lines) > 0 {
		res.Files = []proto.FSSearchFile{{Path: sp.Path, Lines: lines}}
	}
	return res, nil
}

// readLine reads one line: its text, without the newline or a carriage
// return before it and cut at searchLineMax, and how many bytes it took
// in the file. err is set at the end of the file.
func readLine(r *bufio.Reader) (string, int, error) {
	var buf []byte
	n := 0
	for {
		chunk, err := r.ReadSlice('\n')
		n += len(chunk)
		if len(buf) < searchLineMax {
			buf = append(buf, chunk[:min(len(chunk), searchLineMax-len(buf))]...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		text := strings.TrimSuffix(strings.TrimSuffix(string(buf), "\n"), "\r")
		if err != nil {
			return text, n, err
		}
		return text, n, nil
	}
}

// openRegular opens a file that must be a regular one: a folder has no
// lines and a fifo would block, so it is looked at first.
func openRegular(path, name string) (*os.File, os.FileInfo, *proto.Error) {
	if info, err := os.Stat(path); err != nil {
		return nil, nil, proto.Errorf(proto.ErrBadRequest, "%v", err)
	} else if !info.Mode().IsRegular() {
		return nil, nil, proto.Errorf(proto.ErrBadRequest, "%s is not a regular file", name)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, proto.Errorf(proto.ErrBadRequest, "%s is not a regular file", name)
	}
	return f, info, nil
}

func (s *Server) replaceFiles(c *client, rp proto.FSReplaceParams) (proto.FSReplaceResult, *proto.Error) {
	root, perr := s.writableCheckout(c, rp.Root, proto.MethodFSReplace)
	if perr != nil {
		return proto.FSReplaceResult{}, perr
	}
	re, perr := compilePattern(rp.FSPattern)
	if perr != nil {
		return proto.FSReplaceResult{}, perr
	}
	if len(rp.Files) == 0 {
		return proto.FSReplaceResult{}, proto.Errorf(proto.ErrBadRequest, "fs.replace needs the files to replace in")
	}
	var res proto.FSReplaceResult
	skip := func(p, why string) { res.Skipped = append(res.Skipped, proto.FSSkipped{Path: p, Reason: why}) }
	for _, f := range rp.Files {
		path, perr := confine(root, f.Path)
		if perr != nil {
			skip(f.Path, perr.Message)
			continue
		}
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		}
		info, err := os.Stat(path)
		if err != nil {
			skip(f.Path, "deleted since it was searched")
			continue
		}
		if !info.Mode().IsRegular() {
			skip(f.Path, "not a regular file")
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			skip(f.Path, err.Error())
			continue
		}
		if f.Hash == "" || contentHash(data) != f.Hash {
			skip(f.Path, "changed on disk since it was searched")
			continue
		}
		out, count := replaceLines(data, re, rp.Replace, rp.Regex, f.Lines)
		if count == 0 {
			skip(f.Path, "nothing matched")
			continue
		}
		if err := writeAtomic(path, out, info.Mode().Perm()); err != nil {
			skip(f.Path, err.Error())
			continue
		}
		res.Files = append(res.Files, proto.FSReplaced{Path: f.Path, Count: count, Hash: contentHash(out)})
	}
	return res, nil
}

// replaceLines replaces re's matches in data line by line — on the lines
// listed, or all of them — keeping each line's ending, and counts them.
// With expand, $1 and ${name} in repl are the groups matched.
func replaceLines(data []byte, re *regexp.Regexp, repl string, expand bool, only []int) ([]byte, int) {
	want := map[int]bool{}
	for _, n := range only {
		want[n] = true
	}
	var b strings.Builder
	b.Grow(len(data))
	count := 0
	rest := string(data)
	for n := 1; rest != ""; n++ {
		line, ending := rest, ""
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			line, ending, rest = rest[:i], "\n", rest[i+1:]
		} else {
			rest = ""
		}
		if strings.HasSuffix(line, "\r") {
			line, ending = line[:len(line)-1], "\r"+ending
		}
		if len(want) == 0 || want[n] {
			if k := len(matches(re, line)); k > 0 {
				count += k
				if expand {
					line = re.ReplaceAllString(line, repl)
				} else {
					line = re.ReplaceAllLiteralString(line, repl)
				}
			}
		}
		b.WriteString(line)
		b.WriteString(ending)
	}
	return []byte(b.String()), count
}
