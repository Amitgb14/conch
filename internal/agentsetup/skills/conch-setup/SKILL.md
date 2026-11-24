---
name: conch-setup
description: Get a project ready for agents in conch — check conch and the agents, find what tasks in this project need (local files like .env, a setup command for new worktrees, one setup shared by every agent, conch's skills, integrations for CI, deploys and tickets), ask where agents should run (this computer, a remote machine already connected, or a new sandbox), and hand the person the few commands that change their setup. Use when you are inside conch (CONCH_PANE_ID is set) and the person asks to set up conch for a project, onboard a repository, or why agents in it are missing something.
metadata:
  installed-by: conch
---

# Set up a project for agents

You look at everything and propose; the person runs what changes their
setup. conch refuses those steps from an agent's pane — which local files
reach every worktree, what runs in new worktrees, what every agent loads,
which skills are installed — because they are the person's, and secrets
are among them. Don't look for a way round a refusal: hand them the
command.

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

## 1. Look

All of this only reads:

```sh
conch doctor                     # conch, the server, agents, git, gh, the terminal
conch project ls                 # is this project in conch? its ID
conch project files ID           # ignored files copied into every new worktree
conch project setup ID           # the command run in every new worktree
conch agent setup                # what each agent loads here: instructions, skills, MCP servers
conch agent sync                 # a plan only: what giving every agent one setup would write
conch integration ls             # the CI, deploy and ticket services the project uses
conch machine ls                 # remote machines and sandboxes already connected
conch status                     # panes already running in the project
```

Then the project itself:

- **Files tests need that git ignores** — `.env`, `config/local.*`,
  certificates for a local server. Compare with `conch project files ID`.
- **How dependencies are installed** — a lock file (`pnpm-lock.yaml`,
  `package-lock.json`, `go.sum`, `uv.lock`, `Gemfile.lock`) means a fresh
  worktree needs an install before its tests run.
- **Its test command** — the Makefile, package.json or README, run once
  here to know it works.
- **Its CI** (`.github/workflows`, `.circleci`, `.gitlab-ci.yml`), **how it
  deploys** (`vercel.json`, `netlify.toml`), **its tracker** (issue links
  to Linear or Jira in the README or recent commits).
- **What it builds for** — a Dockerfile, a systemd unit, `GOOS=linux` on a
  Mac, a GPU, a suite too heavy for this computer.

## 2. Propose, as one list

Show the person one numbered list — each line what you found and the
command that sets it — and leave out what is already right, saying so in
one line:

| Found | Command, for the person to run |
| --- | --- |
| The project is not in conch | `conch project add PATH` |
| Ignored files tests need don't reach worktrees | `conch project files ID PATTERN...` |
| New worktrees don't install dependencies | `conch project setup ID pnpm install` |
| Agents load different instructions or servers | `conch agent sync -from claude -apply` |
| conch's skills are missing for some agent | `conch agent skill -apply` |
| A CI, deploy or ticket service with no integration | `conch integration install NAME` (it asks for the key) |

`conch project files ID PATTERN...` replaces the list, so the line you
propose keeps every pattern already there and adds the new ones. Name
only files the person's tests need: a pattern copies whatever matches,
secrets included, into every worktree.

## 3. Where agents run

Ask once, with what is connected already (`conch machine ls`) filled in:

```
Where should agents work on this project?
  1. This computer (as now)
  2. A machine already connected:  buildbox (ssh, conch 0.2.2), gpu-1 (ssh)
  3. A new sandbox:  daytona (key set), boat (no key yet)
```

Put first the choice that suits what you found in step 1 — a Linux
service on a Mac, a heavy suite, the person wanting agents off their
laptop — and say why in a line. With nothing connected, choice 2 says
so and offers `conch machine add user@host` for them to run: it reaches
the host over ssh and installs conch there.

Then, by their answer:

- **This computer** — nothing more to do.
- **A connected machine** — check it answers: `conch -m buildbox status`.
  If it says its conch is older, tell them `conch machine upgrade ID`.
  The project has to be on that machine too:
  `conch -m buildbox project clone URL`. From then on, work starts there
  with `conch -m buildbox task "..."`.
- **A new sandbox** — list the providers with `conch sandbox providers`.
  One without its key needs the person to run
  `conch sandbox provider install NAME`, which asks for the key — never
  ask for it yourself. Once there is a key, tell them a sandbox is billed
  by the provider until it is stopped, and with their yes make one:

  ```sh
  conch sandbox -provider daytona create -label PROJECT-dev
  ```

  It is added as a machine; check it with `conch machine ls`, and stop it
  when it is not needed with `conch sandbox -provider daytona stop ID`. For
  a sandbox per task instead of one kept running,
  `conch task -sandbox daytona "..."` makes one for that task alone.

## 4. Check again, and report

When they say they ran the commands, look again with step 1's commands
and say what is now right and what is not. Run the project's test command
once in a fresh worktree if they want proof that a new task starts ready.

End with what they could do next, a line each: the autopilot
(`conch-autopilot`), a first ticket taken to a merged pull request
(`conch-factory`), a check on everything running (`conch-status`).

## Rules

- Propose; the person runs every command that changes their setup.
- Never ask for, paste or print a key: the command that needs one asks.
- Never propose a file pattern that copies a secret the person didn't
  name, and never copy one into a worktree yourself.
- Make a sandbox only after the person chose it and said yes to its cost.
- Use `conch agent prompt` to message an agent, never `conch send`.
