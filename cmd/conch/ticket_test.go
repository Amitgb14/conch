package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

func TestA4TaskFromATicket(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	git := true
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodProjectAdd:
			return proto.ProjectInfo{ID: "proj1", Name: "api", Git: git}, nil
		case proto.MethodTaskCreate:
			return proto.PaneInfo{ID: "p5", Cwd: "/src/wt", Branch: "api/eng-7-export"}, nil
		case proto.MethodScheduleAdd:
			return proto.ScheduledItem{ID: "s1"}, nil
		}
		return nil, nil
	})
	t.Chdir(t.TempDir())
	var err error
	out, _ := a4Capture(t, "", func() { err = runTask([]string{"-ticket", "ENG-7"}) })
	if err != nil || out != "p5  /src/wt  api/eng-7-export\n" {
		t.Fatalf("task -ticket: %q %v", out, err)
	}
	var tp proto.TaskCreateParams
	srv.params(t, proto.MethodTaskCreate, &tp)
	if tp.Ticket != "ENG-7" || tp.Prompt != "" || tp.Branch != "" || tp.Issue != 0 {
		t.Fatalf("params %+v", tp)
	}
	// Words after it are added; several attempts number from its key.
	a4Capture(t, "", func() { err = runTask([]string{"-ticket", "linear:eng-7", "-n", "2", "keep", "it", "small"}) })
	srv.params(t, proto.MethodTaskCreate, &tp)
	if err != nil || tp.Prompt != "keep it small" || !strings.Contains(tp.Branch, "eng-7") || tp.Ticket != "linear:eng-7" {
		t.Fatalf("with words: %+v %v", tp, err)
	}
	// Queued: the ticket goes with the task the server keeps.
	a4Capture(t, "", func() { err = runTask([]string{"-queue", "-ticket", "ENG-8"}) })
	var it proto.ScheduledItem
	srv.params(t, proto.MethodScheduleAdd, &it)
	if err != nil || it.Task == nil || it.Task.Ticket != "ENG-8" {
		t.Fatalf("queued: %+v %v", it, err)
	}
	// Refused before anything is asked of the server.
	for _, args := range [][]string{{"-ticket", "seven"}, {"-ticket", "ENG-7", "-issue", "3"}, {"-ticket", "ENG-7", "-sandbox", "daytona"}} {
		if a4Capture(t, "", func() { err = runTask(args) }); err == nil {
			t.Fatalf("%v was taken", args)
		}
	}
	git = false
	if a4Capture(t, "", func() { err = runTask([]string{"-ticket", "ENG-7"}) }); err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("no git: %v", err)
	}
	git = true
	srv.setHello(func(int) proto.HelloResult {
		h := currentHello(0)
		h.Capabilities = slices.DeleteFunc(slices.Clone(h.Capabilities), func(c string) bool { return c == proto.CapTickets })
		return h
	})
	if a4Capture(t, "", func() { err = runTask([]string{"-ticket", "ENG-7"}) }); err == nil || !strings.Contains(err.Error(), "predates tasks from tickets") {
		t.Fatalf("old server: %v", err)
	}
}

