// Command conch is a terminal orchestrator for AI coding agents.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/doctor"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
	"github.com/Amitgb14/conch/internal/tui"
	"github.com/Amitgb14/conch/internal/update"
)

// shortUsage is what `conch help` prints: the commands somebody starting
// out needs, not all of them. The whole list was over a hundred lines,
// which answered "what can it do?" and not "what do I type?".
const shortUsage = `conch — run AI coding agents side by side, on your own machines

Start:
  conch                       open conch; agents keep running when you close it
  conch project add PATH      add a git repository you work in
  conch task PROMPT           give an agent a task, on a branch of its own
                              -agent codex picks the agent; -n 3 makes 3 tries
  conch new -agent claude     start an agent in this folder

Keep an eye on them:
  conch status                every pane and agent, and what it is doing
  conch read ID               what an agent's screen shows
  conch send ID TEXT          type into it
  conch wait ID               wait until it is done

More:
  conch machine add HOST      run agents on another machine, over ssh
  conch web                   answer your agents from a phone
  conch config web            every setting, as a form in the browser
  conch update                the newest release, without stopping agents
  conch doctor                check what conch needs, and say how to fix it
  conch completion zsh        tab completion, for zsh, bash or fish
  conch bug                   report a problem

conch help COMMAND (or conch COMMAND -h) says how one goes; conch help all lists
every command; in conch, ? lists every key.
Docs: https://amitgb14.github.io/conch/docs/
`

