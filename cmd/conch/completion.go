package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// Shell completion. The scripts `conch completion SHELL` prints are a few
// lines each and know nothing about conch: on every tab they hand the words
// typed so far to `conch __complete SHELL WORD...` and show what comes back.
// So there is one list of commands, here, for three shells, and a pane
// started a second ago completes as well as a command does. Where a shell
// can show a description (zsh, fish) each candidate carries one — which is
// how somebody new finds out what `conch <tab>` can do.

const completionUsage = "usage: conch completion bash | zsh | fish"

type compItem struct{ word, desc string }

// compCommands are the commands offered after `conch`, in the order a
// newcomer would want them: the ones conch help starts with first.
var compCommands = []compItem{
	{"task", "give an agent a task, on a branch of its own"},
	{"new", "start a program or an agent in a pane"},
	{"status", "every pane and agent, and what it is doing"},
	{"read", "what a pane's screen shows"},
	{"send", "type into a pane"},
	{"wait", "wait until an agent is done"},
	{"close", "close a pane"},
	{"rename", "name a pane"},
	{"redraw", "draw a pane's screen again"},
	{"view", "what the terminal is showing"},
	{"show", "put panes in front of the person"},
	{"project", "the git repositories you work in"},
	{"branch", "commit, push, open a pull request, merge"},
	{"review", "comments on a branch, sent to its agent"},
	{"worktree", "list or clean up worktrees"},
	{"schedule", "what the server will do later"},
	{"agent", "install, set up and drive agents"},
	{"machine", "run agents on another machine, over ssh"},
	{"sandbox", "hosted sandboxes as machines"},
	{"upload", "copy files to a machine"},
	{"download", "copy files from a machine"},
	{"web", "answer your agents from a phone"},
	{"config", "edit every setting in the browser"},
	{"desktop", "the desktop window's page, in a browser"},
	{"ask", "ask the brain to plan actions"},
	{"server", "run, stop or reload the server"},
	{"update", "the newest release, without stopping agents"},
	{"doctor", "check what conch needs, and how to fix it"},
	{"bug", "the facts for a bug report"},
	{"mcp", "an MCP server for an agent"},
	{"skill", "print conch's skill for agents"},
	{"completion", "tab completion for bash, zsh or fish"},
	{"version", "print the version"},
	{"help", "the commands to start with"},
}

// compSubcommands are what follows a command that has its own.
var compSubcommands = map[string][]compItem{
	"config": {{"web", "every setting, as a form in the browser"}, {"path", "where config.toml is"}},
	"server": {{"stop", "stop the server and every pane it owns"}, {"reload", "run a new build, keeping panes"}},
	"agent": {
		{"status", "which agents are installed"}, {"install", "install an agent"},
		{"explain", "how a pane's agent state was decided"}, {"prompt", "send an agent a prompt"},
		{"setup", "what an agent loads in a checkout"}, {"sync", "give the others one agent's setup"},
		{"library", "servers and skills every agent follows"}, {"skill", "install conch's skill for agents"},
		{"add", "describe an agent of your own"},
	},
	"agent library": {
		{"list", "what is in the library"}, {"add", "add an MCP server"}, {"mcp", "add conch's own tools"},
		{"skill", "add a skill"}, {"on", "turn an entry on"}, {"off", "turn an entry off"}, {"rm", "remove an entry"},
		{"import", "take an agent's servers and skills"}, {"plan", "what apply would write"},
		{"apply", "write it into each agent"}, {"undo", "take the last apply back"},
	},
	"project": {
		{"add", "add a git repository"}, {"create", "make a new one"}, {"ls", "list projects"},
		{"rm", "forget a project"}, {"files", "local files copied into each worktree"},
		{"setup", "what runs in a new worktree"},
	},
	"branch": {
		{"commit", "commit on this branch"}, {"push", "push it"}, {"pr", "open a pull request"},
		{"merge", "merge it into its base"}, {"discard", "throw the branch away"},
	},
	"review":   {{"ls", "list comments"}, {"add", "comment on a file or line"}, {"rm", "remove a comment"}, {"send", "send them to the agent"}},
	"worktree": {{"ls", "list worktrees"}, {"clean", "remove finished ones"}},
	"schedule": {{"ls", "what is scheduled"}, {"rm", "cancel one"}},
	"machine": {
		{"add", "add a machine reached over ssh"}, {"ls", "list machines"}, {"rm", "remove one"},
		{"rename", "give one a new label"}, {"upgrade", "put this conch on it"}, {"hosts", "hosts from your ssh config"},
	},
	"sandbox": {{"-provider", "the provider to act on"}, {"stats", "every provider's sandboxes"}},
	"sandbox -provider": {
		{"create", "make a sandbox"}, {"add", "make an existing sandbox a machine"}, {"ls", "list sandboxes"},
		{"start", "start one"}, {"stop", "stop one"}, {"rm", "delete one"}, {"ssh", "a shell in one"},
		{"url", "a link to a port in one"}, {"usage", "what one has used"},
		{"snapshot", "save one as a snapshot"}, {"snapshots", "list snapshots"},
	},
	"web": {
		{"pair", "pair a phone"}, {"devices", "paired phones"}, {"revoke", "unpair one"},
		{"permission", "what a phone may do"}, {"stop", "stop a conch web in the background"},
	},
	"update":     {{"latest", "the newest release"}, {"list", "every published release"}, {"rollback", "the version before the last update"}},
	"completion": {{"bash", ""}, {"zsh", ""}, {"fish", ""}},
	"help":       {{"all", "every command"}},
}

