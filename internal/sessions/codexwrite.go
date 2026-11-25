package sessions

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// codexWriteVersion is the Codex whose rollouts CodexWriter's were checked
// against (0.171, as read by codexConversation), and the cli_version it
// writes.
const codexWriteVersion = "0.171.0"

// CodexWriter writes Codex sessions: a rollout under $CODEX_HOME/sessions
// (or ~/.codex/sessions), which `codex resume ID` opens with its history.
//
// It is for a Codex that cannot import a Claude Code session itself, and
// for every other agent's conversation: Codex 0.126 and later import
// Claude Code's own (CodexImporter), and so own the format of what they
// write. The rollout is the shape every Codex reads, older ones too
// (Codex's own test rollout is three lines of it): a session_meta, then
// each message twice — a response_item, which is what the model is given
// back, and the user_message or agent_message event a resume shows — and
// commands as exec_command calls (the shell tool of Codex since unified
// exec became its default) with their output. Codex finds a
// rollout by its file name when its state database has no row for it.
type CodexWriter struct {
	Env Env
}

// Agent is the agent whose sessions it writes.
func (CodexWriter) Agent() string { return "codex" }

func (w CodexWriter) home() string {
	if d := w.Env.get("CODEX_HOME"); d != "" {
		return d
	}
	return filepath.Join(w.Env.Home, ".codex")
}

// WriteConversation writes c as a new Codex session to resume in dir.
// Nothing is left behind when it fails.
func (w CodexWriter) WriteConversation(ctx context.Context, c Conversation, dir string) (Written, error) {
	if err := ctx.Err(); err != nil {
		return Written{}, err
	}
	if !c.HasMessages() {
		return Written{}, errors.New("nothing was said in it to carry over")
	}
	if dir == "" {
		return Written{}, errors.New("no folder to resume it in")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return Written{}, fmt.Errorf("no folder to resume it in: %s is not there", dir)
	}
	id := newSessionUUID()
	now := time.Now()
	body := codexRollout(c, id, dir, now)
	folder := filepath.Join(w.home(), "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return Written{}, err
	}
	path := filepath.Join(folder, "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
	if err := writeWhole(path, body); err != nil {
		return Written{}, err
	}
	return Written{Agent: "codex", ID: id, Path: path, Files: []string{path}}, nil
}

// ReadBack reads a session WriteConversation wrote, as conch reads any.
func (CodexWriter) ReadBack(ctx context.Context, wr Written) (Conversation, error) {
	return ReadConversation(ctx, Env{}, Session{Agent: "codex", ID: wr.ID, Path: wr.Path})
}

// codexLine is one line of a rollout.
type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   any    `json:"payload"`
}