const usage = `conch — terminal orchestrator for AI coding agents

Usage:
  conch                         open the TUI (starts the server if needed)
  conch server                  run the server in the foreground
  conch server stop             stop the server and every pane it owns
  conch server reload           run a new conch build without stopping panes
  conch status [-wide] [-json] show server and pane status, with the host a terminal is on with ssh;
                                fitted to the terminal (-wide: every column, as piped); BACKGROUND
                                is what an idle agent left running (-json: agent.background, agent.jobs)
  conch new [-cwd DIR] [-name N] -- CMD [ARGS...]
                                start a pane (with -m, default: that machine's home)
  conch send ID TEXT            type TEXT into a pane (-keys to send key names); from an agent's
                                pane, only into panes running an agent, never a plain shell
                                ([agents] unrestricted_launch lifts it)
  conch read ID                 print a pane's visible screen
  conch attach [-hold] [-raw] [-view] ID
                                show a pane in this terminal and type into it (the Mac app's panes);
                                -hold keeps an ended pane's screen until a key; -raw gives this
                                terminal the program's own output to draw, not conch's screen;
                                -view (with -raw) never resizes the pane or takes its size
  conch close ID                close a pane
  conch rename ID [NAME]        name a pane; without NAME it gets its own back. Every
                                command taking a pane ID takes its name too
  conch redraw ID               draw a pane's screen again (after a program left stale text)
  conch wait ID [-state done] [-timeout 30m]
                                block until a pane's agent is done (or waiting, working, idle);
                                exits 124 if the time runs out
  conch new -agent claude [-account NAME] [-cwd DIR] [-- CLAUDE ARGS...]
                                start Claude Code with state tracking. From an agent's pane, only
                                -- --model NAME, and no CMD ([agents] unrestricted_launch lifts it);
                                -account starts it on another of its accounts (default: the
                                folder's pin, else its own; "default" is its own)
  conch account list | add AGENT NAME [-dir DIR] | remove AGENT NAME
                                an agent's other accounts (Claude Code, Codex): a work and a
                                personal subscription, each a folder of its own to sign in to
                                once; list shows each one's plan usage and agents
  conch account pin [PATH] NAME | unpin [PATH]
                                agents started in a project, worktree or task folder use that
                                account (the longest pin wins; NAME is any agent's of that name)
  conch account failover PERCENT|off
                                a new agent whose account is past PERCENT of a plan window
                                starts on the agent's next account; running agents never move
  conch ssh TARGET [SSH OPTION...]
                                a terminal here logged in to a host with ssh, outside every
                                project, as the TUI's H opens one; prints its pane ID
  conch ssh -copy-key TARGET [SSH OPTION...]
                                a terminal that puts this computer's key on the host, asking
                                for its password there once, so later logins need none
  conch ssh -check-key TARGET [SSH OPTION...]
                                whether the host takes the key: prints key, password or
                                unreachable (a batch-mode login, which never asks)
  conch agent explain ID        show how a pane's agent state was decided
  conch agent question ID       what a waiting agent asks, and its choices, as JSON (null when not waiting)
  conch agent answer [-question QID] ID CHOICE
                                pick a choice of a waiting agent's menu without opening its pane
  conch agent prompt [-wait] [-until done,idle,waiting] [-timeout 30m] [-at TIME] ID TEXT
                                send an agent its next message, refused (exit 3) while it
                                waits on a question; -wait blocks until that work ends;
                                -at sends it later (14:05, +30m), once the agent is free
  conch view                    what the terminal is showing: its tabs, and which pane has the keyboard
  conch show [-name N] ID...    ask the terminal to put those panes in front of the person
  conch skill [-list | NAME]    print conch's skill (or conch-team, conch-review, conch-ship,
                                conch-factory, conch-autopilot) instead, for an agent conch cannot
                                install it for
  conch agent skill [-skill NAMES] [-agent NAMES] [-remove] [-apply]
                                teach the agents conch: its skills, in each agent's own skills
                                folder — conch (start, prompt and read other agents),
                                conch-team (several at once), conch-review (review, fix,
                                review again), conch-ship (tests, pull request, CI, merge),
                                conch-factory (a ticket all the way to a merged PR) and
                                conch-autopilot (set up the automated workflow for a project)
  conch agent add NAME -command BINARY [-label L] [-prompt 'FLAG {prompt}'] [-resume 'FLAG {id}']
                                describe an agent conch has no adapter for, so it starts and is
                                watched like the others; what you leave out it will not pretend
                                to do (its sessions are not read either)
  conch agent status | install claude
                                check or install Claude Code (use -m for a machine)
  conch agent setup [-agent NAME] [-copy] [DIR]
                                what each agent loads there: instructions, skills, MCP servers
  conch agent sync [-user] [-from NAME] [-to NAMES] [-apply] [-undo] [DIR]
                                give the other agents one agent's setup — a checkout's, or
                                with -user your own (~/.claude and the rest); without
                                -apply it only says what it would do
  conch agent library [list | add | mcp | skill | on | off | rm | import AGENT | plan | apply | undo]
                                MCP servers and skills kept in conch, each given to the
                                agents you choose: add NAME (URL | -- COMMAND ARGS),
                                skill [NAME] FOLDER | owner/repo[/path][@ref] (from GitHub),
                                -to NAMES, -env K=${VAR}; mcp adds conch's own tools
                                (conch mcp); plan says what apply writes
  conch project add PATH | create [-no-git] PATH | clone URL [DIR] | ls | rm ID | files ID [-reset | -none | PATTERN...]
                | git-init ID|PATH
                                make a plain-folder project a git repository: git init and an
                                empty first commit (its files stay uncommitted), so a task can
                                make a branch in it
                | move [-plan] [-into DIR] [-y] [-json] ID -to MACHINE
                                move a whole project to another machine: the repository (cloned
                                there from its origin), every worktree with its uncommitted work,
                                every branch with commits its origin lacks, and the agents with
                                their conversations; -plan says what would move
                | setup ID [-none | COMMAND]
                                manage projects shown in the sidebar; setup is run in a
                                terminal of its own in each new worktree (npm install…),
                                and every pane there gets its own ports from $CONCH_PORT
  conch group [ls [-json]] | add NAME PROJECT... | rm NAME [PROJECT...] | rename OLD NEW | notes NAME [TEXT]
                | task [-branch B] [-agent A] [-account NAME] [-each] [-only P,P] NAME PROMPT... | tasks NAME | task-rm NAME BRANCH
                | merge [-order P,P] [-remove] [-stop] NAME BRANCH
                                projects kept together because they change together (an API, its
                                web app, their specs), each still a project of its own
  conch task [-cwd DIR] [-branch B] [-base B] [-agent A,B] [-n N] [-name N] [-at TIME] [-every SPAN] [-queue [-wip N]] [-after REF] [-issue N | -ticket KEY] [-account NAME] [-sandbox PROVIDER] PROMPT
                                new branch + worktree + Claude with PROMPT; in a folder that isn't a
                                git repository the agent works there instead (with -m and no -cwd:
                                that machine's home);
                                -n / several agents try it once each, one branch per attempt;
                                -name names the pane; -at starts it later (14:05, +2h), -every
                                again and again (1d, weekly), each run on a branch of its own;
                                -queue holds it until fewer than -wip (3) agents run in the project;
                                -after REF waits for that branch, pane or scheduled task to finish
                                and starts from its branch (stacked work; -base to start elsewhere);
                                -issue N works on that GitHub issue (PROMPT is then optional);
                                -ticket KEY on that Linear or Jira ticket (ENG-123, linear:ENG-123,
                                or its address), set up in [trackers] in config.toml;
                                -account NAME starts the agents on that account of theirs;
                                -sandbox P runs it in a new sandbox, the project cloned from origin
  conch schedule [ls | rm ID]   what the server will do later (prompt -at, task -at/-every)
  conch ticket ls [-mine] | show TICKET | comment TICKET TEXT | status TICKET STATUS | link TICKET URL
                                tickets in Linear or Jira ([trackers] in config.toml): list the
                                project's open ones, read one, comment, move it, link a pull request
  conch branch commit [-file PATH]... -m MESSAGE
                                commit a task branch's changes (-cwd, -branch pick it;
                                -cwd needed with -m)
  conch branch push [-rebase] | pr [-title T] [-draft] | merge [-no-squash] | discard [-force]
                                finish a branch: push, open a pull request, merge into
                                the base, or delete it and its worktree
  conch branch fix-ci [-agent A] send a branch's failed CI run to its agent (or start one
                                in its worktree with it), as f in the review queue
  conch review [ls | add | rm | send | reply] [-cwd DIR] [-branch B] ...
                                comments on a branch's diff for its agent: list them, add
                                FILE[:LINE] NOTE, rm ID, send [-agent A] them to it, or
                                reply ID ANSWER to one you were sent
                                (the same ones as a and S in the changes view)
  conch worktree ls | clean [-y] [-force] [PATH...]
                                list leftover worktrees, or remove the finished ones
  conch machine add [-label L] [-o Setting=value]... SSH_TARGET
                                add a remote machine (installs conch there); -o is an ssh
                                setting it is reached with (-o ProxyJump=bastion)
  conch machine ls | rm ID | rename ID LABEL | upgrade ID | hosts
  conch machine bridge ID       conch's protocol to that machine's server, on stdin and stdout
                                (how the Mac app reaches it); installs nothing
  conch sandbox -provider daytona|boat|devin create [-label L] [-snapshot S] [-cpu N] [-memory GiB] [-disk GiB] [-env NAME]...
                                make a sandbox (Daytona: $DAYTONA_API_KEY, boat.dev: $BOAT_API_KEY,
                                Devin Cloud: $DEVIN_API_KEY and a signed-in devin CLI)
                                and add it as a machine; it runs until stopped, so agents keep
                                going with the TUI closed. Every sandbox command but stats needs
                                -provider: there is no default, and a sandbox named under another
                                provider is refused
  conch sandbox -provider P add [-label L] ID
                                make a sandbox ls shows with no machine one of conch's machines:
                                one whose machine was removed, or on boat.dev one made in its own
                                interface. Started if stopped and conch installed; never deleted
                                if that fails
  conch sandbox providers [-json]  which providers conch can make a sandbox with, and whether each has its key
  conch sandbox adapt [-agent A] [-name N] [-new] [-json] URL...
                                a provider conch doesn't know: conch downloads its API docs, an agent
                                (claude by default) writes a provider file from them in a pane of its
                                own, and conch checks the file; the key is tried against the API only
                                once you agree to where it goes. Nothing is saved yet.
                                One already in the provider registry is offered instead (-new adapts anyway)
  conch sandbox adapt -revise TEXT NAME   ask that agent to change the draft, and check it again
  conch sandbox provider check [-offline] [-json] NAME|FILE | show NAME | save NAME | rm NAME
                                check a provider file, print it, make a draft a provider, or forget one
                                (its sandboxes are left alone)
  conch sandbox provider search [-json] [TERM]
                                the providers others adapted and published (github.com/Amitgb14/conch-sandbox-providers)
  conch sandbox provider install [-y] [NAME]
                                install one: says where your key and ssh go first, and checks the file's hash
  conch sandbox provider update [-y] [NAME]
                                take newer versions of installed ones; a file edited here is left alone,
                                and new hosts are only taken when you say yes
  conch sandbox provider publish [-dry-run] [-json] NAME
                                check a provider of yours with your key, then propose it to the registry
                                as a pull request through gh (your key is never part of it)
  conch sandbox stats           every provider's sandboxes, their state and size, and a line
                                per provider (-provider P narrows it to one)
  conch sandbox -provider P ls | start ID | stop [-y] ID | rm [-y] ID
                                list conch's sandboxes, start or stop one, or delete it for good
  conch sandbox -provider P ssh [-t] ID [COMMAND...]
                                a shell in a running sandbox, or COMMAND run there
                                (-t gives it a terminal); exits with its status
  conch sandbox -provider P url [-expires 1h] [-open] ID PORT
                                a link to a port inside a sandbox
  conch sandbox -provider P usage [-since 720h] ID
                                what a sandbox has cost, period by period
  conch sandbox -provider P snapshot [-name N] ID | snapshots [-rm NAME]
                                keep a sandbox to make others from, or list what is kept
  conch -m MACHINE COMMAND      run a command against a remote machine
  conch -m MACHINE upload [-to DIR [-p]] [-f] PATH...
                                copy files or folders to a machine — into DIR there (~ is its
                                home; -p makes it if missing), else its uploads folder — and
                                print where each landed; a name taken is numbered unless -f
  conch -m MACHINE download [-to DIR] [-f] PATH...
                                copy files or folders from a machine into DIR here (default: the
                                current folder) and print where each landed; -f as for upload
  conch upload|download -ssh PANE ...
                                the same with the host the terminal PANE (ID or name) is logged
                                in to with ssh; upload's DIR defaults to that host's home
  conch web [-listen ADDR] [-port N] [-url URL] [-cert FILE -key FILE]
                                serve your phone: see the agents, reply to one and answer what
                                it asks. Listens only on this machine's Tailscale address
                                unless -listen names another; -url is the https address
                                phones open (the one tailscale serve gives)
  conch web -relay wss://HOST  (or [web] relay in config.toml)
                                also reach the phone from any network, through a relay:
                                conch web connects out to it, and phone and computer
                                encrypt everything with the key the pairing QR code carries
  conch web relay [reset]       how the link to the relay is, and the words its key is
                                said in; reset makes a new key and revokes the phones
                                that paired through the relay
  conch relay [-listen ADDR] [-cert FILE -key FILE]
                                run a relay on a server of your own (:8443 by default), for
                                phones and computers to meet at; it forwards what it can't
                                read and stores nothing, but serves the phone's app, so
                                use only a relay you trust as you trust the computer
  conch login [-no-browser] [-json] [-api URL]
                                sign this device in to Conch Cloud: prints a code, opens
                                the page to approve it, and waits. The server is
                                CONCH_CLOUD_URL, else [cloud] api in config.toml, else
                                Conch Cloud; -json says each step as a line of JSON
  conch logout                  sign this device out of Conch Cloud (revokes its sign-in)
  conch account [-json]         who is signed in, the plan, this month's hours and
                                storage, and the account's devices
  conch cloud machine ls [-json] | new NAME [-size S] [-region R] [-provider ID] [-wait]
                                the account's cloud machines, or make one
  conch cloud machine wake|sleep NAME [-force] | rm NAME [-y] | forget-key NAME
                                wake one or put it to sleep (-force while agents work),
                                destroy one, or forget the key pinned for it so the next
                                connection pins the one Conch Cloud gives
  conch cloud exec [-t] NAME [-- SCRIPT…]
                                run a script on a cloud machine (a login shell with no
                                script), through Conch Cloud's relay end to end; it wakes
                                the machine and waits for it. Exits with the script's
                                status, 255 when the tunnel failed, 254 when it never will
                                (signed out, a machine key that changed)
  conch cloud bridge NAME       conch's protocol to a cloud machine's server on stdin and
                                stdout: what conch machine bridge runs for one
  conch cloud agent [-boot FILE] [-state DIR] [-user NAME] [-socket PATH] [-server-socket PATH]
                                runs inside a Conch Cloud machine, started by its host (the
                                conch-cloud-agent systemd unit): reads boot.json (root-only,
                                0600), says hello to the control plane, starts this machine's
                                conch server as -user (conch when root), reports what its
                                agents do so the machine sleeps when idle, and joins the relay
                                so clients reach the server through an end-to-end tunnel.
                                Exits 3 when the control plane refuses its token
  conch cloud install [-user NAME]
                                ready a machine image for Conch Cloud (as root): the
                                conch-cloud-agent systemd unit, enabled, and its state directory
  conch cloud git-credential [-socket PATH] get
                                git's credential helper on a Conch Cloud machine: the machine
                                token for the git proxy, asked of the cloud agent
  conch web pair [-permission view|reply|full] [-machine NAME]... [-json]
                                a one-time code (5 minutes) that pairs a phone, with a QR code
                                that opens it; full by default (-permission reply or
                                view for a phone that may only answer, or only look). It
                                reaches this computer only unless -machine names a machine;
                                -json says it all as JSON, for a window to draw (Conch.app)
  conch web devices | revoke ID | rename ID NAME | permission ID view|reply|full [-machine NAME] | start | stop
                                list the paired phones, cut one off at once, or change
                                what one may do (full types into terminals) and which
                                machines it reaches (-machine, repeatable; this computer
                                only until you name one, 'local' to take them away);
                                start runs conch web in the background as it was last
                                started (or at the settings' address), stop ends it
  conch config web [-port N] [-no-open]
                                every setting in config.toml as a form in the browser, on
                                127.0.0.1 until ctrl+c; a TUI that is open edits them live
                                instead (Settings, then e)
  conch config path             print where config.toml is
  conch mcp                     an MCP server on stdin and stdout, for an agent that would rather
                                call a tool than a command: start, prompt, wait, read, task, list
                                and rename, with the scope of the pane it is started in
  conch completion bash|zsh|fish
                                print a script that completes commands, panes, machines and agents
                                on tab; without a shell, says how to turn it on
  conch doctor [-json]          check config, server, agents, git, gh, terminal and machines,
                                each with the command that fixes it; exits 1 if any must be fixed
  conch keys [-bound] [-toml] [-json]
                                what every key after the prefix does, and which key it is on now
                                (tmux's list-keys); rebind one under [keys.bind] in config.toml,
                                which -toml prints ready to edit
  conch app [-terminal NAME] [-n]
                                open conch in a terminal of its own, configured by conch and not by
                                whoever set that terminal up (-n writes that configuration and
                                prints the command without starting it); ls says which terminals are
                                here, install adds it to the desktop's applications with its icon,
                                remove takes it out
  conch app update check [-json] [-version V] | install [-app PATH] VERSION | rollback [-app PATH]
                                Conch.app's own update, which the app runs: check asks GitHub for a
                                newer release (a build from source never asks), install downloads it,
                                checks its checksum, signature, team and notarization and puts it in
                                place of the app, keeping the one before for rollback
  conch [-m MACHINE] autopilot push
                                hand the server the autopilot policy config.toml's [verify]
                                sets, as the TUI does when it connects; for a window that is
                                not the TUI (Conch.app runs it)
  conch bug [-open] [-log] [WHAT HAPPENED...]
                                the facts about this conch for a bug report — versions, counts
                                and the last error, never a path or a screen; -open fills in a
                                GitHub issue with them, which you read before you submit it
  conch ask [-y | -n] [-model] [-pane ID] REQUEST
                                ask the brain; it proposes actions and runs them once you confirm
                                (show, close, tell one agent or everyone, what is waiting: planned
                                at once, without the model; -model asks it anyway); -pane is the
                                pane "this" and "here" mean
  conch ask -suggest [-pane ID] [EARLIER REQUEST...]
                                what is worth asking now, as JSON, with what each costs; asks no
                                model (Conch.app's ✦ Ask draws it)
  conch commit-message [-context TEXT]
                                write a commit message for the diff on stdin with the brain; prints it
  conch update [VERSION | latest | list | rollback]
                                replace this binary with the latest release, a named one
                                (an older version moves conch back), list what upstream
                                publishes, or return to the version before the last update
  conch version [--json]        print version
`

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__complete" {
		runComplete(os.Args[2:]) // every tab: before hashing the executable, which costs more than the answer
		return
	}
	buildinfo.Build() // hash the executable now, before a rebuild can replace it
	buildinfo.ResolveVersion()
	log.SetFlags(log.LstdFlags)
	args := os.Args[1:]
	for len(args) >= 2 && (args[0] == "-m" || args[0] == "--machine") {
		machineFlag, args = args[1], args[2:]
	}
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	// A pane named machine:pN, as conch's windows show and copy one, is
	// that machine's pane.
	if a, m, err := machinePane(cmd, args, machineFlag); err != nil {
		fmt.Fprintln(os.Stderr, "conch:", err)
		os.Exit(2)
	} else {
		args, machineFlag = a, m
	}
	// conch CMD -h is answered here, before CMD can take -h for something
	// else or start a server first (help.go).
	if len(args) > 0 && isHelpFlag(args[0]) && helpCommand(cmd) != "" {
		fmt.Print(commandHelp(helpCommand(cmd)))
		return
	}
	// Conch Cloud's commands, while it is hidden (cloudswitch.go).
	if err := cloudGate(cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var err error
	switch cmd {
	case "":
		err = runTUI()
	case "server":
		err = runServer(args)
	case "status", "ls":
		err = runStatus(args)
	case "new":
		err = runNew(args)
	case "ssh":
		err = runSSHLogin(args)
	case "send":
		err = runSend(args)
	case "read":
		err = runRead(args)
	case "attach":
		err = runAttach(args)
	case "commit-message":
		err = runCommitMessage(args)
	case "rename":
		err = runRename(args)
	case "close":
		err = runClose(args)
	case "redraw":
		err = runRedraw(args)
	case "wait":
		err = runWait(args)
	case "agent":
		err = runAgent(args)
	case "group", "groups":
		err = runGroup(args)
	case "project":
		err = runProject(args)
	case "task":
		err = runTask(args)
	case "schedule":
		err = runScheduleCmd(args)
	case "ticket", "tickets":
		err = runTicket(args)
	case "branch":
		err = runBranch(args)
	case "review":
		err = runReview(args)
	case "worktree", "worktrees":
		err = runWorktree(args)
	case "report":
		runReport(args) // called by agent hooks; must never fail the agent
	case "machine", "machines":
		err = runMachine(args)
	case "sandbox", "sandboxes":
		err = runSandbox(args)
	case "bridge":
		err = runBridge()
	case "update":
		err = runUpdate(args)
	case "upload":
		err = runUpload(args)
	case "download":
		err = runDownload(args)
	case "ask":
		err = runAsk(args)
	case "web":
		err = runWeb(args)
	case "relay":
		err = runRelay(args)
	case "cloud":
		err = runCloud(args)
	case "login":
		err = runLogin(args)
	case "logout":
		err = runLogout(args)
	case "account":
		err = runAccount(args)
	case "config":
		err = runConfig(args)
	case "mcp":
		err = runMCP(args)
	case "bug":
		err = runBug(args)
	case "autopilot":
		err = runAutopilot(args)
	case "app":
		err = runApp(args)
	case "doctor":
		err = runDoctor(args)
	case "keys":
		err = cmdKeys(args)
	case "completion":
		err = runCompletion(args)
	case "view":
		err = runView(args)
	case "show":
		err = runShow(args)
	case "skill", "--skill":
		// The text itself, for an agent conch cannot install it for: one
		// whose skills folder conch does not manage, a system prompt
		// somebody assembles by hand, a sandbox where it goes inline.
		// `conch agent skill -apply` is the way to install it.
		err = printSkill(args)
	case "version", "--version", "-v":
		runVersion(args)
	case "help", "--help", "-h":
		switch {
		case len(args) > 0 && args[0] == "all":
			fmt.Print(fullUsage())
		case len(args) > 0 && helpCommand(args[0]) != "":
			fmt.Print(commandHelp(helpCommand(args[0])))
		case len(args) > 0:
			fmt.Fprint(os.Stderr, unknownCommand(args[0]))
			os.Exit(2)
		default:
			fmt.Print(shortUsage)
		}
	default:
		fmt.Fprint(os.Stderr, unknownCommand(cmd))
		os.Exit(2)
	}
	if errors.Is(err, flag.ErrHelp) {
		return // a subcommand's own -h: the flag package has printed its flags
	}
	var late waitTimeout
	if errors.As(err, &late) {
		fmt.Fprintln(os.Stderr, "conch:", err)
		os.Exit(124) // as timeout(1) does, so a script can tell it apart
	}
	var status exitStatus
	if errors.As(err, &status) {
		os.Exit(int(status)) // a command run elsewhere; it has said why
	}
	var blocked agentBlocked
	if errors.As(err, &blocked) {
		fmt.Fprintln(os.Stderr, "conch:", err)
		os.Exit(3) // the question is someone's to answer; retrying won't
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "conch:", errorText(cmd, err))
		os.Exit(1)
	}
}

