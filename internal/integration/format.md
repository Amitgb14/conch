# conch integration file (format 3)

An integration file tells conch how to use a service's HTTP API for one
kind of job conch already does. It is the sandbox provider file grown a
`kind`. This conch reads four:

- `ci`: a branch's runs, what a failed one printed, and re-running it —
  so a failure on CircleCI or GitLab CI reaches the branch's agent as a
  GitHub Actions one does. The example below is one.
- `tickets`: a tracker conch has no code for (Shortcut, GitLab's issues),
  read and changed as Linear and Jira are — see [Tickets](#tickets).
- `notify`: where conch says an agent is waiting, an agent is done, or a
  pull request merged — a Slack or Discord webhook, ntfy — see
  [Notify](#notify).
- `deploy`: where a branch is built and shown (Vercel, Netlify) — its
  preview links, a failed build's log, building again and releasing to
  production — see [Deploy previews](#deploy-previews).

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

An operation is one `call` (with `body`, `headers`, `follow`, `text`
beside it) or `steps`, a chain. A call is `"METHOD /path"` (GET, POST,
PUT or PATCH) under the API. A body is a TOML table sent as JSON, or a
string sent as plain text. `headers` are the service's own, filled like a
body; a header that would carry a credential, or that conch sets, may not
be given.

For kind `ci`:

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

## Tickets

```toml
format = 3
kind = "tickets"
name = "acme-tickets"
label = "Acme Tickets"
api = "https://api.acme.dev/v1"
key_env = "ACME_TOKEN"

[detect]                      # optional: a tracker is usually tied to a
remotes = ["acme.dev"]        # project by [[trackers.projects]] instead

[project]                     # optional: {project}, when the tracker is per repository
id = "{path_escaped}"

[list]                        # required: open tickets, most recently changed first
call = "GET /tickets?state=open&team={scope}&per_page={limit}"
items = "tickets"             # where the list is
key = "number"                # each ticket's key, as conch gives it back as {ticket}
title = "title"
description = "body"          # optional, as are url and status
url = "web_url"
status = "state"

[mine]                        # optional: as list, the tickets assigned to the key's person
call = "GET /tickets?state=open&assignee=me&per_page={limit}"
items = "tickets"
key = "number"
title = "title"

[get]                         # required: one ticket, {ticket}
key = "number"
title = "title"
description = "body"
status = "state_id"
status_names = "{states}"     # status is an id: its name is in this {id, name} list
comments = "comments"         # a path into the ticket, or {STEP.path}
comment_author = "user.name"
comment_body = "text"

[[get.steps]]
as = "states"
call = "GET /states"

[[get.steps]]
call = "GET /tickets/{ticket}"

[comment]                     # optional: {body}
call = "POST /tickets/{ticket}/comments"
body = { text = "{body}" }

[link]                        # optional: {url} and {title} (the url when none is given)
call = "POST /tickets/{ticket}/links"
body = { url = "{url}", title = "{title}" }

[move]                        # optional: {status}, as the person typed it
call = "PUT /tickets/{ticket}"
body = { state = "{status}" }
```

| Operation | | conch asks it | placeholders |
| --- | --- | --- | --- |
| `list` | required | the open tickets | `{scope}` (the team or project `[[trackers.projects]] key` names, else empty), `{limit}` |
| `mine` | optional | the open tickets assigned to the key's person | as `list` |
| `get` | required | one ticket, with its comments | `{ticket}` |
| `comment` | optional | add a comment | `{ticket}`, `{body}` |
| `link` | optional | attach a branch or pull request; a service with no links says it in a comment | `{ticket}`, `{url}`, `{title}` |
| `move` | optional | move it to a status | `{ticket}`, `{status}` |

A ticket's field is a path into the ticket or, holding a `{`, a template
over `{item.x}` and the steps. A selector matches without regard to case,
so `[*name={status}]` finds *In Review* for "in review". There is no
`[states]`: a ticket's status is shown as the service words it. `{project}`
exists only when `[project] id` makes it; a tracker that is not per
repository leaves both out. A ticket is named `NAME:KEY` (`shortcut:123`)
wherever conch takes one, or by its key alone in a project tied to the
tracker. Linear and Jira are conch's own code and need no file.

## Notify

```toml
format = 3
kind = "notify"
name = "acme-chat"
label = "Acme Chat"
api = "https://hooks.acme.dev"  # the host the webhook's address must be on
key_env = "ACME_WEBHOOK_URL"    # the webhook's address: the secret

[auth]
type = "url"                  # the key is the address calls go to

[send]                        # required: {title} {body} {url}
call = "POST"                 # the method alone, or a /path or ?query added to the address
body = { text = "*{title}*\n{body}\n{url}" }  # a table is JSON; a string is sent as text
headers = { X-Priority = "3" } # optional; never Authorization, Cookie, Host or Content-Type
text = "."                    # read the answer as text: a webhook's is often "ok"
```

`[auth] type = "url"` is for a webhook, whose address is its secret: it is
read from `key_env` or the Keychain like any key, never logged or shown,
and must be an `https` address on `api`'s host (or on the address the
person gave for `base_url = "ask"`); one anywhere else is refused before
anything is sent. A notify file may also send a key the usual ways, to a
`call` under `api`.

conch's server sends through every notify file whose key is set: when an
agent starts waiting, when one finishes (as `[notify] waiting` and `done`
allow, and only while `[notify] enabled`), and when the autopilot merges a
pull request. `{url}` is empty when there is nothing to link to — a
template's empty placeholder is left out, and a header that ends up empty
is not sent. `{title}` and `{body}` are cut to 200 and 1500 characters.

## Deploy previews

```toml
format = 3
kind = "deploy"
name = "acme-deploy"
label = "Acme Deploy"
api = "https://api.acme.dev"
key_env = "ACME_TOKEN"

[auth]
type = "bearer"

[detect]                      # required, as for ci: how conch knows a project uses it
files = ["acme.json"]
checks = ["Acme*"]

[states]                      # every state the API documents → ready building failed canceled
READY = "ready"
BUILDING = "building"
ERROR = "failed"
CANCELED = "canceled"

[previews]                    # required: a branch's deploys, newest first
items = "deployments"
id = "uid"
name = "name"
state = "state"
url = "https://{item.host}"   # the preview's address
commit = "sha"
environment = "target"        # production, preview…
error = "error_message"       # why it failed
inspect = "inspector_url"     # the service's own page for it
started = "created"
finished = "ready"

[[previews.steps]]            # find the project by the repository it builds from
as = "proj"
call = "GET /projects?repo=https://{host}/{path}"
pick = "projects.0"

[[previews.steps]]
call = "GET /deployments?project={proj.id}&branch={branch}&sha={head}"

[failed_log]                  # what failed deploy {deploy} printed
call = "GET /deployments/{deploy}/events"
text = "[*].text"

[redeploy]                    # build {deploy} again
call = "POST /deployments/{deploy}/redeploy"

[[promote.steps]]             # release {deploy} to production
as = "dep"
call = "GET /deployments/{deploy}"

[[promote.steps]]
call = "POST /deployments"
body = { from = "{deploy}", name = "{dep.name}", target = "production" }
```

| Operation | | conch asks it | placeholders |
| --- | --- | --- | --- |
| `previews` | required | a branch's deploys, newest first, at `{head}` when there is one | `{branch}`, `{head}`, the remote's parts |
| `failed_log` | optional | what a failed build printed: the end goes to the agent | `{deploy}` |
| `redeploy` | optional | build a deploy again | `{deploy}` |
| `promote` | optional | release a deploy to production | `{deploy}` |

A deploy's fields are a run's (`items`, `id`, `name`, `state`, `url` —
the preview's address — `commit`, `started`, `finished`) and three more:
`environment`, `error` and `inspect`. `[states]` maps every state to
`ready`, `building`, `failed` or `canceled`, and must map something to
`ready` and to `failed`; a state the file does not name is read as
`building`. A pick that finds nothing in `previews` is no deploys; in
`redeploy` or `promote` it is an error, since nothing was done. A
`failed_log` that reads as empty text is no log.

A query may hold a URL (`repo=https://{host}/{path}`, escaped where it
lands); the path may not. conch asks the person before `promote`, which
changes what everybody is shown: `conch integration promote` asks, and
`-y` is the person saying yes beforehand.

Check a file with `conch integration check FILE`, and fix what it reports
until it reports nothing.