// compPaneCommands take a pane as their first argument (show takes several).
var compPaneCommands = map[string]bool{
	"read": true, "send": true, "close": true, "rename": true, "redraw": true, "wait": true, "show": true,
	"agent explain": true, "agent prompt": true,
}

var compPermissions = []compItem{{"view", "watch only"}, {"reply", "answer agents"}, {"full", "everything"}}

// runComplete answers one tab: args are the shell, then the words after
// `conch` with the one being completed last. It never fails out loud — a
// completion that prints an error prints it into somebody's command line.
func runComplete(args []string) {
	if len(args) < 2 {
		return
	}
	shell, words := args[0], args[1:]
	for _, c := range compCandidates(words[:len(words)-1], words[len(words)-1]) {
		fmt.Println(compFormat(shell, c))
	}
}

// compCandidates is what may come next after done, starting with cur.
func compCandidates(done []string, cur string) []compItem {
	// -m MACHINE goes before the command, any number of times.
	remoteMachine := false
	for len(done) > 0 && (done[0] == "-m" || done[0] == "--machine") {
		if len(done) == 1 {
			return compFilter(compMachines(true), cur)
		}
		remoteMachine = remoteMachine || done[1] != "local"
		done = done[2:]
	}
	if len(done) == 0 {
		if strings.HasPrefix(cur, "-") {
			return compFilter([]compItem{{"-m", "run the command against another machine"}}, cur)
		}
		return compFilter(compCommands, cur)
	}

	last := done[len(done)-1]
	switch last { // a flag's value, whatever the command
	case "-agent":
		return compAgentList(cur)
	case "-permission":
		return compFilter(compPermissions, cur)
	case "-provider":
		if done[0] == "sandbox" {
			var out []compItem
			for _, p := range sandbox.Providers {
				out = append(out, compItem{p, ""})
			}
			return compFilter(out, cur)
		}
	case "-sandbox":
		var out []compItem
		for _, p := range sandbox.Providers {
			out = append(out, compItem{p, ""})
		}
		return compFilter(out, cur)
	}
	if strings.HasPrefix(cur, "-") {
		return nil // flags: the command's own -h lists them
	}

	cmd, rest := done[0], compPositional(done[1:])
	path := cmd
	if len(rest) > 0 {
		path = cmd + " " + rest[0]
	}
	if cmd == "sandbox" && slices.Contains(done, "-provider") {
		// sandbox -provider P SUBCOMMAND: the provider is not a subcommand.
		after := compPositional(done[slices.Index(done, "-provider")+2:])
		if len(after) == 0 {
			return compFilter(compSubcommands["sandbox -provider"], cur)
		}
		return nil
	}
	switch {
	case compPaneCommands[path] && len(rest) == 1, compPaneCommands[cmd] && (len(rest) == 0 || cmd == "show"):
		if remoteMachine {
			return nil // that machine's panes are an ssh away; not worth a tab's wait
		}
		return compFilter(compPanes(), cur)
	case len(rest) == 0:
		return compFilter(compSubcommands[cmd], cur)
	case path == "agent library" && len(rest) == 1:
		return compFilter(compSubcommands["agent library"], cur)
	case path == "machine add" && len(rest) == 1:
		var out []compItem
		for _, h := range remote.SSHHosts() {
			out = append(out, compItem{h, "from your ssh config"})
		}
		return compFilter(out, cur)
	case path == "agent install" && len(rest) == 1:
		return compAgentList(cur)
	case slices.Contains([]string{"machine rm", "machine rename", "machine upgrade"}, path) && len(rest) == 1:
		return compFilter(compMachines(false), cur)
	case slices.Contains([]string{"project rm", "project files", "project setup"}, path) && len(rest) == 1:
		if remoteMachine {
			return nil
		}
		return compFilter(compProjects(), cur)
	case path == "web permission" && len(rest) == 2:
		return compFilter(compPermissions, cur)
	}
	return nil
}