// nestedPane is the pane this process runs in when it would connect to the
// server that owns it: a pane inherits both CONCH_PANE_ID and the socket of
// its server. Drawing a TUI inside one of its own panes is confusing — keys
// and the mouse go to the inner one, and closing the pane kills it — so
// conch refuses, like tmux. Clearing either variable (a separate server, or
// CONCH_PANE_ID= to force) opens one anyway.
func nestedPane() string {
	id, sock := os.Getenv("CONCH_PANE_ID"), os.Getenv("CONCH_SOCKET")
	if id == "" || sock == "" || machineFlag != "" && machineFlag != "local" {
		return "" // not in a pane, or aimed at another machine's server
	}
	return id
}

func runTUI() error {
	if id := nestedPane(); id != "" {
		return fmt.Errorf("this is already conch, in pane %s: detach first with %s d, "+
			"or run `CONCH_PANE_ID= conch` to open one inside this pane anyway", id, tuiPrefix())
	}
	cfg, cfgWarning := configForTUI()
	c, err := connect(true)
	if err != nil {
		return err
	}
	if c, err = offerUpgrade(c); err != nil {
		return err
	}
	defer c.Close()

	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if cfg.UI.Mouse {
		// All-motion reports every cell the pointer crosses, which is what
		// lighting the row under it needs; without hover, only drags are
		// reported, which is one message per drag instead of per cell.
		if cfg.UI.Hover {
			opts = append(opts, tea.WithMouseAllMotion())
		} else {
			opts = append(opts, tea.WithMouseCellMotion())
		}
	}
	if term.IsTerminal(os.Stdin.Fd()) {
		// Otherwise Bubble Tea opens /dev/tty itself, as it should.
		opts = append(opts, tea.WithInput(tui.WholeSequences(os.Stdin)))
	}
	wd, _ := os.Getwd()
	p := tea.NewProgram(tui.New(c, cfg).Warn(cfgWarning).LaunchedIn(wd).Doctor(func() []doctor.Check { return doctorChecks(false) }), opts...)
	final, err := p.Run()
	if err == nil && tui.RestartRequested(final) {
		// An update replaced this binary: run the new one in its place.
		c.Close()
		exe, xerr := update.Executable()
		if xerr != nil {
			return xerr
		}
		os.Stdout.WriteString(restartScreen)
		if eerr := syscall.Exec(exe, os.Args, tui.RestartEnv(final)); eerr != nil {
			os.Stdout.WriteString(leaveAltScreen) // nothing took over: give the terminal back
			return eerr
		}
	}
	return err
}

