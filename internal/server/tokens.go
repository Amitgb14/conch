package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Amitgb14/conch/internal/proto"
)

// Access tokens (#350). Scoping by the caller's process tree (scope.go)
// fails open: a process whose ancestors include no pane is the person.
// A token is a right that travels with the connection instead of being
// guessed from where it comes from.
//
// Every pane is given one in CONCH_TOKEN for its own scope: a connection
// that presents it is held to that pane as if the walk had found it, so an
// agent whose helper was reparented under launchd is still that agent.
// The walk stays — it is how an agent started before this build, or one
// that dropped the variable, is found — and when it finds a pane, that
// pane wins over the token: the kernel's word over a string anybody
// reading another process's environment could copy.
//
// The person makes the others, for agents outside conch: a level (read,
// prompt, drive, full), the projects it is limited to, and a date it ends.
// Such a token only narrows. Presented from inside an agent's pane, both
// the pane's scope and the token's level apply.
//
// The secrets are kept as hashes in tokens.json, as phone.json keeps
// devices', and a token names the store that made it, so another
// machine's is refused by name rather than as unknown.

// tokenFile is the store's file in the configuration directory.
const tokenFile = "tokens.json"

// tokenNameMax bounds a token's name, which is shown in lists and logs.
const tokenNameMax = 64

type tokenRec struct {
	ID       string               `json:"id"`
	Name     string               `json:"name,omitempty"`
	Level    string               `json:"level"`
	Projects []proto.TokenProject `json:"projects,omitempty"`
	Hash     string               `json:"hash"`
	Created  time.Time            `json:"created"`
	Expires  time.Time            `json:"expires,omitzero"`
	// Pane is the pane a pane token belongs to; it ends with the pane.
	Pane string `json:"pane,omitempty"`
}

type tokenFileData struct {
	// Store marks this server's tokens: it is in every token it makes.
	Store  string     `json:"store"`
	Tokens []tokenRec `json:"tokens"`
}

// tokenBook is a server's tokens, by id.
type tokenBook struct {
	file string
	now  func() time.Time

	mu    sync.Mutex
	store string
	byID  map[string]*tokenRec
	used  map[string]time.Time // when each was last presented or used; not saved
}

func loadTokens(configDir string) *tokenBook {
	b := &tokenBook{now: time.Now, byID: map[string]*tokenRec{}, used: map[string]time.Time{}}
	if configDir != "" {
		b.file = filepath.Join(configDir, tokenFile)
	}
	if b.file != "" {
		raw, err := os.ReadFile(b.file)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			log.Printf("tokens: %v", err)
		default:
			var saved tokenFileData
			if err := json.Unmarshal(raw, &saved); err != nil {
				// Not thrown away: written over at the next change, which
				// revokes every token in it — and an unreadable file can
				// grant nothing in the meantime.
				log.Printf("tokens: %s: %v", b.file, err)
			} else {
				b.store = saved.Store
				for i := range saved.Tokens {
					t := saved.Tokens[i]
					if t.ID == "" || t.Hash == "" || (t.Level != proto.TokenPane && proto.TokenLevelRank(t.Level) == 0) {
						continue
					}
					b.byID[t.ID] = &t
				}
			}
		}
	}
	if !validHex(b.store, 8) {
		b.store = randomHex(4)
	}
	return b
}

func validHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func hashTokenSecret(secret string) string {
	sum := sha256.Sum256([]byte("conch-token\x00" + secret))
	return hex.EncodeToString(sum[:])
}

// mint makes a token's id, secret and the string handed out.
func (b *tokenBook) mintLocked(pane bool) (id, token, hash string) {
	for {
		id = randomHex(4)
		if _, taken := b.byID[id]; !taken {
			break
		}
	}
	secret := randomHex(32)
	kind := "t"
	if pane {
		kind = "p"
	}
	return id, "conch_" + kind + "_" + b.store + "_" + id + "_" + secret, hashTokenSecret(secret)
}

// parseToken splits a token into its parts; ok is false for anything not
// shaped like one.
func parseToken(tok string) (pane bool, store, id, secret string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(tok), "conch_")
	if !found {
		return false, "", "", "", false
	}
	parts := strings.Split(rest, "_")
	if len(parts) != 4 || (parts[0] != "t" && parts[0] != "p") ||
		!validHex(parts[1], 8) || !validHex(parts[2], 8) || !validHex(parts[3], 64) {
		return false, "", "", "", false
	}
	return parts[0] == "p", parts[1], parts[2], parts[3], true
}

