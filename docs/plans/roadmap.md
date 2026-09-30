# Roadmap

Order of upcoming work (updated 2026-09-29). Finished items move to the git
history; details for big items live in their own plan files.

## Done

1. **Plan-limit alerts** — notify once when Claude's or Codex's 5-hour,
   weekly or spend window passes 80% and 95% (configurable), per machine and
   window, remembered across TUI restarts until the window resets.
2. **Session search** — `/` in the Sessions view filters by title, agent,
   branch and ID as you type, and searches inside conversations on the
   server, showing a matching snippet.
3. **Share a session with another agent** — hand a saved conversation to a
   different agent (or a running one): conch writes the conversation to a
   handoff file in the checkout and starts the chosen agent there with a
   prompt to read it and continue.
4. **Broadcast to a group** — send one prompt to every agent in a project or
   group from the tree, and one command to its terminals. Waiting agents and
   terminals in mixed groups start unticked; the confirmation lists recipients.
5. **tmux extras** — `ctrl+b < > .` move tabs, `ctrl+b q` split numbers,
   `ctrl+b space` and `alt+1`–`alt+5` layouts, `w` picker names with each
   split's branch or folder and agent state.
6. **Website deploy** — `web/` published to GitHub Pages on every push.
7. **First release** — v0.1.0: archives for macOS and Linux (amd64, arm64)
   with checksums, installed by `install.sh` and `conch update`.
8. **Harvest** — finishing a task from the TUI: commit (marked files or
   single hunks), push, `gh pr create`, merge into the base (undone if it
   conflicts), discard a branch and its worktree after naming what that
   loses, and a cleanup list of leftover worktrees. Server methods behind
   `branch.harvest.v1`, `branch.hunks.v1` and `worktree.cleanup.v1`, so
   remote machines work too, and `conch branch commit|push|pr|merge|discard`
   and `conch worktree ls|clean` do the same from a shell. Hunk adoption
   across branches and a side-by-side diff stayed out of scope; marking
   hunks is a TUI step.
9. **Cost on the tree** — each agent's cost on its tree row, totals on the
   Agents section, the project and the machine, and what each saved session
   used in the Sessions list, all labelled as the usage conch has seen.
   Agents that report no cost show tokens; conch ships no price table. The
   task dialog warns when the chosen agent's plan window is past an alert
   threshold, and never refuses.