// Bubble Tea gives the terminal back when it quits, so a TUI exec'ing the
// new build would show the shell underneath for as long as the new one takes
// to draw. Going back to the alternate screen first keeps that flash out of
// the way, and says what is going on in case the new build is slow to start.
const (
	altScreen      = "\x1b[?1049h"
	leaveAltScreen = "\x1b[?1049l"
	restartScreen  = altScreen + "\x1b[H\x1b[2J" + "  conch is starting the new build…\r\n"
)

// configForTUI reads the settings, or says why it could not. A config.toml
// with a typo in it used to stop conch starting at all — with no way in
// short of editing the file — so it runs on the defaults instead and puts
// the reason on screen.
func configForTUI() (config.Config, string) {
	cfg, err := config.Load()
	if err == nil {
		return cfg, ""
	}
	return cfg, "settings not read (" + err.Error() + "); using the defaults"
}

func runServer(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "stop":
			c, err := connect(false)
			if err != nil {
				return downTo("stop", err)
			}
			return stopServer(c, machineFlag == "" || machineFlag == "local")
		case "reload":
			fs := flag.NewFlagSet("server reload", flag.ContinueOnError)
			bin := fs.String("binary", "", "program to reload into (default: the server's own executable path)")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			c, err := connect(false)
			if err != nil {
				return downTo("reload; run conch to start one on this build", err)
			}
			nc, err := reloadServer(c, *bin)
			if err != nil {
				return err
			}
			defer nc.Close()
			fmt.Printf("server reloaded: build %s, pid %d (panes kept)\n", nc.Server.Build, nc.Server.PID)
			return nil
		default:
			return unknownSubcommand("server", args[0])
		}
	}
	if err := withoutAsyncPreemption(); err != nil {
		return err
	}
	err := server.New(config.SocketPath(), config.Dir()).Run()
	if errors.Is(err, server.ErrAlreadyRunning) {
		return fmt.Errorf("%w on %s", err, config.SocketPath())
	}
	return err
}