func (b *tokenBook) saveLocked() {
	if b.file == "" {
		return
	}
	data := tokenFileData{Store: b.store, Tokens: make([]tokenRec, 0, len(b.byID))}
	for _, t := range b.byID {
		data.Tokens = append(data.Tokens, *t)
	}
	sort.Slice(data.Tokens, func(i, j int) bool { return data.Tokens[i].Created.Before(data.Tokens[j].Created) })
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		log.Printf("tokens: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(b.file), 0o700); err != nil {
		log.Printf("tokens: %v", err)
		return
	}
	tmp := b.file + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		log.Printf("tokens: %v", err)
		return
	}
	if err := os.Rename(tmp, b.file); err != nil {
		log.Printf("tokens: %v", err)
	}
}

// issuePane makes pane's own token.
func (b *tokenBook) issuePane(pane string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	id, tok, hash := b.mintLocked(true)
	b.byID[id] = &tokenRec{ID: id, Name: "pane " + pane, Level: proto.TokenPane, Hash: hash, Created: b.now(), Pane: pane}
	b.saveLocked()
	return tok
}

// dropPane ends the tokens of a pane that is gone.
func (b *tokenBook) dropPane(pane string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	changed := false
	for id, t := range b.byID {
		if t.Level == proto.TokenPane && t.Pane == pane {
			delete(b.byID, id)
			changed = true
		}
	}
	if changed {
		b.saveLocked()
	}
}

// dropPaneExcept ends a pane's tokens but keep, or only drop when drop is
// set: a pane whose program was replaced keeps the new program's token
// alone, and one whose new program failed to start loses only the token
// made for it.
func (b *tokenBook) dropPaneExcept(pane, keep, drop string) {
	_, _, keepID, _, _ := parseToken(keep)
	_, _, dropID, _, _ := parseToken(drop)
	b.mu.Lock()
	defer b.mu.Unlock()
	changed := false
	for id, t := range b.byID {
		if t.Level != proto.TokenPane || t.Pane != pane || (keep != "" && id == keepID) || (drop != "" && id != dropID) {
			continue
		}
		delete(b.byID, id)
		changed = true
	}
	if changed {
		b.saveLocked()
	}
}

// keepPanes drops the pane tokens of panes that are not alive: after a
// restart nothing is, and after a reload only what was adopted. Pane ids
// start again from p1 after a restart, so a token left behind would
// otherwise name a pane it was never given to.
func (b *tokenBook) keepPanes(alive func(string) bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	changed := false
	for id, t := range b.byID {
		if t.Level == proto.TokenPane && !alive(t.Pane) {
			delete(b.byID, id)
			changed = true
		}
	}
	if changed {
		b.saveLocked()
	}
}

// checkTokenName is what a token may be called.
func checkTokenName(name string) *proto.Error {
	switch {
	case name == "":
		return proto.Errorf(proto.ErrBadRequest, "a token needs a name, to be listed and revoked by")
	case len(name) > tokenNameMax:
		return proto.Errorf(proto.ErrBadRequest, "a token's name is at most %d bytes", tokenNameMax)
	case strings.HasPrefix(name, "pane "):
		return proto.Errorf(proto.ErrBadRequest, "names starting \"pane \" are conch's, for panes' own tokens")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return proto.Errorf(proto.ErrBadRequest, "a token's name is one line of text")
		}
	}
	return nil
}

