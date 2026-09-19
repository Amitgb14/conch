# Plan: microVM sandboxes for agents

Status: draft, not started (2026-09-13). Nothing here is built yet.

## Goal

Run an agent task inside a disposable microVM, so the agent can work
unattended with permission prompts off and still can't touch the host:
your home directory, keys, other repositories, or the machine itself.

A sandbox is a **machine** in conch's sense. It runs its own conch server,
appears in the sidebar like `devbox`, and shows its panes, projects, branches
and diffs through the protocol that already exists. The new pieces are:

- how conch reaches the server inside (a command, not ssh)
- how the VM is created and destroyed
- how code goes in and comes back

### Non-goals (for the first version)

- Sandboxing agents that run directly on the host.
- A general VM manager. conch creates sandboxes for tasks and removes them.
- Windows hosts.

## User experience

- **Task dialog (`t`):** a new **Run in** field: `this machine` or `sandbox`.
  With `sandbox`, the task starts in a new VM, and the agent starts in
  autonomous mode (Claude `--dangerously-skip-permissions`, Codex full-auto).
  This is safe because nothing outside the VM is reachable.
- **Sidebar:** `▾ ◇ sandbox · fix-login` appears under the machine that hosts
  it. It carries the usual state, spinner and waiting badge, and a lifecycle
  chip: creating, running, stopped.