func codexRollout(c Conversation, id, dir string, now time.Time) []byte {
	from := AgentName(c.Session.Agent)
	var body []byte
	last := now
	stamp := func(t time.Time) string {
		if t.IsZero() {
			t = last
		}
		last = t
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	add := func(t time.Time, typ string, payload any) {
		body = appendJSONLine(body, codexLine{Timestamp: stamp(t), Type: typ, Payload: payload})
	}
	first := now
	if len(c.Items) > 0 && !c.Items[0].Time.IsZero() {
		first = c.Items[0].Time
	}
	meta := map[string]any{"id": id, "session_id": id, "timestamp": first.UTC().Format("2006-01-02T15:04:05.000Z"), "cwd": dir,
		"originator": "conch", "cli_version": codexWriteVersion, "source": "cli", "model_provider": "openai"}
	if c.Session.Branch != "" {
		meta["git"] = map[string]string{"branch": c.Session.Branch}
	}
	add(first, "session_meta", meta)
	user := func(text string, t time.Time) {
		add(t, "response_item", map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": text}}})
		add(t, "event_msg", map[string]any{"type": "user_message", "message": text})
	}
	assistant := func(text string, t time.Time) {
		add(t, "response_item", map[string]any{"type": "message", "role": "assistant", "content": []map[string]string{{"type": "output_text", "text": text}}})
		add(t, "event_msg", map[string]any{"type": "agent_message", "message": text})
	}
	var notes []string
	var notesAt time.Time
	flush := func() {
		if len(notes) > 0 {
			assistant(strings.Join(notes, "\n"), notesAt)
			notes = nil
		}
	}
	calls := 0
	items, start := crossOrder(c)
	if start {
		user(startNote(from), items[0].Time)
	}
	for _, it := range items {
		switch {
		case it.Kind == KindMessage && it.Role == "user":
			flush()
			user(it.Text, it.Time)
		case it.Kind == KindMessage:
			flush()
			assistant(it.Text, it.Time)
		case it.Kind == KindCommand:
			flush()
			calls++
			call := fmt.Sprintf("call_conch_%d", calls)
			args, _ := json.Marshal(map[string]string{"cmd": it.Text, "workdir": dir})
			add(it.Time, "response_item", map[string]any{"type": "function_call", "name": "exec_command", "arguments": string(args), "call_id": call})
			out := "aborted: the command did not finish"
			exit := 1
			if it.Done {
				out, exit = it.Output, 0
				if it.Failed {
					exit = it.Exit
					if exit == 0 {
						exit = 1
					}
				}
			}
			add(it.Time, "response_item", map[string]any{"type": "function_call_output", "call_id": call,
				"output": fmt.Sprintf("Process exited with code %d\nOutput:\n%s", exit, out)})
		default:
			if len(notes) == 0 {
				notesAt = it.Time
			}
			notes = append(notes, crossNote(it, from))
		}
	}
	flush()
	assistant(switchedNote(c, "codex"), time.Time{})
	return body
}

// ---- Codex's own importer ----

// ErrNoImporter says the Codex here cannot import a Claude Code session
// itself: too old, not installed, or the session is not where Codex looks.
var ErrNoImporter = errors.New("this Codex cannot import a Claude Code session itself")

// CodexImporter drives Codex's own import of a Claude Code session — what
// its /import and OpenAI's /codex:transfer do — through `codex
// app-server`: externalAgentConfig/import with the session's file, then
// the thread id from the ledger Codex keeps of its imports
// ($CODEX_HOME/external_agent_session_imports.json). Codex then owns its
// format: it flattens Claude's tools into notes of its own and cuts their
// results. Codex 0.126 and later have the method; an older one answers
// "method not found", which is ErrNoImporter. Codex imports only from
// $HOME/.claude/projects (it does not read CLAUDE_CONFIG_DIR), so a
// session in another account's folder is ErrNoImporter too.
type CodexImporter struct {
	Env Env
	// Wait bounds the import; two minutes when zero, as /codex:transfer
	// waits.
	Wait time.Duration
}

// Agent is the agent it writes sessions for.
func (CodexImporter) Agent() string { return "codex" }

func (im CodexImporter) home() string { return CodexWriter{Env: im.Env}.home() }

// codexBinary is the codex the environment would run: ~/.local/bin first,
// as conch launches agents, then its PATH — never this process's, so a
// test's scratch environment reaches no real codex.
func codexBinary(e Env) string {
	if p := filepath.Join(e.Home, ".local", "bin", "codex"); e.Home != "" && isExecutable(p) {
		return p
	}
	for _, d := range filepath.SplitList(e.get("PATH")) {
		if p := filepath.Join(d, "codex"); d != "" && isExecutable(p) {
			return p
		}
	}
	return ""
}

// importRecord is a row of Codex's import ledger.
type importRecord struct {
	SourcePath     string `json:"source_path"`
	ContentSHA256  string `json:"content_sha256"`
	ImportedThread string `json:"imported_thread_id"`
}

// Import has Codex import the Claude Code session at path, and says the
// thread it made.
func (im CodexImporter) Import(ctx context.Context, path string) (Written, error) {
	bin := codexBinary(im.Env)
	if bin == "" {
		return Written{}, fmt.Errorf("%w: no codex command", ErrNoImporter)
	}
	src, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Written{}, err
	}
	projects, err := filepath.EvalSymlinks(filepath.Join(im.Env.Home, ".claude", "projects"))
	if err != nil || !strings.HasPrefix(src, projects+string(filepath.Separator)) || !strings.HasSuffix(src, ".jsonl") {
		return Written{}, fmt.Errorf("%w: Codex imports only sessions under ~/.claude/projects", ErrNoImporter)
	}
	sum := fileHash(src)
	wait := im.Wait
	if wait == 0 {
		wait = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "app-server")
	cmd.Dir = filepath.Dir(src)
	env := []string{"HOME=" + im.Env.Home}
	if d := im.Env.get("CODEX_HOME"); d != "" {
		env = append(env, "CODEX_HOME="+d)
	}
	if p := im.Env.get("PATH"); p != "" {
		env = append(env, "PATH="+p)
	}
	cmd.Env = append(withoutVars(os.Environ(), "HOME", "CODEX_HOME", "PATH"), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Written{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Written{}, err
	}
	if err := cmd.Start(); err != nil {
		return Written{}, fmt.Errorf("%w: %v", ErrNoImporter, err)
	}
	defer func() {
		stdin.Close()
		cancel()
		_ = cmd.Wait()
	}()
	id, err := importOver(ctx, stdin, stdout, src)
	if err != nil {
		return Written{}, err
	}
	// The ledger is how /codex:transfer finds the thread on every version;
	// the notification's target is newer.
	if rec, ok := im.ledgerFor(src, sum); ok {
		id = rec.ImportedThread
	}
	if id == "" {
		return Written{}, errors.New("Codex imported nothing: the session's folder may be gone, or it was continued elsewhere")
	}
	rollout := CodexRollout(im.Env, id)
	if rollout == "" {
		return Written{}, fmt.Errorf("Codex named thread %s, but no rollout of it is in %s", id, filepath.Join(im.home(), "sessions"))
	}
	return Written{Agent: "codex", ID: id, Path: rollout, Files: []string{rollout}}, nil
}

func (im CodexImporter) ledgerFor(src, sum string) (importRecord, bool) {
	b, err := os.ReadFile(filepath.Join(im.home(), "external_agent_session_imports.json"))
	if err != nil {
		return importRecord{}, false
	}
	var l struct {
		Records []importRecord `json:"records"`
	}
	if json.Unmarshal(b, &l) != nil {
		return importRecord{}, false
	}
	for i := len(l.Records) - 1; i >= 0; i-- {
		r := l.Records[i]
		if r.SourcePath == src && (sum == "" || r.ContentSHA256 == sum) && r.ImportedThread != "" {
			return r, true
		}
	}
	return importRecord{}, false
}

// importOver speaks to an app-server as /codex:transfer does — one JSON
// object a line, no "jsonrpc" field: an id alone is a response, a method
// alone a notification — initialize, then the import, then waits for it
// to complete, and says the thread it names, if it does.
func importOver(ctx context.Context, w io.Writer, r io.Reader, src string) (string, error) {
	type rpcErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	type msg struct {
		ID     *int            `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  *rpcErr         `json:"error"`
		Params json.RawMessage `json:"params"`
	}
	lines := make(chan msg)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
		for sc.Scan() {
			var m msg
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			select {
			case lines <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	send := func(v any) error {
		b, _ := json.Marshal(v)
		_, err := w.Write(append(b, '\n'))
		return err
	}
	next := func() (msg, error) {
		select {
		case m, ok := <-lines:
			if !ok {
				return msg{}, errors.New("codex app-server ended before the import was done")
			}
			return m, nil
		case <-ctx.Done():
			return msg{}, fmt.Errorf("Codex's import did not finish: %w", ctx.Err())
		}
	}
	response := func(id int) (msg, error) {
		for {
			m, err := next()
			if err != nil {
				return m, err
			}
			if m.ID != nil && *m.ID == id && m.Method == "" {
				return m, nil
			}
		}
	}
	if err := send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{
		"clientInfo":   map[string]string{"name": "conch", "title": "conch", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": false}}}); err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoImporter, err)
	}
	if m, err := response(1); err != nil {
		return "", err
	} else if m.Error != nil {
		return "", fmt.Errorf("%w: initialize: %s", ErrNoImporter, m.Error.Message)
	}
	if err := send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return "", err
	}
	if err := send(map[string]any{"id": 2, "method": "externalAgentConfig/import", "params": map[string]any{
		"migrationItems": []map[string]any{{
			"itemType": "SESSIONS", "description": "Switch a Claude Code session to Codex (conch)", "cwd": nil,
			"details": map[string]any{"plugins": []any{}, "sessions": []map[string]any{{"path": src, "cwd": nil, "title": nil}},
				"mcpServers": []any{}, "hooks": []any{}, "subagents": []any{}, "commands": []any{}},
		}},
	}}); err != nil {
		return "", err
	}
	m, err := response(2)
	if err != nil {
		return "", err
	}
	if m.Error != nil {
		if m.Error.Code == -32601 {
			return "", fmt.Errorf("%w: it is older than 0.126", ErrNoImporter)
		}
		return "", fmt.Errorf("Codex refused the import: %s", m.Error.Message)
	}
	var started struct {
		ImportID string `json:"importId"`
	}
	_ = json.Unmarshal(m.Result, &started)
	for {
		m, err := next()
		if err != nil {
			return "", err
		}
		if m.Method != "externalAgentConfig/import/completed" {
			continue
		}
		var done struct {
			ImportID string `json:"importId"`
			Results  []struct {
				Successes []struct {
					Target string `json:"target"`
				} `json:"successes"`
				Failures []struct {
					Message string `json:"message"`
				} `json:"failures"`
			} `json:"itemTypeResults"`
		}
		if json.Unmarshal(m.Params, &done) != nil || (started.ImportID != "" && done.ImportID != "" && done.ImportID != started.ImportID) {
			continue
		}
		for _, r := range done.Results {
			for _, s := range r.Successes {
				if s.Target != "" {
					return s.Target, nil
				}
			}
			for _, f := range r.Failures {
				return "", fmt.Errorf("Codex could not import it: %s", f.Message)
			}
		}
		return "", nil
	}
}

// withoutVars is env less the variables named.
func withoutVars(env []string, names ...string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, n := range names {
			if k == n {
				drop = true
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}