// create makes a token the person asked for; projects are already
// resolved.
func (b *tokenBook) create(p proto.TokenCreateParams, projects []proto.TokenProject) (proto.TokenCreateResult, *proto.Error) {
	name := strings.TrimSpace(p.Name)
	if perr := checkTokenName(name); perr != nil {
		return proto.TokenCreateResult{}, perr
	}
	if proto.TokenLevelRank(p.Level) == 0 {
		return proto.TokenCreateResult{}, proto.Errorf(proto.ErrBadRequest, "%q is not a level: %s", p.Level, strings.Join(proto.TokenLevels, ", "))
	}
	if p.ExpiresIn < 0 {
		return proto.TokenCreateResult{}, proto.Errorf(proto.ErrBadRequest, "a token cannot have expired already")
	}
	if p.Never && p.ExpiresIn > 0 {
		return proto.TokenCreateResult{}, proto.Errorf(proto.ErrBadRequest, "a token either expires or never does")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range b.byID {
		if t.Level != proto.TokenPane && t.Name == name {
			return proto.TokenCreateResult{}, proto.Errorf(proto.ErrBadRequest, "there is a token named %q already; revoke it first, or choose another name", name)
		}
	}
	now := b.now()
	rec := &tokenRec{Name: name, Level: p.Level, Projects: projects, Created: now}
	switch {
	case p.Never:
	case p.ExpiresIn > 0:
		// Capped so the date stays one time.Time can hold.
		rec.Expires = now.Add(time.Duration(min(p.ExpiresIn, int64(100*365*24*3600))) * time.Second)
	default:
		rec.Expires = now.Add(proto.TokenDefaultLife)
	}
	id, tok, hash := b.mintLocked(false)
	rec.ID, rec.Hash = id, hash
	b.byID[id] = rec
	b.saveLocked()
	return proto.TokenCreateResult{Token: tok, Info: b.infoLocked(rec)}, nil
}

func (b *tokenBook) infoLocked(t *tokenRec) proto.TokenInfo {
	return proto.TokenInfo{ID: t.ID, Name: t.Name, Level: t.Level, Projects: t.Projects,
		Created: t.Created, Expires: t.Expires, Used: b.used[t.ID]}
}

// list is the tokens the person made, oldest first; panes' own are not
// theirs to manage and end with their panes.
func (b *tokenBook) list() []proto.TokenInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []proto.TokenInfo{}
	for _, t := range b.byID {
		if t.Level != proto.TokenPane {
			out = append(out, b.infoLocked(t))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// revoke ends a token the person made, by name or id.
func (b *tokenBook) revoke(name string) (proto.TokenInfo, *proto.Error) {
	name = strings.TrimSpace(name)
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, t := range b.byID {
		if t.Level != proto.TokenPane && (t.Name == name || t.ID == name) {
			info := b.infoLocked(t)
			delete(b.byID, id)
			delete(b.used, id)
			b.saveLocked()
			return info, nil
		}
	}
	return proto.TokenInfo{}, proto.Errorf(proto.ErrNotFound, "no token named %q (conch token list lists them)", name)
}

// check finds the token tok is. ignored is a pane token another server
// made: a process that inherited its pane's variable and talks to some
// other conch, which grants nothing and takes nothing away. A token the
// person made for another machine is refused, by name, since somebody
// handed it over expecting it to work.
func (b *tokenBook) check(tok string) (rec tokenRec, ignored bool, perr *proto.Error) {
	pane, store, id, secret, ok := parseToken(tok)
	if !ok {
		return rec, false, proto.Errorf(proto.ErrUnauthorized, "that is not a conch access token")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if store != b.store {
		if pane {
			return rec, true, nil
		}
		return rec, false, proto.Errorf(proto.ErrUnauthorized, "that token was made by another machine's conch; make one on this machine (conch -m MACHINE token create)")
	}
	t, found := b.byID[id]
	// The same answer for an id never made, a revoked token and a forged
	// secret: which of them it is would help only somebody guessing.
	if !found || subtle.ConstantTimeCompare([]byte(t.Hash), []byte(hashTokenSecret(secret))) != 1 ||
		(t.Level == proto.TokenPane) != pane {
		return rec, false, proto.Errorf(proto.ErrUnauthorized, "that token is unknown or revoked")
	}
	if !t.Expires.IsZero() && !b.now().Before(t.Expires) {
		return rec, false, proto.Errorf(proto.ErrUnauthorized, "the token %q expired on %s", t.Name, t.Expires.Local().Format("2 Jan 2006 15:04"))
	}
	b.used[id] = b.now()
	return *t, false, nil
}

// still says whether the token a connection presented still holds: one
// revoked or expired since ends for connections already holding it, not
// only for the next one.
func (b *tokenBook) still(id string) *proto.Error {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.byID[id]
	if !ok {
		return proto.Errorf(proto.ErrUnauthorized, "its token has been revoked")
	}
	if !t.Expires.IsZero() && !b.now().Before(t.Expires) {
		return proto.Errorf(proto.ErrUnauthorized, "its token expired on %s", t.Expires.Local().Format("2 Jan 2006 15:04"))
	}
	b.used[id] = b.now()
	return nil
}

// ---- the connection's side ----

// authenticate answers auth.token: the connection is held to the token
// from now on.
func (s *Server) authenticate(c *client, p proto.AuthTokenParams) (proto.AuthTokenResult, *proto.Error) {
	rec, ignored, perr := s.tokens.check(p.Token)
	if perr != nil {
		log.Printf("token: refused a connection's token: %s", perr.Message)
		return proto.AuthTokenResult{}, perr
	}
	if ignored {
		return proto.AuthTokenResult{Ignored: true}, nil
	}
	res := proto.AuthTokenResult{Name: rec.Name, Level: rec.Level, Expires: rec.Expires, Projects: rec.Projects}
	if rec.Level == proto.TokenPane {
		if _, perr := s.get(rec.Pane); perr != nil {
			return proto.AuthTokenResult{}, proto.Errorf(proto.ErrUnauthorized, "pane %s has closed, and its token with it", rec.Pane)
		}
		res.Pane = rec.Pane
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" {
		if c.token == rec.ID {
			return res, nil
		}
		return proto.AuthTokenResult{}, proto.Errorf(proto.ErrBadRequest, "this connection holds another token already")
	}
	c.token = rec.ID
	if rec.Level != proto.TokenPane {
		g := rec
		c.grant = &g
		return res, nil
	}
	switch {
	case c.pane == "":
		// What this is for: a process the walk could not place.
		c.pane, c.paneToken = rec.Pane, true
	case c.pane != rec.Pane:
		// The kernel's word wins: another pane's token is a string
		// anybody who can read that pane's environment has.
		log.Printf("token: a connection from pane %s presented pane %s's token; it stays %s's", c.pane, rec.Pane, c.pane)
		res.Pane = c.pane
	}
	return res, nil
}

// tokenRef is every field a token's limits are checked against: the
// scope's targets, and the folders a method works in.
type tokenRef struct {
	scopeRef
	// Run is the task graph run.* methods name: its project is the target.
	Run  string `json:"run"`
	Cwd  string `json:"cwd"`
	Dir  string `json:"dir"`
	Path string `json:"path"`
	Root string `json:"root"`
}

// tokenReads are the methods that only read. A method not named in any of
// these lists needs a full token: a method added later is closed to the
// lesser levels until somebody decides otherwise.
var tokenReads = map[string]bool{
	proto.MethodHello: true, proto.MethodPing: true, proto.MethodAuthToken: true,
	proto.MethodPaneCaller: true, proto.MethodActFor: true,
	proto.MethodPaneList: true, proto.MethodPaneRead: true, proto.MethodPaneSubscribe: true, proto.MethodPaneUnsubscribe: true,
	proto.MethodPaneScroll: true, proto.MethodPaneSearch: true, proto.MethodPaneRaw: true,
	proto.MethodUsageHistory: true, proto.MethodAgentExplain: true, proto.MethodAgentStatus: true, proto.MethodAgentLimits: true,
	proto.MethodAgentChat: true, proto.MethodAgentSetup: true, proto.MethodArtifactList: true,
	proto.MethodGroupList: true, proto.MethodProjectList: true, proto.MethodProjectChanges: true, proto.MethodProjectDiff: true,
	proto.MethodProjectLog: true, proto.MethodProjectCommit: true, proto.MethodProjectResolve: true,
	proto.MethodFSList: true, proto.MethodFSRead: true, proto.MethodFSFind: true, proto.MethodFSSearch: true, proto.MethodFSLines: true,
	proto.MethodShellThemes: true, proto.MethodSessionList: true, proto.MethodSessionSearch: true, proto.MethodSessionExport: true,
	proto.MethodViewGet: true, proto.MethodProjectIssues: true, proto.MethodProjectTickets: true, proto.MethodTicketGet: true,
	proto.MethodTicketOverview: true, proto.MethodScheduleList: true, proto.MethodReviewList: true, proto.MethodAutopilotGet: true,
	proto.MethodBranchCILog: true, proto.MethodAutopilotActivity: true, proto.MethodCIStats: true, proto.MethodBranchRebased: true,
	proto.MethodIntegrationDetect: true, proto.MethodBranchPreviews: true, proto.MethodBranchDeployLog: true,
	proto.MethodWorktreeStale: true, proto.MethodBranchStale: true, proto.MethodWorktreeDescribe: true,
	proto.MethodSystemStats: true, proto.MethodAccountList: true, proto.MethodAccountSuggest: true, proto.MethodPaneEndedList: true,
}

// tokenPrompts send to an agent: what a person replying to it does.
var tokenPrompts = map[string]bool{
	proto.MethodAgentPrompt: true, proto.MethodAgentReply: true, proto.MethodAgentBroadcast: true,
	proto.MethodPaneSendText: true, proto.MethodPaneSendKeys: true, proto.MethodAgentAttach: true,
}

// tokenDrives start and stop agents.
var tokenDrives = map[string]bool{
	proto.MethodPaneCreate: true, proto.MethodTaskCreate: true, proto.MethodSessionResume: true,
	proto.MethodPaneClose: true, proto.MethodAgentInterrupt: true, proto.MethodWorktreeAdd: true,
	proto.MethodAgentSwitch: true, proto.MethodAgentSwitchCancel: true,
}

// tokenUnlimited are the methods only a full token limited to no project
// may use: they are about every project at once, or about tokens.
var tokenUnlimited = map[string]bool{
	proto.MethodTokenCreate: true, proto.MethodTokenList: true, proto.MethodTokenRevoke: true,
}

// tokenNeeds is the level a call needs.
func tokenNeeds(method string, r tokenRef, create proto.PaneCreateParams) string {
	if writes, ok := writesHome[method]; ok {
		if writes(r.scopeRef) {
			return proto.TokenFull
		}
		return proto.TokenRead
	}
	switch {
	case tokenReads[method]:
		if r.SSH != "" {
			return proto.TokenFull // another host, with somebody's own login
		}
		return proto.TokenRead
	case tokenPrompts[method]:
		if method == proto.MethodAgentBroadcast && r.Shells {
			return proto.TokenFull
		}
		return proto.TokenPrompt
	case tokenDrives[method]:
		// An agent, and nothing else: a command of its own is anything —
		// and so are flags or an environment for the agent, which reach
		// a shell ("--model $(…)") or tell it to ask nobody
		// (--dangerously-skip-permissions). Below full a token starts an
		// agent as an agent in a pane may: which one, and its --model.
		if method == proto.MethodPaneCreate {
			if create.Agent == "" || len(create.Command) > 0 || create.SSH != "" {
				return proto.TokenFull
			}
			if _, why := agentLaunch(create); why != "" {
				return proto.TokenFull
			}
		}
		return proto.TokenDrive
	}
	return proto.TokenFull
}

// tokenAllows refuses a call the connection's token does not cover. A
// connection without one is left to inScope.
func (s *Server) tokenAllows(c *client, msg proto.Message) *proto.Error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	g := c.grant
	c.mu.Unlock()
	if g == nil {
		return nil
	}
	if perr := s.tokens.still(g.ID); perr != nil {
		log.Printf("token %q: refused %s: %s", g.Name, msg.Method, perr.Message)
		return perr
	}
	var r tokenRef
	var create proto.PaneCreateParams
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &r); err != nil {
			return tokenRefused(g, "%s: params conch cannot check the token against: %v", msg.Method, err)
		}
		if msg.Method == proto.MethodPaneCreate {
			if err := json.Unmarshal(msg.Params, &create); err != nil {
				return tokenRefused(g, "%s: params conch cannot check the token against: %v", msg.Method, err)
			}
			r.Cwd = create.Cwd
		}
	}
	need := tokenNeeds(msg.Method, r, create)
	if proto.TokenLevelRank(g.Level) < proto.TokenLevelRank(need) {
		return tokenRefused(g, "%s: that needs a %s token", tokenVerb(msg.Method), need)
	}
	limited := len(g.Projects) > 0
	if tokenUnlimited[msg.Method] && limited {
		return tokenRefused(g, "%s: that needs a full token for every project", tokenVerb(msg.Method))
	}
	if scoped[msg.Method] == scopePerson {
		return tokenRefused(g, "%s: only the person may, from conch's own window", tokenVerb(msg.Method))
	}
	if limited {
		switch scoped[msg.Method] {
		case scopeServer, scopeHome:
			return tokenRefused(g, "%s: that reaches past its projects", tokenVerb(msg.Method))
		}
		if _, ok := writesHome[msg.Method]; ok && need == proto.TokenFull {
			return tokenRefused(g, "%s: that reaches past its projects", tokenVerb(msg.Method))
		}
		if perr := s.tokenTargets(g, msg.Method, r, create); perr != nil {
			return perr
		}
	}
	// Below full, files are read only inside conch's projects, every one
	// when the token is limited to none: a read token handed to a tool
	// for its screens and diffs is not handed ~/.ssh and the keys in
	// config.toml, which would make it full.
	if !limited && g.Level != proto.TokenFull && tokenFileReads[msg.Method] {
		if perr := s.tokenInProjects(g, msg.Method, r); perr != nil {
			return perr
		}
	}
	// Below full, what is typed goes to agents only: in a shell it runs as
	// a command, and what is stopped is an agent, not somebody's shell.
	if g.Level != proto.TokenFull && (tokenPrompts[msg.Method] || msg.Method == proto.MethodPaneClose) {
		for _, id := range r.paneIDs() {
			if perr := s.tokenAgentPane(g, msg.Method, id); perr != nil {
				return perr
			}
		}
	}
	return nil
}

// paneIDs is every pane a call names.
func (r tokenRef) paneIDs() []string {
	ids := append(append([]string{}, r.IDs...), r.Panes...)
	if r.ID != "" {
		ids = append(ids, r.ID)
	}
	if r.PaneID != "" {
		ids = append(ids, r.PaneID)
	}
	return ids
}

// tokenAgentPane refuses a pane below full that runs no agent; one that
// has ended may be closed.
func (s *Server) tokenAgentPane(g *tokenRec, method, id string) *proto.Error {
	e, perr := s.get(id)
	if perr != nil {
		return nil // the method's own not-found
	}
	info := e.info()
	if info.Agent != nil || (method == proto.MethodPaneClose && info.State != proto.PaneRunning) {
		return nil
	}
	return tokenRefused(g, "%s %s: it runs no agent, and a %s token reaches only agents", tokenVerb(method), id, g.Level)
}

// tokenTargets holds a call to the token's projects: every pane, project
// and folder it names. A target that does not exist is let through, for
// the method's own not-found; reading lists is not filtered, as it is
// not for an agent in a pane.
func (s *Server) tokenTargets(g *tokenRec, method string, r tokenRef, create proto.PaneCreateParams) *proto.Error {
	allowed := map[string]bool{}
	for _, p := range g.Projects {
		allowed[p.ID] = true
	}
	inProject := func(id string) bool { return allowed[id] }
	for _, id := range append(r.paneIDs(), r.SSH) {
		if id == "" {
			continue
		}
		if e, perr := s.get(id); perr == nil {
			if pid := e.info().ProjectID; !inProject(pid) {
				return tokenRefused(g, "%s %s: it is not in the token's projects", tokenVerb(method), id)
			}
			continue
		}
		if id == r.ID {
			if _, perr := s.projects.get(id); perr == nil && !inProject(id) {
				return tokenRefused(g, "%s in project %s: it is not one of the token's projects", tokenVerb(method), id)
			}
		}
	}
	if r.ProjectID != "" {
		if _, perr := s.projects.get(r.ProjectID); perr == nil && !inProject(r.ProjectID) {
			return tokenRefused(g, "%s in project %s: it is not one of the token's projects", tokenVerb(method), r.ProjectID)
		}
	}
	// A run is named by its id or name, not its project, so it is looked
	// up: removing, starting or approving another project's run is
	// reaching past the token's projects as surely as its panes are.
	if r.Run != "" && s.graph != nil {
		if run, perr := s.graph.get(r.Run); perr == nil && !inProject(run.ProjectID) {
			return tokenRefused(g, "%s %s: it is not in the token's projects", tokenVerb(method), r.Run)
		}
	}
	if method == proto.MethodPaneCreate && (create.NoProject || create.Cwd == "") {
		return tokenRefused(g, "start a pane outside its projects: give the folder of one of them")
	}
	if (tokenFileReads[method] || fsMethods[method] && method != proto.MethodPaneCreate) && r.Path == "" && r.Root == "" {
		return tokenRefused(g, "%s: a token limited to projects names a folder in one of them", tokenVerb(method))
	}
	for _, dir := range tokenDirs(r) {
		p := s.projects.containing(dir)
		if p == nil || !inProject(p.id) {
			return tokenRefused(g, "%s %s: it is outside the token's projects", tokenVerb(method), dir)
		}
	}
	return nil
}

// tokenFileReads are the methods below full that read the computer's
// files by path.
var tokenFileReads = map[string]bool{
	proto.MethodFSList: true, proto.MethodFSRead: true, proto.MethodFSFind: true,
	proto.MethodFSSearch: true, proto.MethodFSLines: true, proto.MethodFSDownload: true,
}

// tokenInProjects refuses a file call naming no folder, or one in no
// project conch has.
func (s *Server) tokenInProjects(g *tokenRec, method string, r tokenRef) *proto.Error {
	if r.Path == "" && r.Root == "" {
		return tokenRefused(g, "%s: a %s token names a folder in one of conch's projects", tokenVerb(method), g.Level)
	}
	for _, dir := range tokenDirs(r) {
		if dir == "" || s.projects.containing(dir) == nil {
			return tokenRefused(g, "%s %s: a %s token reads only inside conch's projects", tokenVerb(method), dir, g.Level)
		}
	}
	return nil
}

// tokenDirs is the folders a call works in, made absolute and clean so
// "proj/../../etc" is judged where it lands.
func tokenDirs(r tokenRef) []string {
	var dirs []string
	add := func(p string) {
		if p == "" {
			return
		}
		if strings.HasPrefix(p, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, strings.TrimPrefix(p, "~"))
			}
		}
		if !filepath.IsAbs(p) {
			if r.Root == "" || !filepath.IsAbs(r.Root) {
				dirs = append(dirs, "") // relative to nothing it can judge: refused
				return
			}
			p = filepath.Join(r.Root, p)
		}
		dirs = append(dirs, filepath.Clean(p))
	}
	add(r.Cwd)
	add(r.Dir)
	add(r.Root)
	add(r.Path)
	return dirs
}

