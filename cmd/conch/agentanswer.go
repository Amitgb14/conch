package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/phone"
	"github.com/Amitgb14/conch/internal/proto"
)

// `conch agent question ID` and `conch agent answer ID CHOICE` are what a
// waiting agent asks and the way to answer it without opening its pane:
// the Mac app's Allow / Deny buttons on an agent waiting for you. They
// read the menu off the pane's screen as the phone gateway does
// (internal/phone/question.go), so every way of answering an agent agrees
// on what the choices are — and on refusing an answer to a question that
// has since changed, which is what the question's id is for.

const (
	agentQuestionUsage = "usage: conch agent question ID"
	agentAnswerUsage   = "usage: conch agent answer [-question QID] ID CHOICE"
)

// agentQuestionNow is a pane's question as its screen shows it now, with
// that screen; nil when its agent is not waiting.
func agentQuestionNow(c *client.Client, id string) (*phone.Question, []string, error) {
	var list proto.PaneList
	if err := call(c, proto.MethodPaneList, nil, &list); err != nil {
		return nil, nil, err
	}
	var info *proto.PaneInfo
	for i := range list.Panes {
		if list.Panes[i].ID == id {
			info = &list.Panes[i]
		}
	}
	if info == nil {
		return nil, nil, fmt.Errorf("no pane %s", id)
	}
	var screen proto.PaneReadResult
	if err := call(c, proto.MethodPaneRead, proto.PaneRef{ID: id}, &screen); err != nil {
		return nil, nil, err
	}
	return phone.ReadQuestion(*info, screen.Lines), screen.Lines, nil
}

// agentQuestion prints the question as JSON, or null when the agent is
// not waiting — which is an answer, not an error, for something polling.
func agentQuestion(args []string) error {
	if len(args) != 1 {
		return errors.New(agentQuestionUsage)
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	id, err := resolvePane(c, args[0])
	if err != nil {
		return err
	}
	q, _, err := agentQuestionNow(c, id)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(q)
}

func agentAnswer(args []string) error {
	fs := flag.NewFlagSet("agent answer", flag.ContinueOnError)
	qid := fs.String("question", "", "answer only if this is still the question (its id from conch agent question)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New(agentAnswerUsage)
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	id, err := resolvePane(c, fs.Arg(0))
	if err != nil {
		return err
	}
	q, screen, err := agentQuestionNow(c, id)
	switch {
	case err != nil:
		return err
	case q == nil:
		return fmt.Errorf("the agent in %s is not waiting for an answer", id)
	case *qid != "" && q.ID != *qid:
		return fmt.Errorf("the question in %s has changed since it was read; read it again", id)
	}
	keys, ok := phone.ChoiceKeys(screen, fs.Arg(1))
	if !ok {
		return fmt.Errorf("%q is not one of the choices on %s's screen", fs.Arg(1), id)
	}
	return call(c, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: id, Keys: keys}, nil)
}
