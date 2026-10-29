package main

import (
	"errors"
	"fmt"
	"strings"
	"syscall"

	"github.com/Amitgb14/conch/internal/proto"
)

// What an error says next. An error that stops at what went wrong leaves
// somebody new to find the way on by themselves; the ones people meet
// most — no server, no such pane, a word that is not a subcommand, a
// command given the wrong arguments — end with what to type instead, the
// way conch doctor's fixes do.

// errServerDown is a command that needs the server meeting none. The
// socket's dial error said the same in words nobody new can read:
// "dial unix …/conch.sock: connect: no such file or directory".
var errServerDown = errors.New("the conch server is not running; run conch to start it (agents keep running when conch closes)")

// serverDial turns a failed dial of this machine's socket into
// errServerDown when nothing is listening there, and leaves any other
// failure — a socket somebody else's, a server stuck mid-reload — as it is.
func serverDial(err error) error {
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		return errServerDown
	}
	return err
}

// downTo is errServerDown said for a command that would act on the server
// itself, where starting one to stop it would be no answer.
func downTo(what string, err error) error {
	if errors.Is(err, errServerDown) {
		return fmt.Errorf("the conch server is not running, so there is nothing to %s", what)
	}
	return err
}

// unknownSubcommand is a word that is none of a command's subcommands:
// the one it was likely meant to be, when one is close, and where they
// are all listed.
func unknownSubcommand(cmd, word string) error {
	msg := fmt.Sprintf("unknown %s subcommand %q", cmd, word)
	subs := append(compWords(compSubcommands[cmd]), compWords(compSubcommands[cmd+" -provider"])...)
	if near := nearestWord(word, subs); near != "" {
		msg += fmt.Sprintf(" — did you mean conch %s %s?", cmd, near)
	}
	return fmt.Errorf("%s; conch %s -h lists them", msg, cmd)
}

// notFound adds where the real ones are listed to an error about a pane
// or project that is not there.
func notFound(err error, list string) error {
	return fmt.Errorf("%w; %s lists them", err, list)
}

// errorText is how an error is put in front of somebody: a usage line
// gets where the whole of it is explained, and the server's generic codes
// — bad_request, internal — are dropped, since they say nothing a person
// can act on. The other codes stay: out_of_scope is what an agent is
// taught to read, and scripts may test for the rest.
func errorText(cmd string, err error) string {
	text := err.Error()
	var perr *proto.Error
	if errors.As(err, &perr) && text == perr.Error() &&
		(perr.Code == proto.ErrBadRequest || perr.Code == proto.ErrInternal) {
		text = perr.Message
	}
	if strings.HasPrefix(text, "usage: ") {
		if name := helpCommand(cmd); name != "" {
			text += "\n  conch " + name + " -h explains it"
		}
	}
	return text
}