10. **Brain actions for handoff and broadcast** — the planner can propose
    `share` (one agent's conversation continued by another) and `broadcast`
    (one message to several agents), still confirmed like every other
    action and never aimed at a terminal. `/ !` filters the tree to the
    agents waiting for an answer, across machines.
11. **Best-of-N tasks** — `conch task -n N` and a list of agents try one
    prompt several times, an attempt per branch under the task's own name.
    `A` compares them: what each changed, its agent's state and cost, and
    what a command (`t`) came to in each worktree. Keeping one and
    discarding the rest goes through harvest, which still says what it
    would lose.
12. **`conch wait`** — `conch wait PANE -state done` blocks until an agent
    is done (or waiting, working, idle) using the events the server already
    broadcasts, so a shell can chain tasks without polling. It exits 124
    when `-timeout` runs out, as `timeout(1)` does.
13. **Review queue** — `Q` lists what needs a decision across every
    machine: agents waiting, agents finished, commits not pushed or
    merged, worktrees left dirty — in that order, longest wait first.
    `enter` opens the pane when an agent is waiting and the branch's
    changes otherwise, so the actions stay where harvest put them. Read
    from what the TUI already holds, so it costs no server call and needs
    no capability. Branch rows follow the tree's own window — nothing
    stale enough for the tree to hide — and sit under a heading for their
    project, so a repository is named once however many of its branches
    need you. `x` dismisses a row until what it says changes, and the
    status bar counts what is waiting. A project can name a **check** —
    its own command — which conch runs in a branch's worktree when the
    agent there finishes: the queue then says `check passed` or `check failed
    (exit 1)`, and a failure sorts to the top. No default, nothing runs
    until a command is named, and `v` runs it on demand. Not yet:
    `/` filters the list by project, branch, agent, machine or reason —
    every word has to appear — and the count says how much is hidden.
    Dismissals are kept in `ui.json`, so they outlive the TUI, and are
    forgotten once the row they were about is gone. A verdict is about the
    branch as it stood: when the branch moves the check reads "out of
    date", stops counting as a failure, and is run again once the branch
    has been quiet for half a minute with no agent writing in it.

14. **Staying in step with upstream** — `conch update` installs the latest
    release, `conch update list` shows what is published (marking the
    running, latest and locally kept versions) and `conch update rollback`
    goes back to the version before the last update, from the copy every
    update keeps in `$CONCH_HOME/versions`; `conch update VERSION` goes to
    any release, older ones included, and `install.sh` takes a version as an
    argument for machines without conch yet. `[update] auto = true` installs
    a newer release as soon as the daily check finds one, off by default.
    Going back notes the version it leaves, so it also comes forward again.
    Not yet: choosing a version from the TUI, and moving a remote machine
    back (it follows this computer's build).

15. **File explorer** — `f` browses a project's checkout, or the worktree
    of the branch (or agent) selected, in a lazily expanded tree: a
    per-filetype icon, git status beside the name, a preview with line
    numbers that describes a binary rather than printing it, `/` to find
    among what is loaded, and `a` to put a file's path into the prompt of
    the agent working in that checkout, on whichever machine it is on. `d`
    opens the file's diff, `e` its `$EDITOR`, `y` copies the path. Reading
    only, and only inside the project, behind `fs.list` and `fs.read`. See
    [file-explorer.md](file-explorer.md).

16. **Move a worktree to another machine** — `T` on a branch sends the
    branch and everything uncommitted in its worktree to another machine,
    and the agents working there carry on: their conversations are handed
    over and their panes here close. Only the commits the far side lacks go
    across; staged arrives staged and unstaged unstaged; untracked files
    and the project's local files (`.env`) go with them, while what git
    ignores stays behind. The worktree here is kept, so a move that goes
    wrong costs nothing. The project picker offers to clone the repository
    there when the machine does not have it.

17. **Reading a pane's past** — a program on the alternate screen keeps no
    scrollback, so conch watches the screen and keeps the rows that scroll
    off it: `ctrl+b [` and selection reach what an agent said a page ago.
    `/` and `?` search that history in scroll mode, `n` and `N` repeat,
    and the server searches the whole of it so a match off screen is
    found. `Y` on an agent opens the conversation it saved, to scroll and
    copy part of — the alternate screen makes that impossible in the pane
    itself.

18. **Watching a terminal** — `ctrl+b M` alerts when a pane goes quiet
    after printing, `ctrl+b A` when it prints at all: a build or a test run
    says when it is done without being watched. An icon in the status bar
    opens what conch itself is using in memory and CPU.

19. **Saved ssh sessions** — a host reached with `H` stays in the tree
    rather than being typed again after it ends or conch restarts.

20. **Sandboxes** — a machine conch makes for you in the cloud, so a long
    task runs away from the laptop and keeps going once it is closed.
    `M` → **New sandbox…** → the provider, or
    `conch sandbox [-provider NAME] create|ls|start|stop|rm`:
    conch creates the sandbox, installs itself there and adds it as an
    ordinary remote machine, so the tree, `-m` and everything else work with
    it unchanged. The API key is read from the environment every time,
    unless you choose to keep it in `config.toml`; a sandbox that fails to
    set itself up is deleted rather than left to cost; stopping and
    deleting say what they would end
    and what work has reached no remote. Two providers are supported:
    Daytona, reached with a fresh ssh token through its gateway, and
    boat.dev, whose sandboxes are whole machines with an sshd of their own —
    conch authorizes its public key there, offers that same key, and
    connects straight to them, so nothing that opens one leaves this
    computer. Both take the same
    settings, and optional interfaces cover what only some of them can do:
    ports, snapshots, cost, and a key of your own (`Previewer`,
    `Snapshotter`, `Metered`, `KeyAuthorizer`). The docs are the
    Sandboxes page under `web/src/app/docs/sandboxes`.

21. **One setup, every agent** — from a user who tried conch: "I feel
    locked in to those models while I also like to use others for some
    tasks. And then I have to sync settings, skills, mcps (and their auth)
    with codex." So `i` on a checkout now writes as well as reads: `s`
    takes the setup of the agent whose tab is open and gives it to the
    others — its instructions, its skills and the MCP servers it declares,
    each written where that agent looks for it — and `u` puts it back.
    `conch agent sync [-from NAME] [-to NAMES] [-apply] [-undo]` does the
    same from a shell, on a remote checkout too (`agent.setup.sync`, in
    `internal/agentsetup/sync.go`).

    What makes it safe to run rather than clever: nothing is written until
    the plan has been seen; a file somebody wrote by hand is left alone
    (conch writes its own marked block, or a file that wasn't there); a
    skill is linked, not copied, so `.agents/skills` serves Codex, Gemini
    and OpenCode at once; an agent that expands `@path` is pointed at the
    other file rather than given a copy of it; and every write is recorded
    under `.conch/agent-sync/` so it can be undone.

    Auth stays where it is, as the plan said it should: an agent's login is
    its own, and a server whose environment or headers hold a value rather
    than a `${VAR}` reference is not copied at all — the reason says so.
    The docs are "One setup, every agent" on the Tasks page.

    Checked against the real agents (R28): each one's own CLI shows the
    server conch wrote in its own format, Codex answered from the
    instructions copied into AGENTS.md, and Gemini read the skill linked
    into `.agents/skills`. Trust is the thing that catches people out —
    Gemini leaves a project's servers *and* skills out until the folder is
    trusted, and says nothing about the skills — so the plan says so.

    The same person asked for "an actual UI for humans, especially for
    reviewing the work", which is what the changes view, the review queue
    and the checks already are. Worth remembering when that feedback comes
    again: it was the setup, not the reviewing, that was missing.

22. **One setup, every agent: your own, not just a checkout's** — 21 gave
    the other agents what a *checkout* holds; this gives them what your
    home does, which is the half people complain about: `~/.claude/CLAUDE.md`
    and `~/.claude/skills`, `~/.codex/config.toml`, `~/.gemini/settings.json`,
    `~/.config/opencode`. It is machine-wide, so it is in Settings → Agents
    ("Give the others Claude Code's setup…", "Put the last one back…") and
    `conch agent sync -user`, behind `agent.setup.sync.user.v1`.

    The engine is 21's, with the user scope's paths; two things are its
    own. There is no `git status` in a home directory, so the plan carries
    every path in full and the record — under `$CONCH_HOME/agent-sync/`,
    since a home has no root for a `.conch` — is what puts it back. And a
    file that is a symlink is left alone with a reason, because these are
    kept in dotfiles repositories and writing through the link would edit
    one. Claude is read from `~/.claude.json` but never written there: that
    file holds its state and every project's history.

    Checked against the agents themselves in a scratch home: `codex mcp
    list` showed the server conch wrote in `~/.codex/config.toml`, and
    `gemini skills list` the skill linked into `~/.agents/skills`.

23. **A library the agents follow, Devin included, and each agent's own
    way of naming a variable** — sync copies one agent's setup once; the
    library (Settings → Agents → **Shared MCP servers & skills…**, `conch
    agent library`, behind `agent.library.v1`) keeps servers and skills in
    conch, each ticked for the agents that should have it. Apply writes what
    is missing, updates what conch wrote before, and takes out what is no
    longer wanted — only ever what conch wrote, which it remembers in
    `library/written.json` since JSON has nowhere to mark it. Claude's
    servers go in with `claude mcp add-json --scope user`, since
    `~/.claude.json` is its state file and conch still does not write it.

    Found on the way: sync copied `${TOKEN}` as it stood, and only Claude and
    Gemini expand that. Codex expands nothing in `config.toml` (it passes a
    variable by name: `env_vars`, `bearer_token_env_var`, `env_http_headers`),
    OpenCode writes `{env:TOKEN}` and Devin `${env:TOKEN}` — so a server
    reached them with the literal text for a token. References are now put in
    one form when read and written in each agent's own (`vars.go`); a server
    Codex cannot express — a renamed variable, a reference in its arguments,
    SSE — is left out with the reason. Gemini's streamable HTTP servers were
    also under `url`, which Gemini reads as SSE; they go under `httpUrl`.

    Devin is in the setup view (`i`) and in sync and the library. It reads
    Claude Code's instructions, skills and servers, and OpenCode's servers,
    by itself (`read_config_from` turns that off), so conch gives it only
    what it would not otherwise see — a second copy would be read twice.

24. **Agents working together** — an agent in a conch pane starts another
    agent, prompts it, waits for it and reads what it did, kept to its own
    work (PR #22, fixes from its first real run in #23). `conch agent
    prompt` (`agent.prompt.v1`) types only into an agent and never onto a
    question it is asking, and `-wait` waits on the agent's turn rather
    than its state. Panes take names (`conch rename`, `task -name`,
    `task.name.v1`). The server records who started each pane and keeps an
    agent calling from its pane to its own panes and project
    (`pane.scope.v1`), finding the caller from the kernel rather than
    `CONCH_PANE_ID`, and carries that scope to other machines through `-m`
    (`scope.remote.v1`). `conch agent skill` installs conch's skill, which
    teaches the agents all this (`agent.skill.v1`). All of it is
    command-line and agent-facing; the TUI shows helpers only as ordinary
    panes — see 25. Page: [Agents working together](https://amitgb14.github.io/conch/docs/agents-together).

## Next

25. **Agents working together, in the TUI** — 24 is driven from the
    command line and by agents themselves; in the TUI a helper is just
    another pane. Three things to bring in:

    - **The skill, from the setup view.** `i` (and Settings → Agents,
      beside the library) shows whether conch's skill is installed for
      each agent on the machine and installs or removes it through
      `agent.skill` — plan first, as `conch agent skill` does, then apply.
      A skill of that name conch didn't write shows as the person's, not
      as installed. Per machine, through that machine's server.
    - **Who started what, in the tree.** A helper's row says which pane
      started it (`created_by`), or sits under that agent's row, so a
      reviewer reads as the reviewer of something; its creator's row can
      say how many helpers it has and whether any is waiting. `!`, the
      review queue (`Q`) and notifications name the agent a waiting helper
      works for. Clicking and selecting it follow the same paths as keys.
    - **Prompt and wait, from the pane menu.** A *Prompt…* action on an
      agent sends one message through `agent.prompt` — refused, and saying
      so, when the agent is waiting for an answer — and reports when the
      turn it started ends: done, waiting for you, or failed, as a status
      message or a notification when the pane isn't in view.

    Servers without `agent.prompt.v1`, `pane.scope.v1` or `agent.skill.v1`
    (an older remote) get each piece left out with a word why, not a
    failed call. Docs: the help overlay, `interface` and `keys` pages, and
    the agents-together page; a row in the end-to-end plan for the TUI
    paths with real agents, mouse included.

30. **Mouse-first polish** — the one felt every day, because this is how
    conch is driven: a thing that needs a key may as well not exist. Most of
    it already works — clicking a split to focus it, dragging a split's
    border and the sidebar's edge, tabs and their `×`, `+` for a new tab,
    right-click row menus, double-click to expand a row or take a word in a
    pane, shift-drag for the terminal's own selection, the wheel over the
    tree, a pane's history and every list, and the git window's clicks — so
    what is left is specific, in the order it is missed:

    - **Drag a tab to reorder it.** `ctrl+b < >` moves tabs and the mouse
      cannot: the clearest key-only path left.
    - **Drag a pane into another split or tab.** There is no mouse way to
      move a pane at all; grabbing its title and dropping it is the obvious
      one.
    - **Scrollbars to grab.** Every list and diff scrolls by wheel alone. A
      thumb in the margin, dragged, and a click in the trough for a page.
    - **Triple-click for a whole line**, since double-click already takes a
      word (`wordAt` in `internal/tui/selection.go`).
    - **Hover.** Motion events are handled for drags already, so the row or
      button under the pointer can light up; nothing does today, so nothing
      looks clickable until it is clicked.

    Tests drive mouse messages, not keys — press, motion and release as a
    terminal sends them, a drag that ends outside the window, a drag that
    never releases — and `internal/tools/clicker` drives a real TUI for the
    end-to-end row.
31. **Agents driving conch through MCP** — `conch agent prompt`, pane names
    and the skill (24) are the command-line half; an agent that calls a tool
    gets a typed result and an error it can act on, where one that shells out
    gets a screenful of text it parses wrong under load. `conch mcp` is a
    stdio MCP server in front of the same socket, with tools for `start`,
    `prompt`, `wait`, `read`, `task`, `list` and `rename`. Scoping needs no
    new rules: the server finds the caller from the socket's peer pid walked
    up to a pane's program, so an MCP process started inside a pane inherits
    that pane's scope. `agent library` already writes an MCP server into
    every agent's own configuration, so installing it is one action rather
    than five formats. Tests: the handshake and each tool against a fake
    server, a call from outside any pane, a call to an agent that is waiting,
    and a `wait` that runs out of time.

What is parked sits under **Not now** and **Last**.

## Not now

26. **E2B sandboxes** — deferred, and taken up later rather than next.
    `internal/sandbox/e2b.go` makes, lists, pauses, resumes, ends and keeps
    alive an E2B sandbox through its platform API, with tests against a
    fake one; it stays in the tree, unregistered, so nothing offers to make
    a sandbox conch cannot reach. What is missing is reaching it: E2B has
    no ssh. Its agent inside the sandbox (envd) runs processes and
    terminals over ConnectRPC on plain HTTPS — `Process/Start` with stdin
    enabled, `Process/StreamInput` for what is typed, the start stream for
    what comes back, `Process/Update` to resize — so what conch needs is a
    command that speaks that: `conch sandbox exec`, handed back by
    `remote.TransportFor` as an ordinary exec.Cmd, leaving install, bridge
    and panes as they are. Also needed: registering the provider, and
    pushing the sandbox's clock back while conch is connected, since E2B
    ends one on a time to live rather than on idleness.

    Two providers cover the case today, and boat.dev showed how much of a
    provider is its own peculiarities rather than the interface. This waits
    until there is a reason to want a third.

27. **MicroVM sandboxes on your own hardware** — see
    [microvm-sandbox.md](microvm-sandbox.md). 20 covers the case that
    mattered: somewhere isolated to run an agent, made and thrown away from
    conch. What this plan adds is a sandbox on hardware you own — a
    Firecracker or Kata microVM on a Linux host, Apple `container` or Lima
    on a Mac — for code that can't leave the building, or when a cloud
    provider is not wanted. Still deferred: a VM added as an ordinary
    machine gives the same boundary today, so what is left is lifecycle
    convenience.
32. **Two tiers of agent support** — conch supports five agents where the
    widest tools detect twenty, because `AGENTS.md` asks for an adapter,
    detection from the real agent, sessions that list, resume and delete, a
    setup inspector, docs and an end-to-end row. Those catalogs *detect*; conch
    *supports*. Rather than fifteen more to that bar: **supported** keeps the
    contract, and **runs here** is a manifest anybody can drop in
    (`$CONCH_HOME/agents/<name>.toml`) naming the binary, how a first prompt
    and a resume are passed, and the detect rules, with `conch agent add`
    writing one. Detection falls back to the process name and the generic
    screen rules; sessions and the setup view say *not read for this agent*
    rather than pretending, and each row says which tier it is.
    `internal/detect` already loads manifests, so the work is making
    `internal/adapter`'s `Registry` data as well as code. It waits because
    the five that are supported are the five in use here.
33. **Review in the terminal** — side-by-side diff above a width threshold,
    word-level highlighting inside a changed line, and opening the file at
    the selected hunk in `$EDITOR`. The changes view already picks hunks, so
    this is the reading of them, not the picking. Where review is the
    bottleneck the honest answer stays pairing conch with `gh` or a graphical
    tool, and the docs should say so.
34. **Custom actions** — an `[actions]` table in `config.toml` putting a named
    command on the pane menu with `CONCH_PANE`, `CONCH_PROJECT` and
    `CONCH_BRANCH` in its environment. It is what most of a plugin system is
    for, without a registry to host or an API to keep stable for other
    people's code.

## Last

28. **Auto-approve rules** — per-project rules that let agents run safe
    commands without waiting for the user. A sandbox is the boundary these
    need, and 20 gives one: the rules would be allowed there and nowhere
    else, so a rule that skips a confirmation cannot reach the laptop.
29. **Task graph** — server-side rules such as "when A is done, start a
    review agent on its worktree", only once auto-approve rules exist, since
    they run actions nobody confirmed. Notifies rather than moving focus.

## Decisions

- conch stays one Go binary in a terminal somebody already has: no embedded
  browser, no desktop or mobile application. A phone reaches it over ssh with
  any terminal client, and if that ever needs more it should be
  `conch status`-shaped — read-only, one screen — not a second interface to
  keep in step.
- **Windows waits.** The pane layer is `select(2)` on ptys, unix sockets and
  `syscall.Exec` for the hot reload; a port means ConPTY, named pipes and
  another reload story, each of them somewhere the reload can wedge with
  panes running. WSL2 runs conch today, and the website and README should say
  so rather than leave it an open question.
- **No plugin marketplace.** 34 covers what plugins are mostly for; a
  marketplace is a distribution business and an API surface to keep stable
  for other people's code.
- New worktrees keep copying `.env`, `.env.*` and `.envrc` by default
  (`internal/server/localfiles.go`), so agents in a task have the same local
  setup as the main checkout.

Every item ships with tests covering its edge cases (see `AGENTS.md`) and a
row in the [end-to-end plan](../testing/end-to-end.md) for what fakes can't
prove.
