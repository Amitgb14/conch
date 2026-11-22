package phone

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

	"github.com/Amitgb14/conch/internal/fleet"
	"github.com/Amitgb14/conch/internal/proto"
)

// Artifact is a claude.ai artifact an agent published, as its session
// transcript records it.
type Artifact struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	Kind     string    `json:"kind"`
	Versions int       `json:"versions"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	// Dir is the directory the publishing session ran in.
	Dir string `json:"dir,omitempty"`
}

// ArtifactList answers GET /api/artifacts: newest first. Pane is empty
// when it lists every artifact on this machine.
type ArtifactList struct {
	Pane      string     `json:"pane"`
	Artifacts []Artifact `json:"artifacts"`
}

// artifacts lists what the pane's Claude Code sessions published, or with
// no pane, what every Claude Code session on this machine published. The
// screen can't say: Claude Code draws full screen, and a link it printed
// has scrolled away by the time anyone looks. Its transcripts keep every
// publish, with the title it was given.
func (g *Gateway) artifacts(rq *request) (any, *APIError) {
	root := claudeProjects()
	ref := rq.r.URL.Query().Get("pane")
	if ref == "" {
		if root == "" {
			return ArtifactList{Artifacts: []Artifact{}}, nil
		}
		m, _ := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
		arts, err := transcriptArtifacts.readAll(m)
		if err != nil {
			return nil, apiErr(CodeServerUnavailable, "could not read the transcripts: "+err.Error())
		}
		return ArtifactList{Artifacts: arts}, nil
	}
	c, pane, id, aerr := g.paneOn(rq.dev, ref)
	if aerr != nil {
		return nil, aerr
	}
	out := ArtifactList{Pane: id, Artifacts: []Artifact{}}
	if fleet.MachineOf(id) != LocalMachine {
		// The transcript is on that machine, out of this gateway's reach.
		return out, nil
	}
	var list proto.PaneList
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodPaneList, nil, &list); aerr != nil {
		return nil, aerr
	}
	i := slices.IndexFunc(list.Panes, func(p proto.PaneInfo) bool { return p.ID == pane })
	if i < 0 {
		return nil, apiErr(CodeNotFound, "no pane "+id)
	}
	a := list.Panes[i].Agent
	if a == nil || a.Name != "claude" || a.SessionID == "" {
		return out, nil
	}
	if root == "" {
		return out, nil
	}
	path := findTranscript(root, a.SessionID)
	if path == "" {
		return out, nil
	}
	// Every session that ran in the agent's directory, not just the current
	// one: a resumed or cleared agent starts a new transcript there.
	m, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.jsonl"))
	arts, err := transcriptArtifacts.readAll(m)
	if err != nil {
		return nil, apiErr(CodeServerUnavailable, "could not read the agent's transcript: "+err.Error())
	}
	out.Artifacts = arts
	return out, nil
}

func claudeProjects() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// findTranscript is Claude Code's transcript for a session: a session id
// names one file, in the folder of whichever directory it started in.
func findTranscript(root, session string) string {
	if strings.ContainsAny(session, `/\`) || strings.Contains(session, "..") {
		return ""
	}
	m, _ := filepath.Glob(filepath.Join(root, "*", session+".jsonl"))
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

// transcriptArtifacts remembers each transcript's artifacts until the file
// changes: a long session's transcript runs to megabytes.
var transcriptArtifacts = &artifactCache{entries: map[string]artifactEntry{}}

// maxCachedTranscripts bounds the cache. Listing every artifact reads every
// transcript on the computer, and there can be thousands, so a small bound
// would empty the cache part-way through each listing and make every
// request read them all again; an entry is a size, a time and a short list.
var maxCachedTranscripts = 1 << 14

type artifactCache struct {
	mu      sync.Mutex
	entries map[string]artifactEntry
}

type artifactEntry struct {
	size  int64
	mtime time.Time
	arts  []Artifact
}

func (c *artifactCache) read(path string) ([]Artifact, error) {
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
	arts, err := parseArtifacts(f)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if len(c.entries) >= maxCachedTranscripts {
		// Transcripts deleted since go first; only past that, everything.
		for p := range c.entries {
			if _, err := os.Stat(p); err != nil {
				delete(c.entries, p)
			}
		}
		if len(c.entries) >= maxCachedTranscripts {
			clear(c.entries)
		}
	}
	c.entries[path] = artifactEntry{size: st.Size(), mtime: st.ModTime(), arts: arts}
	c.mu.Unlock()
	return arts, nil
}

// readAll merges the artifacts of several transcripts: an artifact two
// sessions published is one entry, with both sessions' versions.
func (c *artifactCache) readAll(paths []string) ([]Artifact, error) {
	byURL := map[string]*Artifact{}
	var firstErr error
	for _, p := range paths {
		arts, err := c.read(p)
		if err != nil {
			if firstErr == nil && !errors.Is(err, os.ErrNotExist) {
				firstErr = err
			}
			continue
		}
		for _, a := range arts {
			m := byURL[a.URL]
			if m == nil {
				cp := a
				byURL[a.URL] = &cp
				continue
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
	return newestFirst(byURL), nil
}

func newestFirst(byURL map[string]*Artifact) []Artifact {
	out := make([]Artifact, 0, len(byURL))
	for _, a := range byURL {
		out = append(out, *a)
	}
	slices.SortFunc(out, func(a, b Artifact) int {
		if c := b.Updated.Compare(a.Updated); c != 0 {
			return c
		}
		return strings.Compare(a.URL, b.URL)
	})
	return out
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

// parseArtifacts reads a Claude Code transcript for the Artifact tool's
// publishes: one entry per artifact, newest first.
func parseArtifacts(r io.Reader) ([]Artifact, error) {
	calls := map[string]artifactCall{}
	byURL := map[string]*Artifact{}
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && (bytes.Contains(line, []byte(`"Artifact"`)) || bytes.Contains(line, []byte("claude.ai/"))) {
			collectArtifacts(line, calls, byURL)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return newestFirst(byURL), nil
}

func collectArtifacts(line []byte, calls map[string]artifactCall, byURL map[string]*Artifact) {
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
				a = &Artifact{URL: m[1], Created: l.Timestamp, Dir: l.Cwd}
				byURL[m[1]] = a
			}
			a.Versions++
			if l.Timestamp.After(a.Updated) {
				a.Updated = l.Timestamp
			}
			if a.Title == "" {
				a.Title = artifactTitle(call)
			}
			if k := ofTypeRe.FindStringSubmatch(text); k != nil {
				a.Kind = k[1]
			} else if a.Kind == "" {
				a.Kind = artifactKind(call)
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

// artifactTitle is the title the call gave, else the page's <title>, else
// its file's name.
func artifactTitle(c artifactCall) string {
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

func artifactKind(c artifactCall) string {
	switch {
	case c.TypeURL != "":
		return "Artifact"
	case strings.HasSuffix(strings.ToLower(c.FilePath), ".md"):
		return "Doc"
	default:
		return "Page"
	}
}
