package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
)

// Conch Cloud is hidden in this release (config.CloudEnabled): its
// commands are left out of the usage, conch help all and completion, and
// run directly they say how to turn it on rather than "unknown command".
// Hidden is not broken, so what reaches a cloud machine already in
// machines.json — `conch cloud bridge` and `exec`, which remote's
// transport runs — still works, and so do the verbs on one somebody has
// (wake, sleep, rm, forget-key: Conch.app's row menu runs them), as does
// what runs inside one (`agent`, `install`, `git-credential`), whose
// image knows nothing of the switch. What is refused is what offers
// Cloud: signing in, the account, listing and making machines.

// cloudCommands are Conch Cloud's own commands. `account` is not one of
// them: its subcommands are an agent's accounts, which are no part of
// Cloud, and only `conch account [-json]` on its own is Cloud's
// (cloudAccount). Listing it here once refused every agent-account
// command with Cloud hidden, which was every release.
var cloudCommands = []string{"login", "logout", "cloud"}

// cloudAccountEntry is the usage entry for Cloud's own `conch account`,
// taken out of help while Cloud is hidden; the agent-account entries
// beside it stay.
const cloudAccountEntry = "  conch account [-json]"

// cloudAccount reports whether `conch account ARGS` is Conch Cloud's
// account rather than an agent's: anything but an agent-account
// subcommand, or nothing, which lists agent accounts while Cloud is
// hidden.
func cloudAccount(args []string) bool {
	if len(args) > 0 && agentAccountCommands[args[0]] {
		return false
	}
	return len(args) > 0 || cloudOn()
}

// cloudPlumbing are the `conch cloud` subcommands that run with Cloud off.
var cloudPlumbing = []string{"bridge", "exec", "agent", "install", "git-credential"}

// cloudOn says whether Conch Cloud is offered; a variable for tests.
var cloudOn = config.CloudOn

// cloudHidden reports whether cmd is a Cloud command that is hidden now.
func cloudHidden(cmd string) bool {
	return slices.Contains(cloudCommands, cmd) && !cloudOn()
}

// cloudNotEnabled is what a hidden Cloud command says when run.
func cloudNotEnabled(cmd string) error {
	return fmt.Errorf("conch %s: Conch Cloud is not enabled in this release; to try it, set %s=1 or put enabled = true under [cloud] in config.toml", cmd, config.CloudEnv)
}

// cloudMachineVerbs are the `conch cloud machine` verbs on a machine
// somebody already has, which run with Cloud off.
var cloudMachineVerbs = []string{"wake", "start", "sleep", "stop", "rm", "delete", "forget-key"}

// cloudGate is nil when the command may run: any command that is not
// Cloud's, Cloud's while it is enabled, and the plumbing either way.
func cloudGate(cmd string, args []string) error {
	if cmd == "account" {
		if cloudAccount(args) && !cloudOn() {
			return cloudNotEnabled(cmd)
		}
		return nil
	}
	if !slices.Contains(cloudCommands, cmd) {
		return nil
	}
	if cmd == "cloud" && len(args) > 0 && slices.Contains(cloudPlumbing, args[0]) {
		return nil
	}
	if cmd == "cloud" && len(args) > 1 && (args[0] == "machine" || args[0] == "machines") && slices.Contains(cloudMachineVerbs, args[1]) {
		return nil
	}
	if cloudOn() {
		return nil
	}
	return cloudNotEnabled(cmd)
}

// offeredCommands are the commands completion and suggestions offer:
// every one, less Cloud's while it is hidden.
func offeredCommands() []compItem {
	if cloudOn() {
		return compCommands
	}
	out := make([]compItem, 0, len(compCommands))
	for _, c := range compCommands {
		if !slices.Contains(cloudCommands, c.word) {
			out = append(out, c)
		}
	}
	return out
}

// fullUsage is conch help all: the usage, less Cloud's commands (each
// line naming one and the lines under it) while it is hidden.
func fullUsage() string {
	if cloudOn() {
		return usage
	}
	return withoutCloudAccount(withoutCommands(usage, cloudCommands))
}

// withoutCloudAccount is text less Cloud's own `conch account` entry,
// while Cloud is hidden; an agent's account entries stay.
func withoutCloudAccount(text string) string {
	if cloudOn() {
		return text
	}
	return withoutEntries(text, func(line string) bool { return strings.HasPrefix(line, cloudAccountEntry) })
}

// withoutCommands is text with the usage entries of cmds taken out, as
// usageOf finds them.
func withoutCommands(text string, cmds []string) string {
	return withoutEntries(text, func(line string) bool {
		m := helpWords.FindStringSubmatch(line)
		return m != nil && slices.ContainsFunc(strings.Split(m[1], "|"), func(c string) bool {
			return slices.Contains(cmds, c)
		})
	})
}

// withoutEntries is text less each usage entry whose first line drop
// picks, with the lines under it.
func withoutEntries(text string, drop func(line string) bool) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder
	for i := 0; i < len(lines); i++ {
		if drop(lines[i]) {
			for i+1 < len(lines) && strings.HasPrefix(lines[i+1], "                ") {
				i++
			}
			continue
		}
		b.WriteString(lines[i])
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