func TestA4TicketCommand(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	inProject := true
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodProjectResolve:
			if !inProject {
				return nil, proto.Errorf(proto.ErrNotFound, "no project holds it")
			}
			return proto.ProjectPlace{ProjectID: "proj1", Root: "/src/api", Git: true}, nil
		case proto.MethodProjectTickets:
			return proto.TicketList{Tracker: "linear", Tickets: []proto.TicketInfo{
				{Tracker: "linear", Key: "ENG-7", Title: "Export as CSV", Status: "Todo", URL: "https://linear.app/a/issue/ENG-7"},
				{Tracker: "linear", Key: "ENG-12", Title: "Dark mode", Status: "In Progress"},
			}}, nil
		case proto.MethodTicketGet:
			return proto.TicketInfo{Tracker: "linear", Key: "ENG-7", Title: "Export as CSV", Status: "Todo", URL: "https://linear.app/a/issue/ENG-7",
				Body: "Users ask.", Comments: []proto.TicketComment{{Author: "Ana", Body: "Also XLSX?"}}, Links: []proto.TicketLink{{Title: "PR #3", URL: "https://gh/3"}}}, nil
		case proto.MethodTicketUpdate:
			return proto.TicketInfo{Tracker: "linear", Key: "ENG-7", Title: "Export as CSV", Status: "In Review"}, nil
		}
		return nil, nil
	})
	t.Chdir(t.TempDir())
	var err error
	out, _ := a4Capture(t, "", func() { err = runTicket([]string{"ls", "-mine", "-n", "5"}) })
	if err != nil || !strings.Contains(out, "ENG-7   Todo         Export as CSV\n") || !strings.Contains(out, "ENG-12  In Progress  Dark mode\n") {
		t.Fatalf("ls: %q %v", out, err)
	}
	var lp proto.TicketsParams
	srv.params(t, proto.MethodProjectTickets, &lp)
	if lp.ProjectID != "proj1" || !lp.Assigned || lp.Limit != 5 {
		t.Fatalf("ls params %+v", lp)
	}
	out, _ = a4Capture(t, "", func() { err = runTicket([]string{"ls", "-json"}) })
	if err != nil || !strings.Contains(out, `"key": "ENG-7"`) {
		t.Fatalf("ls -json: %q %v", out, err)
	}

	out, _ = a4Capture(t, "", func() { err = runTicket([]string{"show", "ENG-7"}) })
	for _, want := range []string{"ENG-7  Export as CSV\n", "Todo · https://linear.app/a/issue/ENG-7", "Users ask.", "Ana: Also XLSX?", "PR #3  https://gh/3"} {
		if err != nil || !strings.Contains(out, want) {
			t.Fatalf("show lacks %q: %q %v", want, out, err)
		}
	}

	out, _ = a4Capture(t, "", func() { err = runTicket([]string{"comment", "ENG-7", "PR", "opened:", "https://gh/3"}) })
	var up proto.TicketUpdateParams
	srv.params(t, proto.MethodTicketUpdate, &up)
	if err != nil || up.Comment != "PR opened: https://gh/3" || up.Ticket != "ENG-7" || up.ProjectID != "proj1" || !strings.Contains(out, "commented on ENG-7") {
		t.Fatalf("comment: %+v %q %v", up, out, err)
	}
	out, _ = a4Capture(t, "", func() { err = runTicket([]string{"status", "ENG-7", "In", "Review"}) })
	up = proto.TicketUpdateParams{}
	srv.params(t, proto.MethodTicketUpdate, &up)
	if err != nil || up.Status != "In Review" || up.Comment != "" || !strings.Contains(out, "ENG-7 is In Review") {
		t.Fatalf("status: %+v %q %v", up, out, err)
	}
	out, _ = a4Capture(t, "", func() { err = runTicket([]string{"link", "-title", "PR #3", "ENG-7", "https://gh/3"}) })
	up = proto.TicketUpdateParams{}
	srv.params(t, proto.MethodTicketUpdate, &up)
	if err != nil || up.LinkURL != "https://gh/3" || up.LinkTitle != "PR #3" || !strings.Contains(out, "linked https://gh/3 to ENG-7") {
		t.Fatalf("link: %+v %q %v", up, out, err)
	}

	// Outside any project: the ticket is still reached, with no project.
	inProject = false
	if a4Capture(t, "", func() { err = runTicket([]string{"show", "linear:ENG-7"}) }); err != nil {
		t.Fatal(err)
	}
	var gp proto.TicketGetParams
	srv.params(t, proto.MethodTicketGet, &gp)
	if gp.ProjectID != "" || gp.Ticket != "linear:ENG-7" {
		t.Fatalf("no project: %+v", gp)
	}

	// Usage mistakes, before anything is sent.
	for _, args := range [][]string{nil, {"what"}, {"show"}, {"show", "seven"}, {"comment", "ENG-7"}, {"status", "ENG-7"}, {"link", "ENG-7"}, {"link", "ENG-7", "not-an-address"}, {"ls", "-tracker", "github"}} {
		if a4Capture(t, "", func() { err = runTicket(args) }); err == nil {
			t.Errorf("%v was taken", args)
		}
	}
	// The server's refusal is said.
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodProjectResolve {
			return proto.ProjectPlace{ProjectID: "proj1", Git: true}, nil
		}
		return nil, proto.Errorf(proto.ErrBadRequest, "Linear refused the key")
	})
	if a4Capture(t, "", func() { err = runTicket([]string{"show", "ENG-7"}) }); err == nil || !strings.Contains(err.Error(), "refused the key") {
		t.Fatalf("refusal: %v", err)
	}
	// An older server.
	srv.setHello(func(int) proto.HelloResult {
		h := currentHello(0)
		h.Capabilities = slices.DeleteFunc(slices.Clone(h.Capabilities), func(c string) bool { return c == proto.CapTickets })
		return h
	})
	if a4Capture(t, "", func() { err = runTicket([]string{"ls"}) }); err == nil || !strings.Contains(err.Error(), "predates tickets") {
		t.Fatalf("old server: %v", err)
	}
}
