# conch integration file (format 3)

An integration file tells conch how to use a service's HTTP API for one
kind of job conch already does. It is the sandbox provider file grown a
`kind`; the kind this conch reads is `ci`: a branch's runs, what a failed
one printed, and re-running it — so a failure on CircleCI or GitLab CI
reaches the branch's agent as a GitHub Actions one does.

A file runs nothing. Its calls go to its own `api` (or the URL the person
gave for `base_url = "ask"`), with the person's key; a URL a reply hands
back may be fetched from a host listed in `follow_hosts`, **without** the
key. A request to any other host is refused. That is all a file can make
conch do.

It is TOML, kept as `$CONCH_HOME/integrations/NAME.toml`; a file there
replaces one conch ships of the same name.

```toml
format = 3                    # required: a conch too old for it refuses the file
kind = "ci"
name = "acme-ci"              # lowercase letters, digits, -; the file is NAME.toml
label = "Acme CI"             # how people write it
api = "https://api.acme.dev"  # https; every call's path goes after it
# base_url = "ask"            # self-hosted: the person gives the API's URL at install
#                             # (api, if set, is then the default)
key_env = "ACME_TOKEN"        # the key: this variable, else the Keychain (conch-mcp, named by it)
follow_hosts = ["logs.acme.dev"]  # hosts a URL from a reply may be fetched from, never with the key
docs = "https://docs.acme.dev/api"
adapted_by = "claude"
adapted = "2026-11-15"
key_help = "Acme → Settings → Tokens; read access, write to re-run."
note = ""                     # anything else a person must know

[auth]                        # how the key is sent; all optional
type = "header"               # header (default) | bearer | basic
header = "Authorization"      # header: default Authorization
value = "Bearer {key}"        # header: default "Bearer {key}"; must contain {key}
# scopes = ["read_builds"]    # bearer: shown to the person making the token
# user_env = "ACME_USER"      # basic: the user name's variable; the key is the password

[detect]                      # which projects use it: any one is enough
files = [".acme.yml"]         # a file in the checkout
remotes = ["acme.dev"]        # the remote's host, or a host under it
checks = ["acme/*"]           # a pull request check's name (a glob)

[project]
id = "{owner}/{repo}"         # the service's name for the project, as {project}

[states]                      # every state the API documents → pass fail pending canceled
ok = "pass"
broken = "fail"
busy = "pending"

[runs]                        # required: runs of a branch, newest first
call = "GET /runs?branch={branch}&commit={head}"
items = "runs"                # where the list is
id = "id"                     # each run's id, then optional fields:
name = "{item.workflow} #{item.number}"  # a { makes it a template over {item.x}
state = "status"
commit = "sha"
url = "web_url"
started = "created_at"        # RFC 3339, or Unix seconds or milliseconds
finished = "finished_at"

[failed_log]                  # a failed run's output: {run} is its id
job = "{jobs.0.name}"         # what failed, over the steps

[[failed_log.steps]]
as = "jobs"                   # this step's value, {jobs.x} to those after it
call = "GET /runs/{run}/jobs"
pick = "jobs[*status=broken]" # what is kept; nothing picked = nothing failed

[[failed_log.steps]]          # the last step is the log
each = "jobs"                 # once per item of jobs, as {item}
call = "GET /jobs/{item.id}"
follow = "log_url"            # fetch the URL(s) there, without the key…
text = "[*].line"             # …and read each as the lines of a JSON list

[rerun]                       # re-run what failed in {run}
call = "POST /runs/{run}/retry"
body = { failed_only = true }
```

## Operations

An operation is one `call` (with `body`, `follow`, `text` beside it) or
`steps`, a chain. A call is `"METHOD /path"` (GET, POST, PUT or PATCH)
under the API. A body is a TOML table sent as JSON.

| Operation | | conch asks it | it gives conch |
| --- | --- | --- | --- |
| `runs` | required | the runs of `{branch}` (at `{head}`, when there is one) | the fields above, mapped through `[states]` |
| `failed_log` | optional | the output of failed run `{run}` | text: the end is kept and sent to the agent |
| `rerun` | optional | re-run what failed in `{run}` | nothing |
| `stats` | optional | as `runs`, over a longer time, for the CI panel | as `runs` |

## Placeholders

Every call, pick and body may use `{project}`, `{branch}`, `{head}` (the
pull request's head commit, empty when there is none), and the remote's
parts: `{host}`, `{owner}`, `{repo}`, `{path}` (owner/repo, or
group/sub/repo) and `{path_escaped}` (that path as one URL-escaped
segment). `failed_log` and `rerun` also have `{run}`. A step's value is
`{NAME.x}` to the steps after it when it says `as = "NAME"`, and
`{reply.x}` to the step just after it either way.

`{project}` is put in as `[project] id` made it; every other value is
escaped where it lands. A query parameter left empty is dropped, so
`commit={head}` asks for nothing when there is no head. `{key}` goes in
the auth value and nowhere else.

## Steps

What a step keeps of its reply:

- `pick` — a path into the JSON reply: `a.b.0.c`. A part may select from a
  list: `items[status=failed]` is the first item whose `status` reads
  `failed`; `items[*status=failed]` every such item; `items[*]` every
  item, and `[*]` alone every item of a reply that is a list. After a
  `*`, the rest of the path is read in each item and the results put in
  one list. A selector's key may reach inside with `/` (`vcs/revision`);
  its value may be a placeholder, and one that is empty matches every
  item. A pick that finds nothing ends the operation: there is no such
  thing, which is an answer rather than an error.
- `follow` — a path to one URL or a list of them; each is fetched over
  https from a `follow_hosts` host, without the key, and read by `text`.
- `text` — `"."` keeps the reply as text; a path reads a JSON reply and
  puts what it finds together, a line each.
- `each` — the step is made once per item of an earlier step's list, as
  `{item}`, and the texts put together.

Check a file with `conch integration check FILE`, and fix what it reports
until it reports nothing.