// compPositional drops flags and the values of the flags that take one, so
// `branch pr -title T` is still `branch pr`. Flags that take no value are
// few and known; the rest are assumed to take one.
func compPositional(words []string) []string {
	bare := map[string]bool{"-wait": true, "-queue": true, "-draft": true, "-rebase": true, "-force": true,
		"-y": true, "-no-git": true, "-reset": true, "-none": true, "-apply": true, "-remove": true, "-copy": true,
		"-user": true, "-keys": true, "-json": true, "-open": true, "-log": true, "-t": true, "-f": true, "-p": true,
		"-no-squash": true, "-undo": true}
	var out []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "--":
			return out
		case strings.HasPrefix(w, "-") && !strings.Contains(w, "="):
			if !bare[w] {
				i++ // its value
			}
		case strings.HasPrefix(w, "-"):
		default:
			out = append(out, w)
		}
	}
	return out
}

// compAgentList completes an agent name, or the last of a comma list
// (task -agent claude,co…).
func compAgentList(cur string) []compItem {
	head, tail := "", cur
	if i := strings.LastIndex(cur, ","); i >= 0 {
		head, tail = cur[:i+1], cur[i+1:]
	}
	var out []compItem
	for _, a := range compFilter(compAgents(), tail) {
		if !slices.Contains(strings.Split(head, ","), a.word) {
			out = append(out, compItem{head + a.word, a.desc})
		}
	}
	return out
}

// compAgents are the agents conch knows how to run: the ones it ships a
// manifest for, and any somebody described in their own.
func compAgents() []compItem {
	manifests, _ := detect.LoadManifests(detect.ManifestDir(config.Dir()))
	names := make([]string, 0, len(manifests))
	for name := range manifests {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]compItem, 0, len(names))
	for _, n := range names {
		out = append(out, compItem{n, ""})
	}
	return out
}

func compMachines(withLocal bool) []compItem {
	var out []compItem
	if withLocal {
		out = append(out, compItem{remote.LocalID, "this computer"})
	}
	ms, _ := remote.Machines()
	for _, m := range ms {
		out = append(out, compItem{m.ID, m.Target})
	}
	return out
}

// compFromServer asks the local server, never starting one, and gives up
// quickly: a tab that hangs is worse than a tab that offers nothing.
func compFromServer(method string, out any) bool {
	done := make(chan bool, 1)
	go func() {
		c, err := client.Dial(config.SocketPath(), "conch-complete")
		if err != nil {
			done <- false
			return
		}
		defer c.Close()
		done <- callFor(c, method, nil, out, time.Second) == nil
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(compWait):
		return false
	}
}

var compWait = 1500 * time.Millisecond

// compPanes are the running panes, by id, and by name where they have one.
func compPanes() []compItem {
	var list proto.PaneList
	if !compFromServer(proto.MethodPaneList, &list) {
		return nil
	}
	var out []compItem
	for _, p := range list.Panes {
		if p.State != proto.PaneRunning {
			continue
		}
		what := filepath.Base(p.Cwd)
		if p.Agent != nil && p.Agent.Name != "" {
			what = p.Agent.Name + " · " + what
			if p.Agent.State != "" {
				what += " · " + p.Agent.State
			}
		}
		if p.Name != "" {
			out = append(out, compItem{p.Name, p.ID + " · " + what})
			what = p.Name + " · " + what
		}
		out = append(out, compItem{p.ID, what})
	}
	return out
}

