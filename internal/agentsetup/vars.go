package agentsetup

import (
	"fmt"
	"regexp"
	"strings"
)

// Every agent refers to an environment variable its own way, and one of
// them not at all: Claude Code writes ${VAR}, Gemini $VAR or ${VAR},
// OpenCode {env:VAR}, Devin ${env:VAR}, and Codex expands nothing in its
// config — it passes a variable on by name, with env_vars,
// bearer_token_env_var and env_http_headers. Copying "${TOKEN}" from one
// file to another as it stands gives the server the text "${TOKEN}".
//
// So a server read from any agent's file has its references put in one
// form, ${VAR}, and each is written back in the form the agent it goes to
// expands — or, where it cannot say it at all, the server is left out and
// the reason says why.

// anyRef matches a reference in any agent's form: ${VAR}, ${VAR:-default},
// $VAR, {env:VAR} and ${env:VAR}.
var anyRef = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}|\{env:([A-Za-z_][A-Za-z0-9_]*)\}|\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-[^}]*)?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// neutralRef matches a reference once it is in the one form.
var neutralRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// wholeRef is a value that is nothing but a reference, perhaps after an
// authorization scheme: "${TOKEN}", "Bearer ${TOKEN}".
var wholeRef = regexp.MustCompile(`^(?:([A-Za-z]+) )?\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// neutral puts every reference in s in the one form, ${VAR}. A default
// (${VAR:-x}) is dropped: not every agent can say one.
func neutral(s string) string {
	return anyRef.ReplaceAllStringFunc(s, func(m string) string {
		for _, name := range anyRef.FindStringSubmatch(m)[1:] {
			if name != "" {
				return "${" + name + "}"
			}
		}
		return m
	})
}

// refFor writes the references in s in the form agent expands.
func refFor(agent, s string) string {
	var form string
	switch agent {
	case "opencode":
		form = "{env:$1}"
	case "devin":
		form = "$${env:$1}"
	default:
		return s // ${VAR}, as Claude Code and Gemini read it
	}
	return neutralRef.ReplaceAllString(s, form)
}

func refsFor(agent string, m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = refFor(agent, v)
	}
	return out
}

// safeValue reports whether a value can travel: nothing, or a reference
// and perhaps the scheme before it. Anything else is a key, a token or a
// password written out, and is not copied.
func safeValue(v string) bool {
	v = strings.TrimSpace(neutral(v))
	return v == "" || wholeRef.MatchString(v)
}

// codexVars is how Codex is given a server's variables: env_vars for the
// ones passed on under their own name, and for an http server
// bearer_token_env_var and env_http_headers. reason is why it cannot be,
// when Codex has no way to say what the server wants.
type codexVars struct {
	envVars     []string
	bearer      string
	headerVars  map[string]string // header → variable
	literalEnv  map[string]string // values with no reference in them
	literalHead map[string]string
}

func codexVarsOf(s mcpServer) (codexVars, string) {
	var cv codexVars
	for _, k := range sortedStrings(s.Env) {
		v := strings.TrimSpace(s.Env[k])
		m := wholeRef.FindStringSubmatch(v)
		switch {
		case !neutralRef.MatchString(v):
			if cv.literalEnv == nil {
				cv.literalEnv = map[string]string{}
			}
			cv.literalEnv[k] = v
		case m != nil && m[1] == "" && m[2] == k:
			cv.envVars = append(cv.envVars, k)
		default:
			return cv, fmt.Sprintf("Codex passes a variable on only under its own name, and %s is %s", k, v)
		}
	}
	for _, h := range sortedStrings(s.Headers) {
		v := strings.TrimSpace(s.Headers[h])
		m := wholeRef.FindStringSubmatch(v)
		switch {
		case !neutralRef.MatchString(v):
			if cv.literalHead == nil {
				cv.literalHead = map[string]string{}
			}
			cv.literalHead[h] = v
		case m != nil && strings.EqualFold(h, "Authorization") && strings.EqualFold(m[1], "Bearer") && cv.bearer == "":
			cv.bearer = m[2]
		case m != nil && m[1] == "":
			if cv.headerVars == nil {
				cv.headerVars = map[string]string{}
			}
			cv.headerVars[h] = m[2]
		default:
			return cv, "Codex cannot build the " + h + " header from a variable and other text"
		}
	}
	for _, a := range append([]string{s.Command, s.URL}, s.Args...) {
		if neutralRef.MatchString(a) {
			return cv, "Codex does not expand ${VAR} in a command, its arguments or a URL"
		}
	}
	if s.Transport == "sse" {
		return cv, "Codex speaks streamable HTTP to a server, not SSE"
	}
	return cv, ""
}

// unwritable says why a server cannot be declared for agent, or "".
func unwritable(agent string, s mcpServer) string {
	if agent == "codex" {
		_, why := codexVarsOf(s)
		return why
	}
	return ""
}
