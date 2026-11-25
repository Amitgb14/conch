package sessions

import (
	"encoding/json"
	"os"
	"time"
)

// qwenLine is a line of a Qwen Code session as qwenConversation reads it:
// a record of qwenRecord's shape, with the chain that orders it and what a
// tool call returned.
type qwenLine struct {
	UUID       string    `json:"uuid"`
	ParentUUID *string   `json:"parentUuid"`
	Type       string    `json:"type"`
	Subtype    string    `json:"subtype"`
	Timestamp  time.Time `json:"timestamp"`
	Message    *struct {
		Role  string          `json:"role"`
		Parts json.RawMessage `json:"parts"`
	} `json:"message"`
	ToolCallResult *struct {
		CallID string          `json:"callId"`
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
	} `json:"toolCallResult"`
}

// qwenChain is a session's records in the order Qwen Code resumes them
// (0.27, chatRecordingService.ts): each names the one before it by
// parentUuid, so a conversation rewound and taken another way keeps both
// in the file, and only the branch the last record is on is the
// conversation. Records without ids, from older releases, are read in the
// file's order.
func qwenChain(recs []qwenLine) []qwenLine {
	byID := map[string]int{}
	last := -1
	for i, r := range recs {
		if r.UUID == "" {
			continue
		}
		byID[r.UUID] = i
		// A title or a snapshot is a record beside the conversation,
		// not the end of it.
		if r.Type == "user" || r.Type == "assistant" || r.Type == "tool_result" {
			last = i
		}
	}
	if last < 0 {
		return recs
	}
	var chain []qwenLine
	seen := map[string]bool{}
	for i := last; i >= 0; {
		r := recs[i]
		if seen[r.UUID] {
			break // a loop: what was read so far stands
		}
		seen[r.UUID] = true
		chain = append(chain, r)
		if r.ParentUUID == nil || *r.ParentUUID == "" {
			break
		}
		j, ok := byID[*r.ParentUUID]
		if !ok {
			break // a gap: Qwen resumes from there too
		}
		i = j
	}
	for a, b := 0, len(chain)-1; a < b; a, b = a+1, b-1 {
		chain[a], chain[b] = chain[b], chain[a]
	}
	return chain
}

// qwenConversation reads a Qwen Code session whole: what was said, each
// tool call from the assistant's functionCall parts and its result from
// the tool_result record after it. Thoughts are left out, and so are
// records with a subtype — something conch did not type (another agent,
// a goal's runtime message) or Qwen's own bookkeeping.
func qwenConversation(path string) (Conversation, error) {
	f, err := os.Open(path)
	if err != nil {
		return Conversation{}, err
	}
	defer f.Close()
	var recs []qwenLine
	lines(f, 1<<30, func(b []byte) bool {
		var r qwenLine
		if json.Unmarshal(b, &r) == nil {
			recs = append(recs, r)
		}
		return true
	})
	c := newConv()
	for _, r := range qwenChain(recs) {
		if r.Message == nil {
			continue
		}
		parts := geminiParts(r.Message.Parts)
		switch {
		case r.Type == "user" && r.Subtype == "":
			if geminiImage(parts) {
				c.note("user", "Image attached", false, r.Timestamp)
			}
			c.message("user", geminiSaid(parts), r.Timestamp)
		case r.Type == "assistant":
			c.message("assistant", geminiSaid(parts), r.Timestamp)
			for _, p := range parts {
				if p.FunctionCall != nil {
					geminiCall(c, p.FunctionCall.ID, p.FunctionCall.Name, p.FunctionCall.Args, r.Timestamp)
				}
			}
		case r.Type == "tool_result":
			for _, p := range parts {
				if p.FunctionResponse == nil {
					continue
				}
				out, failed := geminiResponse(p.FunctionResponse.Response)
				if tr := r.ToolCallResult; tr != nil && (tr.Status == "error" || len(tr.Error) > 0 && string(tr.Error) != "null") {
					failed = true
				}
				geminiResult(c, p.FunctionResponse.ID, out, failed)
			}
		}
	}
	return c.done(), nil
}
