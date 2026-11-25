// Package artifacts reads the claude.ai artifacts Claude Code published
// from its session transcripts (~/.claude/projects/FOLDER/SESSION.jsonl).
// Nothing else records them: claude.ai offers no list, and the link an
// agent printed has scrolled off its full-screen interface by the time
// anybody looks. A transcript keeps every Artifact tool call and the
// tool's reply, which names the artifact's address.
//
// The phone gateway (GET /api/artifacts) and the server (artifact.list)
// both read through here.
package artifacts

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// Found is an artifact and the transcript folders that published it: a
// folder is one directory's sessions, which is how a pane's agent is
// matched to what it published.
type Found struct {
	proto.Artifact
	Folders []string
}

// Root is Claude Code's folder of transcripts, or "" without a home.
func Root() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// FindTranscript is Claude Code's transcript for a session: a session id
// names one file, in the folder of whichever directory it started in.
func FindTranscript(root, session string) string {
	if session == "" || strings.ContainsAny(session, `/\`) || strings.Contains(session, "..") {
		return ""
	}
	m, _ := filepath.Glob(filepath.Join(root, "*", session+".jsonl"))
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

// All is every artifact any Claude Code session under root published.
func All(c *Cache, root string) ([]Found, error) {
	if root == "" {
		return []Found{}, nil
	}
	m, _ := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	return c.ReadAll(m)
}

// OfSession is what the sessions saved beside session published: every
// session that ran in its directory, not just this one, since a resumed or
// cleared agent starts a new transcript there. Found is false when the
// session's transcript is not on disk (yet: it is written after the first
// answer).
func OfSession(c *Cache, root, session string) ([]Found, bool, error) {
	path := FindTranscript(root, session)
	if path == "" {
		return []Found{}, false, nil
	}
	m, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.jsonl"))
	arts, err := c.ReadAll(m)
	return arts, true, err
}

// Plain is the artifacts without their folders.
func Plain(f []Found) []proto.Artifact {
	out := make([]proto.Artifact, 0, len(f))
	for _, a := range f {
		out = append(out, a.Artifact)
	}
	return out
}

// Shared is the cache both readers use: one process reads a transcript
// once until it changes.
var Shared = NewCache()

// MaxCached bounds a cache. Listing every artifact reads every transcript
// on the computer, and there can be thousands, so a small bound would
// empty the cache part-way through each listing and make every request
// read them all again; an entry is a size, a time and a short list.
var MaxCached = 1 << 14

// Cache remembers each transcript's artifacts until the file changes: a
// long session's transcript runs to megabytes.
type Cache struct {
	mu      sync.Mutex
	entries map[string]entry
}

type entry struct {
	size  int64
	mtime time.Time
	arts  []proto.Artifact
}

// NewCache is an empty cache.
func NewCache() *Cache { return &Cache{entries: map[string]entry{}} }

// Read is one transcript's artifacts, newest first.
func (c *Cache) Read(path string) ([]proto.Artifact, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	e, ok := c.entries[path]
	c.mu.Unlock()
	if ok && e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
		return e.arts, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	arts, err := Parse(f)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if len(c.entries) >= MaxCached {
		// Transcripts deleted since go first; only past that, everything.
		for p := range c.entries {
			if _, err := os.Stat(p); err != nil {
				delete(c.entries, p)
			}
		}
		if len(c.entries) >= MaxCached {
			clear(c.entries)
		}
	}
	c.entries[path] = entry{size: st.Size(), mtime: st.ModTime(), arts: arts}
	c.mu.Unlock()
	return arts, nil
}

// Len is how many transcripts the cache holds.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Has says whether the cache holds path.
func (c *Cache) Has(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[path]
	return ok
}

// ReadAll merges the artifacts of several transcripts: an artifact two
// sessions published is one entry, with both sessions' versions.
func (c *Cache) ReadAll(paths []string) ([]Found, error) {
	byURL := map[string]*Found{}
	var firstErr error
	for _, p := range paths {
		arts, err := c.Read(p)
		if err != nil {
			if firstErr == nil && !errors.Is(err, os.ErrNotExist) {
				firstErr = err
			}
			continue
		}
		folder := filepath.Dir(p)
		for _, a := range arts {
			m := byURL[a.URL]
			if m == nil {
				byURL[a.URL] = &Found{Artifact: a, Folders: []string{folder}}
				continue
			}
			if !slices.Contains(m.Folders, folder) {
				m.Folders = append(m.Folders, folder)
			}
			m.Versions += a.Versions
			if a.Created.Before(m.Created) {
				m.Created = a.Created
			}
			if a.Updated.After(m.Updated) {
				m.Updated, m.Title, m.Kind, m.Dir = a.Updated, a.Title, a.Kind, a.Dir
			}
		}
	}
	if len(byURL) == 0 && firstErr != nil {
		return nil, firstErr
	}
	out := make([]Found, 0, len(byURL))
	for _, a := range byURL {
		out = append(out, *a)
	}
	slices.SortFunc(out, func(a, b Found) int {
		if c := b.Updated.Compare(a.Updated); c != 0 {
			return c
		}
		return strings.Compare(a.URL, b.URL)
	})
	return out, nil
}

var (
	// A publish's result: "Created a new Artifact at URL", "Updated the
	// Artifact at URL", "Published … at URL".
	publishedRe = regexp.MustCompile(`^(?:Created|Updated|Published)\b[^\n]*?(https://claude\.ai/(?:code/)?artifact/[A-Za-z0-9_-]+)`)
	ofTypeRe    = regexp.MustCompile(`of type "([^"]{1,40})"`)
	titleTagRe  = regexp.MustCompile(`(?is)<title>\s*(.*?)\s*</title>`)
)

type artifactCall struct {
	Title    string `json:"title"`
	FilePath string `json:"file_path"`
	TypeURL  string `json:"type_url"`
}

type transcriptLine struct {
	Timestamp time.Time `json:"timestamp"`
	Cwd       string    `json:"cwd"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type transcriptBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// Parse reads a Claude Code transcript for the Artifact tool's publishes:
// one entry per artifact, newest first. Lines that are not JSON are
// skipped, as a transcript cut off mid-write ends in one.
func Parse(r io.Reader) ([]proto.Artifact, error) {
	calls := map[string]artifactCall{}
	byURL := map[string]*proto.Artifact{}
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && (bytes.Contains(line, []byte(`"Artifact"`)) || bytes.Contains(line, []byte("claude.ai/"))) {
			collect(line, calls, byURL)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	out := make([]proto.Artifact, 0, len(byURL))
	for _, a := range byURL {
		out = append(out, *a)
	}
	slices.SortFunc(out, func(a, b proto.Artifact) int {
		if c := b.Updated.Compare(a.Updated); c != 0 {
			return c
		}
		return strings.Compare(a.URL, b.URL)
	})
	return out, nil
}

func collect(line []byte, calls map[string]artifactCall, byURL map[string]*proto.Artifact) {
	var l transcriptLine
	if json.Unmarshal(line, &l) != nil {
		return
	}
	var blocks []transcriptBlock
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		switch {
		case b.Type == "tool_use" && b.Name == "Artifact":
			var in artifactCall
			_ = json.Unmarshal(b.Input, &in)
			calls[b.ID] = in
		case b.Type == "tool_result":
			call, ok := calls[b.ToolUseID]
			if !ok {
				continue
			}
			text := resultText(b.Content)
			m := publishedRe.FindStringSubmatch(text)
			if m == nil {
				continue
			}
			a := byURL[m[1]]
			if a == nil {
				a = &proto.Artifact{URL: m[1], Created: l.Timestamp, Dir: l.Cwd}
				byURL[m[1]] = a
			}
			a.Versions++
			if l.Timestamp.After(a.Updated) {
				a.Updated = l.Timestamp
			}
			if a.Title == "" {
				a.Title = title(call)
			}
			if k := ofTypeRe.FindStringSubmatch(text); k != nil {
				a.Kind = k[1]
			} else if a.Kind == "" {
				a.Kind = kind(call)
			}
		}
	}
}

// resultText is a tool result's text: a string, or text blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// title is the title the call gave, else the page's <title>, else its
// file's name.
func title(c artifactCall) string {
	if c.Title != "" {
		return c.Title
	}
	if strings.HasSuffix(strings.ToLower(c.FilePath), ".html") {
		if f, err := os.Open(c.FilePath); err == nil {
			head := make([]byte, 8192)
			n, _ := io.ReadFull(f, head)
			f.Close()
			if m := titleTagRe.FindSubmatch(head[:n]); m != nil && len(m[1]) > 0 {
				return string(m[1])
			}
		}
	}
	if c.FilePath != "" {
		return strings.TrimSuffix(filepath.Base(c.FilePath), filepath.Ext(c.FilePath))
	}
	return "Artifact"
}

func kind(c artifactCall) string {
	switch {
	case c.TypeURL != "":
		return "Artifact"
	case strings.HasSuffix(strings.ToLower(c.FilePath), ".md"):
		return "Doc"
	default:
		return "Page"
	}
}