// withoutAsyncPreemption makes sure the server runs with async preemption
// off, starting itself again once to get there. A reload is a syscall.Exec,
// and on macOS runtime_BeforeExec waits for every pending preemption signal
// to be taken: in a server with a thread per pane, per client and per file
// watch, one of them is not, and the exec never happens — the server stops
// serving without ever coming back (see the hot reload note in AGENTS.md).
// This runs before anything else, while there is nothing to preempt, so the
// exec here is the one that is safe to make.
func withoutAsyncPreemption() error {
	godebug, again := asyncPreemptOff(runtime.GOOS, os.Getenv("GODEBUG"))
	if !again {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil // nothing to start again: carry on as we are
	}
	env := config.MergeEnv(os.Environ(), "GODEBUG="+godebug)
	_ = execSelf(exe, os.Args, env) // an exec that fails leaves us as we are
	return nil
}

// execSelf is syscall.Exec. The tests' TestMain refuses it: run by a test,
// the exec started the whole test binary again with that test's
// temporary HOME, whose cleanup then never ran — 256 MB of Go caches left
// behind at every run of the package on macOS.
var execSelf = syscall.Exec

// asyncPreemptOff is the GODEBUG the server wants and whether it has to
// start again to get it: only on macOS, and only once.
func asyncPreemptOff(goos, godebug string) (string, bool) {
	const off = "asyncpreemptoff=1"
	if goos != "darwin" {
		return godebug, false
	}
	for _, f := range strings.Split(godebug, ",") {
		if strings.TrimSpace(f) == off {
			return godebug, false
		}
	}
	if godebug == "" {
		return off, true
	}
	return godebug + "," + off, true
}

func runNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	cwd := fs.String("cwd", "", "working directory (default: current)")
	name := fs.String("name", "", "pane name")
	agent := fs.String("agent", "", "launch a known agent (claude); remaining args are passed to it")
	account := fs.String("account", "", "the agent's account to start on (conch account list); default: the folder's pin, else its own")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *account != "" && *agent == "" {
		return errors.New("-account is an agent's: give -agent too")
	}
	command := fs.Args()
	agentArgs := ""
	if *agent != "" {
		for _, a := range command {
			agentArgs += adapter.ShellQuote(a) + " "
		}
		command = nil
	}
	// No command is the server's login shell, which is also the only
	// command an agent's pane may ask for (internal/server/launchrule.go).
	params := proto.PaneCreateParams{Name: *name, Agent: *agent, Account: *account, AgentArgs: agentArgs, Command: command, Cwd: *cwd, Cols: 120, Rows: 40}
	switch {
	case onRemoteMachine() && *cwd == "":
		// The server starts it in that machine's home, as a machine-level
		// pane, like the TUI's panes under the machine's CLI group.
		params.NoProject = true
	case onRemoteMachine():
		if err := remoteDir(*cwd); err != nil {
			return err
		}
	default:
		if params.Cwd == "" {
			params.Cwd, _ = os.Getwd()
		}
		dir, err := filepath.Abs(params.Cwd)
		if err != nil {
			return err
		}
		params.Cwd = dir
	}
	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	if *account != "" {
		if err := needAccounts(c); err != nil {
			return err
		}
	}
	var info proto.PaneInfo
	if err := call(c, proto.MethodPaneCreate, params, &info); err != nil {
		return err
	}
	fmt.Println(info.ID)
	return nil
}

func runSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	keys := fs.Bool("keys", false, "treat arguments as key names (e.g. enter ctrl+c)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return errors.New("usage: conch send [-keys] ID TEXT...")
	}
	rest := fs.Args()[1:]
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	id, err := resolvePane(c, fs.Arg(0))
	if err != nil {
		return err
	}
	if *keys {
		return call(c, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: id, Keys: rest}, nil)
	}
	return call(c, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: id, Text: strings.Join(rest, " ")}, nil)
}

func runRead(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: conch read ID")
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
	var res proto.PaneReadResult
	if err := call(c, proto.MethodPaneRead, proto.PaneRef{ID: id}, &res); err != nil {
		return err
	}
	// Drop trailing blank rows.
	end := len(res.Lines)
	for end > 0 && res.Lines[end-1] == "" {
		end--
	}
	_, err = io.WriteString(os.Stdout, strings.Join(res.Lines[:end], "\n")+"\n")
	return err
}

func runRedraw(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: conch redraw ID")
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{"pane.redraw.v1"})) > 0 {
		return errors.New("the conch server predates redrawing panes; reload it with `conch server reload`")
	}
	id, err := resolvePane(c, args[0])
	if err != nil {
		return err
	}
	return call(c, proto.MethodPaneRedraw, proto.PaneRef{ID: id}, nil)
}

func runClose(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: conch close ID")
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
	return call(c, proto.MethodPaneClose, proto.PaneRef{ID: id}, nil)
}