func tokenVerb(method string) string {
	if v, ok := tokenVerbs[method]; ok {
		return v
	}
	return verb(method)
}

var tokenVerbs = map[string]string{
	proto.MethodPaneCreate: "start a pane", proto.MethodTaskCreate: "start a task", proto.MethodSessionResume: "resume a session",
	proto.MethodWorktreeAdd: "add a worktree", proto.MethodTokenCreate: "make a token", proto.MethodTokenList: "list tokens",
	proto.MethodTokenRevoke: "revoke a token", proto.MethodAgentReply: "reply to", proto.MethodPaneRead: "read",
	proto.MethodPaneSubscribe: "watch", proto.MethodPaneRaw: "watch", proto.MethodFSWrite: "write files in",
	// A file on this computer: scope.go's verbs for these are about a
	// terminal's ssh host, which a token's refusal here is not.
	proto.MethodFSList: "list files in", proto.MethodFSRead: "read", proto.MethodFSFind: "find files in",
	proto.MethodFSSearch: "search", proto.MethodFSLines: "read", proto.MethodFSDownload: "copy files from",
	proto.MethodFSMkdir: "make folders in", proto.MethodFSUpload: "copy files into",
}

// tokenRefused is a call the token does not cover, written down with the
// token's name: the log is the record of what a token was kept from.
func tokenRefused(g *tokenRec, format string, args ...any) *proto.Error {
	what := fmt.Sprintf(format, args...)
	log.Printf("token %q (%s): refused %s", g.Name, g.Level, what)
	return proto.Errorf(proto.ErrOutOfScope, "the %s token %q may not %s", g.Level, g.Name, what)
}

// resolveTokenProjects turns what the person typed — a name, an id or a
// folder — into this server's projects.
func (s *Server) resolveTokenProjects(refs []string) ([]proto.TokenProject, *proto.Error) {
	var out []proto.TokenProject
	seen := map[string]bool{}
	all := s.projects.list()
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		var hits []proto.ProjectInfo
		abs := ref
		if filepath.IsAbs(ref) {
			abs = filepath.Clean(ref)
		}
		for _, p := range all {
			if p.ID == ref || p.Path == abs {
				hits = []proto.ProjectInfo{p}
				break
			}
			if p.Name == ref {
				hits = append(hits, p)
			}
		}
		switch len(hits) {
		case 0:
			return nil, proto.Errorf(proto.ErrNotFound, "no project %q here (conch project ls lists them)", ref)
		case 1:
		default:
			return nil, proto.Errorf(proto.ErrBadRequest, "%d projects are named %q; give its folder or its id", len(hits), ref)
		}
		if p := hits[0]; !seen[p.ID] {
			seen[p.ID] = true
			out = append(out, proto.TokenProject{ID: p.ID, Name: p.Name, Path: p.Path})
		}
	}
	return out, nil
}