func compProjects() []compItem {
	var list proto.ProjectList
	if !compFromServer(proto.MethodProjectList, &list) {
		return nil
	}
	var out []compItem
	for _, p := range list.Projects {
		out = append(out, compItem{p.ID, p.Name})
	}
	return out
}

func compFilter(items []compItem, prefix string) []compItem {
	var out []compItem
	for _, it := range items {
		if strings.HasPrefix(it.word, prefix) {
			out = append(out, it)
		}
	}
	return out
}

// compFormat writes a candidate the way the shell's script reads it: bash
// takes the word alone, zsh's _describe "word:description" with the word's
// own colons escaped, fish "word<tab>description".
func compFormat(shell string, c compItem) string {
	desc := strings.Join(strings.Fields(c.desc), " ") // one line, whatever a pane is called
	switch shell {
	case "zsh":
		word := strings.ReplaceAll(c.word, ":", `\:`)
		if desc == "" {
			return word
		}
		return word + ":" + desc
	case "fish":
		if desc == "" {
			return c.word
		}
		return c.word + "\t" + desc
	}
	return c.word
}

func runCompletion(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%s\n\n%s", completionUsage, completionHowTo(filepath.Base(os.Getenv("SHELL"))))
	}
	switch args[0] {
	case "bash":
		_, _ = os.Stdout.WriteString(completionBash)
	case "zsh":
		_, _ = os.Stdout.WriteString(completionZsh)
	case "fish":
		_, _ = os.Stdout.WriteString(completionFish)
	default:
		return fmt.Errorf("conch completes bash, zsh and fish, not %q\n%s", args[0], completionUsage)
	}
	return nil
}

// completionHowTo says how to turn completion on, for the shell somebody
// uses first.
func completionHowTo(shell string) string {
	ways := []struct{ shell, how string }{
		{"zsh", "zsh:   add  eval \"$(conch completion zsh)\"  to ~/.zshrc, after compinit"},
		{"bash", "bash:  add  eval \"$(conch completion bash)\"  to ~/.bashrc"},
		{"fish", "fish:  conch completion fish > ~/.config/fish/completions/conch.fish"},
	}
	slices.SortStableFunc(ways, func(a, b struct{ shell, how string }) int {
		switch {
		case a.shell == shell:
			return -1
		case b.shell == shell:
			return 1
		}
		return 0
	})
	var lines []string
	for _, w := range ways {
		lines = append(lines, w.how)
	}
	return "To turn it on:\n  " + strings.Join(lines, "\n  ") + "\nHomebrew installs it for you."
}

// The scripts. Each passes the words after `conch`, the current one last
// (empty after a space), and falls back to file names when conch offers
// nothing, since most bare arguments left over are paths.

const completionBash = `# conch completion for bash: eval "$(conch completion bash)"
_conch() {
	# The words first: bash 3.2 (macOS's) joins a slice into one word
	# once IFS is changed.
	local words=("${COMP_WORDS[@]:1:COMP_CWORD}")
	local IFS=$'\n'
	COMPREPLY=($(conch __complete bash "${words[@]}" 2>/dev/null))
}
complete -o default -F _conch conch
`

const completionZsh = `#compdef conch
# conch completion for zsh: eval "$(conch completion zsh)" after compinit,
# or save it as _conch somewhere on $fpath.
_conch() {
	local -a c
	c=("${(@f)$(conch __complete zsh "${(@)words[2,CURRENT]}" 2>/dev/null)}")
	c=(${c:#})
	if (( ${#c} )); then
		_describe -t conch conch c
	else
		_files
	fi
}
if [[ $funcstack[1] == _conch ]]; then
	_conch "$@"
else
	compdef _conch conch
fi
`

const completionFish = `# conch completion for fish: conch completion fish > ~/.config/fish/completions/conch.fish
function __conch_complete
	set -l words (commandline -opc)
	set -e words[1]
	set -l c (conch __complete fish $words (commandline -ct) 2>/dev/null)
	if test (count $c) -gt 0
		printf '%s\n' $c
	else
		__fish_complete_path (commandline -ct)
	end
end
complete -c conch -f -a '(__conch_complete)'
`