- **Sandbox menu (`m`):**
  - **Bring branch back**: fetches the task's commits into the host repo,
    optionally checking the branch out into a worktree.
  - **Open shell**
  - **Stop / Start**
  - **Destroy** (confirm; warns if commits haven't been brought back)
- **CLI:**
  - `conch sandbox create [-image I] [-project ID] [-branch B] [-agent A] PROMPT`
  - `conch sandbox ls | stop | start | rm | shell ID`
  - `conch sandbox bring-back ID`
- **Brain:** `start_task` gains `"sandbox": true`. The planner prefers
  sandboxes for long or risky work when sandboxes are enabled.
- **Settings → Sandbox tab:**
  - backend and its status (installed / not)
  - default image
  - CPU and memory
  - network policy
  - which credentials each sandbox may receive
  - auto-destroy after brought back / after N days idle

## Architecture

```
TUI / CLI (host)
  └─ remote.Transport ──exec──▶ `container exec -i <vm> conch bridge` ──▶ conch server (in VM)
                                                                        └─ agent panes, git, hooks
sandbox.Backend (host): create / start / stop / destroy / exec / copy
```

### 1. Transport abstraction (`internal/remote`)

The package assumes ssh today: `ProbeMachine`, `Install` and `Bridge` all
take an ssh `target` and call `run`/`sshCmd`. Introduce:

```go
// Transport runs commands on a machine.
type Transport interface {
    // Command returns a command running script on the machine, with
    // stdin/stdout connected (for probe, install and bridge).
    Command(ctx context.Context, script string) (*exec.Cmd, error)
    // Describe is for messages, e.g. "gpu-box" or "sandbox fix-login".
    Describe() string
}
```

- `sshTransport{target, interactive}` wraps today's code, with no behaviour change.
- `execTransport{argv []string}` runs e.g. `container exec -i NAME sh -c SCRIPT`.
- `ProbeMachine`, `Install`, `Connect` and `Bridge` take a `Transport`.
  `Connect` keeps its install / outdated logic unchanged.
- **Test harness for free:** an `execTransport` of `sh -c` runs
  `conch bridge` locally against a second `CONCH_HOME`. That gives end-to-end
  tests of the whole remote path without ssh or VMs.

### 2. Machine catalog (`internal/remote/catalog.go`)

- `Machine` gains `Kind string` (`ssh` default, `sandbox`) and
  `Sandbox *SandboxRef{Backend, Name, Host, Project, Branch, Created}`.
- `Host` is the machine the VM runs on: `local`, or a remote machine ID for
  later phases.
- The TUI's `machine.connect` picks the transport from `Kind`. The rest of the
  TUI (tree, panes, changes, notifications) needs no change.

### 3. Sandbox backends (`internal/sandbox`)

```go
type Backend interface {
    Name() string
    Check(ctx) error                                   // installed, running, supported
    Create(ctx, Spec) (Instance, error)                // image, name, cpus, memory, mounts, env, network
    Start(ctx, name) error
    Stop(ctx, name) error
    Destroy(ctx, name) error
    List(ctx) ([]Instance, error)                      // to reconcile after restarts
    ExecArgv(name string, script string) []string      // for execTransport
    CopyIn(ctx, name, hostPath, guestPath string) error
}
```

| Backend | Host | Notes |
|---|---|---|
| **apple** (first) | macOS 26+, Apple silicon | `container` CLI; each container is a lightweight VM (Virtualization.framework); OCI images; folder sharing |
| **lima** (fallback on Mac) | macOS/Linux | full VM, slower to start; fine for a long-lived shared sandbox |
| **cloud-hypervisor** / **firecracker** (later) | Linux + KVM | Firecracker has **no virtio-fs**, so code goes in by copy or a block device. That's fine with the clone-in design below. Cloud Hypervisor has virtio-fs. |
| **docker** (optional, weaker) | anywhere | container isolation only, labelled as such in Settings |

### 4. Server inside the VM

- **Image:** a published OCI image `ghcr.io/amitgb14/conch-sandbox` containing:
  - Debian slim
  - git, curl, ca-certificates, ripgrep
  - Node 20
  - Claude Code, Codex, Gemini CLI, OpenCode
  - a `conch` user
  - no conch binary, which is copied in at start so it always matches the client build
- **Bootstrap:** the existing `remote.Install` path does it: probe → copy the
  binary for `linux/arm64` (or `amd64`) → `conch bridge`. The cross-build and
  release-download fallbacks already exist.
- **Agent hooks:** they need no change. They call `conch report` inside the VM,
  which talks to the in-VM server.

### 5. Code in, code out

**The git trap:** a worktree's `.git` is a file pointing at an absolute host
path, `<repo>/.git/worktrees/<name>`. Mounting a worktree into a VM at a
different path breaks git. Mounting the repo's `.git` read-write fixes that,
but lets the agent write `.git/hooks` or `.git/config`, which **run on the
host** the next time you use git. That is a sandbox escape. So the default is
to **clone in and fetch back**:

1. **In:**
   - Mount the host repo **read-only** at `/src/origin` and clone inside the VM:
     `git clone --no-hardlinks /src/origin /work/<project>`.
   - Check out the base commit and create the branch there.
   - Where the backend can't mount, copy a `git bundle` in instead.
   - Copy the project's **local files** (the gitignored patterns from
     `project.set_files`) in explicitly. The user opts in, since these are often
     secrets.
2. **Work:**
   - The in-VM server registers `/work/<project>` as a project.
   - The sidebar shows its branch, changes and diffs as for any remote machine.
3. **Out (Bring branch back):**
   - `git bundle create - <base>..<branch>` inside the VM, streamed over the
     transport, then `git fetch <bundle> <branch>:<branch>` on the host.
   - Fetch only moves objects and refs, never hooks or config.
   - Refuse to overwrite a host branch that has diverged; offer `<branch>-sandbox`.
   - Optional: create a host worktree for it.

A second mode, **shared worktree** (mount the worktree plus `.git` at identical
paths), is faster and shows edits live on the host. It stays opt-in with a
clear warning, and is never used together with autonomous agent mode.

### 6. Credentials

The host's Claude login lives in the macOS keychain and can't be mounted.
Per agent:

| Agent | Into the VM as |
|---|---|
| Claude Code | `CLAUDE_CODE_OAUTH_TOKEN` (made once with `claude setup-token`), or `ANTHROPIC_API_KEY` |
| Codex | `OPENAI_API_KEY`, or a copied `~/.codex/auth.json` (opt-in) |
| Gemini CLI | `GEMINI_API_KEY` |
| OpenCode | its providers' key variables |
| git push (optional) | a fine-grained `GH_TOKEN` scoped to the repo; off by default. Bringing the branch back needs none. |

