package sandbox

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// pollEvery is how often a wait asks for the sandbox's state; tests shorten it.
var pollEvery = 2 * time.Second

// PollFaster shortens the wait between polls to d until the test ends,
// for the tests of a program built on this package: a fake provider
// answers at once, and two seconds a poll adds up across a suite.
func PollFaster(t interface{ Cleanup(func()) }, d time.Duration) {
	old := pollEvery
	pollEvery = d
	t.Cleanup(func() { pollEvery = old })
}

// maxBody bounds a reply read from the API.
const maxBody = 4 << 20

// maxPages bounds List, against a cursor that never ends.
const maxPages = 50

func firstSet(vs ...string) string {
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// replyMessage is the reason out of an error reply: a JSON message — at
// message (one, or a list for a rejected body), error, or error.message
// (boat.dev's) — else the text itself, cut short. An HTML page from a
// proxy says nothing useful, so it says nothing.
func replyMessage(data []byte) string {
	var e struct {
		Message json.RawMessage `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil {
		var one, errText string
		var many []string
		var inner struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		_ = json.Unmarshal(e.Message, &one)
		_ = json.Unmarshal(e.Message, &many)
		_ = json.Unmarshal(e.Error, &errText)
		_ = json.Unmarshal(e.Error, &inner)
		if m := firstSet(one, strings.Join(many, "; "), errText, inner.Message, inner.Code); m != "" {
			return m
		}
	}
	msg := strings.TrimSpace(string(data))
	if strings.HasPrefix(msg, "<") {
		return ""
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}

// nameChars are what a sandbox's name of conch's is made of.
var nameChars = regexp.MustCompile(`[^a-z0-9-]+`)
