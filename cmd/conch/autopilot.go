package main

import (
	"errors"
	"fmt"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/tui"
)

const autopilotUsage = "usage: conch [-m MACHINE] autopilot push"

// runAutopilot hands a server the autopilot policy from config.toml's
// [verify] — what the TUI does as it connects and whenever Settings
// change. A window that is not the TUI (Conch.app) runs it, or a server
// would act on whatever policy a TUI last gave it, and none at all where
// no TUI has ever run.
func runAutopilot(args []string) error {
	if len(args) != 1 || args[0] != "push" {
		return errors.New(autopilotUsage)
	}
	cfg, cerr := config.Load()
	if cerr != nil {
		return fmt.Errorf("settings not read, so nothing sent: %w", cerr)
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{proto.CapAutopilot})) > 0 {
		fmt.Println("this server has no autopilot (older than autopilot.v1); nothing to set")
		return nil
	}
	pol := tui.AutopilotPolicy(cfg.Verify)
	if err := call(c, proto.MethodAutopilotSet, pol, nil); err != nil {
		return err
	}
	fmt.Printf("autopilot: auto_fix %d, auto_merge %v, auto_pr %v, %d check(s)\n", pol.AutoFix, pol.AutoMerge, pol.AutoPR, len(pol.Checks))
	if len(pol.Triage) > 0 {
		if len(c.MissingCapabilities([]string{proto.CapAutopilotTriage})) > 0 {
			fmt.Println("triage: this server is older than autopilot.triage.v1 and triages nothing; reload it")
		} else {
			fmt.Printf("triage: issues labelled for %d project(s) are read by an agent and labelled\n", len(pol.Triage))
		}
	}
	if len(pol.Intake) > 0 {
		if len(c.MissingCapabilities([]string{proto.CapAutopilotIntake})) > 0 {
			fmt.Println("intake: this server is older than autopilot.intake.v1 and takes no issues; reload it")
		} else {
			fmt.Printf("intake: issues labelled for %d project(s) are taken as queued tasks\n", len(pol.Intake))
		}
	}
	return nil
}