- conch stores **which** variables a sandbox may get, not their values.
- Values come from the host environment, or from the macOS keychain via `security
  find-generic-password` under a `conch` service. They go into the VM
  environment at create time, never written to the image or to disk inside.
- Settings → Sandbox shows each credential as available or missing.

### 7. Network policy

- `open` (default at first), `restricted` or `off`.
- `restricted` allows only:
  - the model APIs: api.anthropic.com, api.openai.com, generativelanguage.googleapis.com
  - package registries
  - the repo's git host
- **Implementation depends on the backend** (verify in phase 0). Two candidates:
  - backend network filtering, if it has it
  - a small egress proxy on the host (HTTP CONNECT allow-list), with the VM's
    `HTTPS_PROXY` set and direct egress blocked by the VM's firewall inside the
    image, set up by root at boot before the agent user starts
- A proxy that can be bypassed from inside is **not** a security boundary. Label
  `restricted` honestly until direct egress is blocked outside the guest's
  control.

### 8. Lifecycle and reconciliation

- **Catalog:** `sandboxes` in `machines.json` records backend, name, project,
  branch, base commit, created, brought-back.
- **After restarts:** on TUI and server start, `Backend.List` reconciles the
  catalog. Unknown VMs named `conch-*` are shown as orphans with a Destroy
  action.
- **Auto-destroy:** after a successful bring-back (optional), or after N days
  stopped.
- **Resource limits:** CPUs and memory per sandbox. Show a warning past a
  configurable number of running sandboxes.

## Protocol and capabilities

- The server inside a VM is the standard server; no new methods are needed there.
- On the host server, sandbox management can stay **client-side** at first: the
  TUI and CLI call the backend directly, as they do for ssh machines.
- Move it to a server method only for phase 6, sandboxes on remote hosts:
  - `sandbox.create`, `sandbox.list`, `sandbox.destroy`, `sandbox.bring_back`
  - capability `sandbox.v1`
  - the remote host's server runs the backend, and the client bridges through
    the host: `ssh devbox -- container exec -i … conch bridge`, which is an
    `execTransport` wrapped in ssh.

## Config

```toml
[sandbox]
backend = "apple"              # apple | lima | docker | cloud-hypervisor
image = "ghcr.io/amitgb14/conch-sandbox:latest"
cpus = 4
memory = "8G"
network = "open"               # open | restricted | off
credentials = ["CLAUDE_CODE_OAUTH_TOKEN"]
local_files = false            # copy the project's local files in
autonomous = true              # skip agent permission prompts inside sandboxes
destroy_after_bring_back = false
```

## Phases

Each phase ends with passing race tests, a live check in the nested TUI
harness, and a local commit.

### Phase 0: spike (verify before building)
- The Apple `container` CLI on this Mac (macOS 26.1, arm64):
  - exact flags for run/exec/stop/rm, volumes (read-only), cpus/memory, env
  - is `exec -i` stdio clean and fast enough for the NDJSON bridge (frame
    streaming at 30 fps)?
  - does it filter the network?
- `claude` inside Linux with `CLAUDE_CODE_OAUTH_TOKEN`: headless login works,
  hooks fire, and `--dangerously-skip-permissions` is accepted as the conch user
  (it refuses as root).
- Clone-in from a read-only mount, then bundle-out and fetch-back, round trip.
- Boot time and memory per sandbox. Target: a VM with a running agent in under
  10 s.
- **Done when:** a short written note of findings, and this plan updated.

### Phase 1: transport abstraction — done

`Transport` is in `internal/remote/transport.go`, with `sshTransport` and
`execTransport`; `ProbeMachine`, `Install`, `Connect` and `Bridge` take one,
and callers pass `remote.SSH(target, interactive)`. `Options.Interactive`
went with it: a transport already knows whether it may prompt. `Bridge` asks
the transport for a non-prompting variant first, since its stdin and stdout
carry the protocol.

