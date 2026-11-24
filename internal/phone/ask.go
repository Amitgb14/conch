package phone

import (
	"encoding/json"
	"slices"

	"github.com/Amitgb14/conch/internal/proto"
)

// Ask is GET /api/ask?pane=: the question an agent is waiting on, in full
// — every option and its description — as its transcript has it, since a
// menu longer than the screen cannot be read off the screen. Questions is
// never null; it is empty when the agent is asking nothing this way.
type Ask struct {
	Pane      string        `json:"pane"`
	Questions []AskQuestion `json:"questions"`
}

// AskQuestion is one of Claude Code's AskUserQuestion questions.
type AskQuestion struct {
	Question    string      `json:"question"`
	Header      string      `json:"header,omitempty"`
	MultiSelect bool        `json:"multi_select,omitempty"`
	Options     []AskOption `json:"options"`
}

// AskOption is one choice and what it means.
type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

func (g *Gateway) ask(rq *request) (any, *APIError) {
	ref := rq.r.URL.Query().Get("pane")
	if ref == "" {
		return nil, apiErr(CodeBadRequest, "which pane? pane is missing")
	}
	c, pane, id, aerr := g.paneOn(rq.dev, ref)
	if aerr != nil {
		return nil, aerr
	}
	var list proto.PaneList
	if aerr := g.callOn(rq, c, callTimeout, proto.MethodPaneList, nil, &list); aerr != nil {
		return nil, aerr
	}
	i := slices.IndexFunc(list.Panes, func(p proto.PaneInfo) bool { return p.ID == pane })
	if i < 0 {
		return nil, apiErr(CodeNotFound, "no pane "+id)
	}
	out := Ask{Pane: id, Questions: []AskQuestion{}}
	if list.Panes[i].Agent == nil || len(c.MissingCapabilities([]string{proto.CapAgentChat})) > 0 {
		return out, nil
	}
	var chat proto.AgentChat
	// A chat that cannot be read is a question the phone answers from the
	// screen, as it did before: not an error.
	if g.callOn(rq, c, callTimeout, proto.MethodAgentChat, proto.AgentChatParams{ID: pane}, &chat) == nil {
		out.Questions = pendingAsk(chat.Turns)
	}
	return out, nil
}

// pendingAsk is the AskUserQuestion the conversation ends on, still
// unanswered; nil when it ends on anything else, or its input was clipped.
func pendingAsk(turns []proto.ChatTurn) []AskQuestion {
	if len(turns) == 0 {
		return nil
	}
	t := turns[len(turns)-1]
	if t.Role != "tool" || t.Text != "AskUserQuestion" || t.Done {
		return nil
	}
	var in struct {
		Questions []struct {
			Question    string      `json:"question"`
			Header      string      `json:"header"`
			MultiSelect bool        `json:"multiSelect"`
			Options     []AskOption `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal([]byte(t.Input), &in) != nil {
		return nil
	}
	var out []AskQuestion
	for _, q := range in.Questions {
		a := AskQuestion{Question: q.Question, Header: q.Header, MultiSelect: q.MultiSelect, Options: q.Options}
		if a.Options == nil {
			a.Options = []AskOption{}
		}
		out = append(out, a)
	}
	return out
}
