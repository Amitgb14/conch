package proto

import (
	"slices"
	"strings"
	"time"
)

// Access tokens give a connection a limited right that does not depend on
// where it comes from. Every pane is handed one for its own scope (in
// CONCH_TOKEN), so an agent whose process tree has been reparented is
// still held to its pane; and the person makes others for agents outside
// conch — read only, prompt, drive or full, for some projects, until a
// date. A connection presents one with auth.token; a server without
// token.v1 cannot check one, and a client then says so rather than act as
// the person (a pane's own token excepted: the process walk still scopes
// that connection as it always did).

// CapToken is auth.token, token.create, token.list and token.revoke, and
// a token in each pane's environment.
const CapToken = "token.v1"

const (
	// MethodAuthToken presents a token: the connection is held to it from
	// then on. It can be given once.
	MethodAuthToken = "auth.token"
	// MethodTokenCreate makes a token for an agent outside conch; its
	// secret is in the answer and nowhere else.
	MethodTokenCreate = "token.create"
	// MethodTokenList lists the tokens the person made (not the panes').
	MethodTokenList = "token.list"
	// MethodTokenRevoke ends a token at once, by name or id.
	MethodTokenRevoke = "token.revoke"
)

// ErrUnauthorized is a token the server refused: unknown, revoked,
// expired, forged or another machine's.
const ErrUnauthorized = "unauthorized"

// The access levels, least first: each may do what the one before it may.
const (
	// TokenRead reads: panes' screens, projects, diffs, sessions.
	TokenRead = "read"
	// TokenPrompt also sends to agents — a prompt, a reply, keys — but
	// never types into a shell, where what is typed runs as a command.
	TokenPrompt = "prompt"
	// TokenDrive also starts and stops agents: tasks, agent panes,
	// resumed sessions, closing and interrupting.
	TokenDrive = "drive"
	// TokenFull does what the person can, within its projects.
	TokenFull = "full"
	// TokenPane is a pane's own token: the scope an agent in that pane
	// already has. It is never made by hand.
	TokenPane = "pane"
)

// TokenLevels are the levels a token can be made with, least first.
var TokenLevels = []string{TokenRead, TokenPrompt, TokenDrive, TokenFull}

// TokenLevelRank orders the levels; 0 for one that is not a level.
func TokenLevelRank(level string) int {
	return slices.Index(TokenLevels, level) + 1
}

// TokenEnv is the variable a token is read from.
const TokenEnv = "CONCH_TOKEN"

// PaneTokenPrefix starts a pane's own token, so a client can tell it from
// one the person made without asking the server.
const PaneTokenPrefix = "conch_p_"

// IsPaneToken reports whether tok is a pane's own token.
func IsPaneToken(tok string) bool { return strings.HasPrefix(tok, PaneTokenPrefix) }

// AuthTokenParams presents a token.
type AuthTokenParams struct {
	Token string `json:"token"`
}

// AuthTokenResult says what the connection is now held to. Ignored is a
// pane token of another server — one inherited by a process talking to a
// conch that did not start its pane — which grants nothing and takes
// nothing away.
type AuthTokenResult struct {
	Name    string    `json:"name,omitempty"`
	Level   string    `json:"level,omitempty"`
	Pane    string    `json:"pane,omitempty"`
	Ignored bool      `json:"ignored,omitempty"`
	Expires time.Time `json:"expires,omitzero"`
	// Projects are the projects it is limited to; none is every one.
	Projects []TokenProject `json:"projects,omitempty"`
}

// TokenProject is one project a token is limited to.
type TokenProject struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

// TokenCreateParams makes a token. Projects are names, ids or paths of
// projects this server has; none is every project. ExpiresIn is seconds:
// zero is thirty days, and Never makes one that does not expire.
type TokenCreateParams struct {
	Name      string   `json:"name"`
	Level     string   `json:"level"`
	Projects  []string `json:"projects,omitempty"`
	ExpiresIn int64    `json:"expires_in,omitempty"`
	Never     bool     `json:"never,omitempty"`
}

// TokenDefaultLife is how long a token lasts when nobody says.
const TokenDefaultLife = 30 * 24 * time.Hour

// TokenInfo is a token as listed: never its secret, which is kept only
// as a hash.
type TokenInfo struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Level    string         `json:"level"`
	Projects []TokenProject `json:"projects,omitempty"`
	Created  time.Time      `json:"created"`
	Expires  time.Time      `json:"expires,omitzero"`
	Used     time.Time      `json:"used,omitzero"`
}

// TokenCreateResult carries the token itself, once.
type TokenCreateResult struct {
	Token string    `json:"token"`
	Info  TokenInfo `json:"info"`
}

// TokenListResult is every token the person made.
type TokenListResult struct {
	Tokens []TokenInfo `json:"tokens"`
}

// TokenRevokeParams names a token by its name or its id.
type TokenRevokeParams struct {
	Name string `json:"name"`
}