// stopServer asks the server to stop and waits until it has, so a following
// command doesn't reach the dying server. It closes c. A local server is
// gone when its socket refuses connections; a remote one (local false) when
// the connection to it closes.
func stopServer(c *client.Client, local bool) error {
	defer c.Close()
	if err := call(c, proto.MethodServerStop, nil, nil); err != nil {
		return err
	}
	if !local {
		// The bridge ends with the server. Don't wait long for it: connecting
		// again goes through a new bridge anyway.
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && c.Err() == nil; {
			time.Sleep(50 * time.Millisecond)
		}
		return nil
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		nc, err := net.Dial("unix", config.SocketPath())
		if err != nil {
			return nil
		}
		nc.Close()
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("server did not stop within 10s")
}

// offerUpgrade checks that the running server supports everything this
// binary uses. A server started by an older build keeps running across
// rebuilds (that is the point of it), so ask before replacing it: restarting
// stops its panes. Declining keeps the old server; the TUI then warns.
func offerUpgrade(c *client.Client) (*client.Client, error) {
	missing := c.OlderBuild()
	if len(missing) == 0 {
		return c, nil
	}
	var list proto.PaneList
	_ = call(c, proto.MethodPaneList, nil, &list)
	running := 0
	for _, p := range list.Panes {
		if p.State == proto.PaneRunning {
			running++
		}
	}
	fmt.Fprintf(os.Stderr, "The conch server (pid %d, running since %s) is from an older build and lacks: %s.\n",
		c.Server.PID, c.Server.Started.Format("Jan 2 15:04"), strings.Join(missing, ", "))
	if len(c.MissingCapabilities([]string{"server.reload.v1"})) == 0 {
		fmt.Fprint(os.Stderr, "Reload it onto this build? Panes keep running. [Y/n] ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a == "n" || a == "no" {
			return c, nil
		}
		exe, _ := buildinfo.Executable()
		return reloadServer(c, exe)
	}
	if running > 0 {
		fmt.Fprintf(os.Stderr, "Restarting it stops its %d running pane(s).\n", running)
	}
	fmt.Fprint(os.Stderr, "Restart the server now? [y/N] ")
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return c, nil
	}
	if err := stopServer(c, true); err != nil {
		return nil, err
	}
	return connect(true)
}

// reloadServer asks the server to exec bin (default: its own executable) and
// returns a connection to the reloaded server. The old connection is closed.
func reloadServer(c *client.Client, bin string) (*client.Client, error) {
	pid, loaded := c.Server.PID, loadedAt(c.Server)
	var res proto.ServerReloadResult
	err := call(c, proto.MethodServerReload, proto.ServerReloadParams{Binary: bin}, &res)
	c.Close()
	if err != nil {
		return nil, err
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		nc, err := connect(false)
		if err != nil {
			continue
		}
		if nc.Server.PID == pid && loadedAt(nc.Server).After(loaded) {
			return nc, nil // the same process, started again
		}
		if nc.Server.PID != pid {
			nc.Close()
			return nil, errors.New("a different server answered; the reload may have failed: see " + config.ServerLogPath())
		}
		nc.Close()
	}
	return nil, errors.New("the server did not come back within 15s; its panes are lost — run `conch` to start a fresh one. See " + config.ServerLogPath())
}

// reloadedNote says when a server last loaded a binary, for printing
// after its uptime. "up" counts from the *first* server of a chain of
// reloads — by design, since its panes have been alive that whole time —
// so a server upgraded a minute ago still reads as days old, and
// somebody read exactly that as proof their change had not reached the
// machine. Nothing for a server that has never reloaded, or one too old
// to say.
func reloadedNote(h proto.HelloResult) string {
	if h.LoadedAt.IsZero() || !h.LoadedAt.After(h.Started) {
		return ""
	}
	return fmt.Sprintf("  (reloaded %s ago)", time.Since(h.LoadedAt).Round(time.Second))
}

// loadedAt is when a server's program started. Servers from before
// loaded_at reset Started at every reload instead.
func loadedAt(h proto.HelloResult) time.Time {
	if !h.LoadedAt.IsZero() {
		return h.LoadedAt
	}
	return h.Started
}

// connect dials the local server (or the -m machine's), optionally starting
// it first.
// tuiPrefix is the configured tmux-style prefix, for messages.
func tuiPrefix() string {
	if cfg, err := config.Load(); err == nil && cfg.Keys.Prefix != "" {
		return cfg.Keys.Prefix
	}
	return "ctrl+b"
}

func connect(start bool) (*client.Client, error) {
	if machineFlag != "" && machineFlag != "local" {
		c, err := connectMachine(machineFlag)
		if err != nil {
			return nil, err
		}
		if err := actForHere(c, machineFlag); err != nil {
			c.Close()
			return nil, err
		}
		return c, nil
	}
	sock := config.SocketPath()
	if start {
		if err := client.EnsureServer(sock, config.ServerLogPath()); err != nil {
			return nil, err
		}
	}
	c, err := client.Dial(sock, "conch-cli")
	if err != nil {
		return nil, serverDial(err)
	}
	return c, nil
}

func call(c *client.Client, method string, params, out any) error {
	return callFor(c, method, params, out, 10*time.Second)
}

// callFor is call with a longer wait, for work that reaches the network or
// touches many files.
func callFor(c *client.Client, method string, params, out any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := c.Call(ctx, method, params, out)
	var perr *proto.Error
	if errors.As(err, &perr) && perr.Code == proto.ErrUnknown {
		return fmt.Errorf("the conch server (pid %d) is from an older build without %s; restart it with `conch server stop` (its panes stop) or run `conch` to be asked", c.Server.PID, method)
	}
	return err
}