The exec path has the end-to-end test the plan asked for
(`cmd/conch/transport_test.go`): `Exec("…", "/bin/sh", "-c")` against a
scratch `HOME` and `CONCH_HOME` runs the whole path — probe, capability
check, bridge, then a pane created and read — in about half a second,
without ssh. ssh machines behave as before: the suite passed unchanged, and
a real machine was exercised end to end in R13 the same day.

### Phase 2: backend + image
- `internal/sandbox` with `apple` (and a `fake` backend for tests).
- `docker/sandbox/Dockerfile` for the image, and a CI workflow that publishes it
  on tags.
- `conch sandbox create/ls/stop/start/rm/shell`, with the catalog and
  reconciliation.
- **Done when:** `conch sandbox create` boots a VM, bootstraps conch, and
  `conch -m sandbox-x status` works.

### Phase 3: tasks in sandboxes
- Clone-in, branch creation, local files (opt-in), credentials, autonomous
  agent flags per adapter (a new `Adapter.AutonomousArgs()`).
- TUI:
  - task dialog **Run in** field
  - sandbox rows and lifecycle chip
  - sandbox menu
  - Settings → Sandbox tab
- **Done when:** `t` → sandbox starts Claude working on a branch inside the VM,
  and its diff shows in the sidebar.

### Phase 4: bring back
- Bundle stream over the transport, host fetch, divergence handling, optional
  worktree, auto-destroy.
- **Done when:** commits made in the VM land on the host branch, with no hooks
  or config transferred. Test: a malicious `.git/hooks/post-checkout` in the VM
  must not reach the host.

### Phase 5: network policy
- `off` and `restricted` per the phase 0 findings, labelled truthfully.
- **Done when:** a test from inside the VM shows blocked hosts failing and
  allowed hosts working.

### Phase 6: Linux and remote hosts
- A Cloud Hypervisor (virtio-fs) or Firecracker (copy-in) backend.
- `sandbox.*` server methods and capability, so sandboxes can run on `devbox`.
- **Done when:** a sandbox on a Linux machine, driven from the Mac TUI.

### Phase 7: brain
- `start_task.sandbox`, world includes sandbox availability, and the planner
  prefers sandboxes for parallel or autonomous work.
- Summaries work unchanged (screens come from the in-VM server).

## Security checklist

- [ ] No host path is mounted read-write in the default mode.
- [ ] Nothing that git executes on the host (`.git/hooks`, `.git/config`, `core.fsmonitor`, filters) can be written from the VM.
- [ ] Bring-back uses bundles and fetch only. Never `git pull` from a VM-controlled repository config.
- [ ] Credentials are passed per sandbox, from an allow-list, never baked into images.
- [ ] Autonomous agent flags are only ever added inside a sandbox, never on host panes.
- [ ] The agent runs as a non-root user inside the VM.
- [ ] The docker backend is labelled "container isolation" and doesn't enable autonomous mode by default.
- [ ] Network `restricted` isn't described as a boundary unless direct egress is blocked outside the guest.

## Risks and open questions

- **Apple `container` maturity:** it is new; its CLI flags and networking may change.
  Keep the backend thin, and have Lima ready as a fallback.
- **Claude token login:** `setup-token` tokens are long-lived. Document revoking
  them. Check subscription terms for use inside VMs.
- **Streaming overhead:** frames go over `exec -i`. Measure in phase 0. If slow,
  forward a vsock or unix socket instead.
- **Disk use:** agents' `node_modules` per sandbox. Consider a shared read-only
  package cache mount.
- **Branch collisions** between host and sandbox with the same name. Prefix
  sandbox branches (`sandbox/<slug>`) or detect on bring-back.
- **Open:** should the shared-worktree mode exist at all, given the hook risk?
  (Leaning: only without autonomous mode, behind a warning.)
