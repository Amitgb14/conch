package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// Things for later: a message to an agent (conch agent prompt -at) or a
// task (conch task -at / -every), kept by the server so they happen with
// nothing connected. conch schedule lists them and takes one away.

const scheduleUsage = "usage: conch schedule [ls | rm ID]"

func runScheduleCmd(args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	c, err := connect(false)
	if err != nil {
		return errors.New("server is not running")
	}
	defer c.Close()
	if err := needSchedule(c); err != nil {
		return err
	}
	switch args[0] {
	case "ls", "list":
		if len(args) != 1 {
			return errors.New(scheduleUsage)
		}
		var list proto.ScheduleList
		if err := call(c, proto.MethodScheduleList, nil, &list); err != nil {
			return err
		}
		if len(list.Items) == 0 {
			fmt.Println("nothing scheduled")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tWHEN\tREPEATS\tWHAT")
		for _, it := range list.Items {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", it.ID, it.At.Local().Format("Mon 2 Jan 15:04"), everyText(it.EveryMinutes), scheduledWhat(it))
		}
		return tw.Flush()
	case "rm", "remove":
		if len(args) != 2 {
			return errors.New(scheduleUsage)
		}
		return call(c, proto.MethodScheduleRemove, proto.ScheduledItem{ID: args[1]}, nil)
	}
	return errors.New(scheduleUsage)
}

func needSchedule(c *client.Client) error {
	if len(c.MissingCapabilities([]string{proto.CapSchedule})) > 0 {
		return errors.New("the conch server there predates scheduling; reload it with `conch server reload`")
	}
	return nil
}

// scheduledWhat is an item in words, for a list.
func scheduledWhat(it proto.ScheduledItem) string {
	var s string
	switch {
	case it.After != "":
		s = fmt.Sprintf("task after %s: %s", afterText(it), oneLineText(it.Task.Prompt, 60))
	case it.Queue:
		s = fmt.Sprintf("queued task (%d at a time): %s", it.WIP, oneLineText(it.Task.Prompt, 60))
	case it.Task != nil:
		s = "task: " + oneLineText(it.Task.Prompt, 60)
	default:
		s = fmt.Sprintf("to %s (%s): %s", it.Pane, it.Agent, oneLineText(it.Text, 50))
	}
	if it.Note != "" {
		s += " — " + it.Note
	}
	if it.Waiting != "" {
		s += " [waiting: " + it.Waiting + "]"
	}
	if it.LastError != "" {
		s += " [last run failed: " + it.LastError + "]"
	}
	return s
}

// afterText is what a task waits for: the branch, once known, else the
// task on the schedule that will start it.
func afterText(it proto.ScheduledItem) string {
	if it.AfterBranch != "" {
		return it.AfterBranch
	}
	return it.After
}

func oneLineText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

func everyText(min int) string {
	switch {
	case min <= 0:
		return "-"
	case min%(7*24*60) == 0:
		return fmt.Sprintf("every %dw", min/(7*24*60))
	case min%(24*60) == 0:
		return fmt.Sprintf("every %dd", min/(24*60))
	case min%60 == 0:
		return fmt.Sprintf("every %dh", min/60)
	}
	return fmt.Sprintf("every %dm", min)
}

// scheduleOut says what was scheduled.
func scheduleOut(it proto.ScheduledItem) {
	if it.WIP == 0 {
		it.WIP = proto.DefaultWIP
	}
	if it.After != "" {
		also := ""
		if it.Queue {
			also = fmt.Sprintf(", and fewer than %d agents are running in the project", it.WIP)
		}
		from := "its branch"
		if it.Task.Base != "" {
			from = it.Task.Base
		}
		fmt.Printf("%s waits for %s to finish%s, then starts from %s (conch schedule rm %s takes it away)\n", it.ID, afterText(it), also, from, it.ID)
		return
	}
	if it.Queue {
		fmt.Printf("%s queued: it starts when fewer than %d agents are running in the project (conch schedule rm %s takes it away)\n", it.ID, it.WIP, it.ID)
		return
	}
	when := it.At.Local().Format("Mon 2 Jan 15:04")
	if it.EveryMinutes > 0 {
		when += ", then " + everyText(it.EveryMinutes)
	}
	fmt.Printf("%s scheduled for %s (conch schedule rm %s takes it away)\n", it.ID, when, it.ID)
}
