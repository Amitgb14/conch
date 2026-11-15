# conch integration file

An integration file tells conch how to use a service's HTTP API for one
kind of job conch already does. The kind this conch knows is `ci`: where a
branch's failed run is, and what it printed, so a failure on CircleCI or
GitLab CI reaches the branch's agent as a GitHub Actions one does.

A file runs nothing. Its calls go to its own `api`, with the person's key,
and a URL a reply hands back may be fetched from a host listed in `follow`,
without the key. That is all a file can make conch do.

It is TOML, kept as `$CONCH_HOME/integrations/NAME.toml`; a file there
replaces one conch ships of the same name.

```toml
kind = "ci"
name = "acme-ci"              # lowercase letters, digits, -; the file is NAME.toml
label = "Acme CI"             # how people write it
api = "https://api.acme.dev"  # https; every call's path goes after it
key_env = "ACME_TOKEN"        # the key: this variable, else the Keychain (conch-mcp, named by it)
auth_header = "Authorization" # default
auth_value = "Bearer {key}"   # default; must contain {key}
follow = ["logs.acme.dev"]    # hosts a URL from a reply may be fetched from, never with the key
docs = "https://docs.acme.dev/api"
adapted_by = "claude"
adapted = "2026-11-15"
note = ""                     # what a person must know: which token, which scopes

[detect]                      # which projects use it: any one is enough
files = [".acme.yml"]         # a file in the checkout
remotes = ["acme.dev"]        # the remote's host, or a host under it

[project]
id = "{owner}/{repo}"         # the service's name for the project, as {project}

[failed_log]
workflow = "{run.name}"       # which run answered, over the steps' values
url = "{run.web_url}"

[[failed_log.steps]]
as = "run"                    # this step's value, {run.PATH} to those after it
call = "GET /runs?branch={branch}&commit={head}"
pick = "runs[status=failed]"  # what is kept; nothing picked = no failed run

[[failed_log.steps]]
as = "jobs"
call = "GET /runs/{run.id}/jobs"
pick = "jobs[*status=failed]"

[[failed_log.steps]]          # the last step is the log
each = "jobs"                 # once per item of jobs, as {item}
call = "GET /jobs/{item.id}/log"
text = true                   # the reply is the log as it is
```

## Placeholders

Every call, fetch and pick may use `{project}`, `{branch}`, `{head}` (the
pull request's head commit — empty when there is none), and the remote's
parts: `{host}`, `{owner}`, `{repo}`, `{path}` (owner/repo, or
group/sub/repo) and `{path_escaped}` (that path as one URL-escaped
segment). `{project}` is put in as `[project] id` made it; every other
value is escaped where it lands. A query parameter left empty is dropped,
so `commit={head}` asks for nothing when there is no head.

## Steps

A step is a `call` (`"GET /path"`, `POST` or `PUT`, on `api`, with the
key) or a `fetch` (a URL found in a reply, usually `"{item}"`, from a
`follow` host over https, without the key). `each` repeats it for every
item of an earlier step's list, and their texts are put together.

What a step keeps:

- `pick` — a path into a JSON reply: `a.b.0.c`. A part may select from a
  list: `items[status=failed]` is the first item whose `status` reads
  `failed`; `items[*status=failed]` is every such item; `items[*]` is
  every item, and `[*]` alone every item of a reply that is a list. After
  a `*`, the rest of the path is read in each item and the results put in
  one list. A selector's key may reach inside with `/` (`vcs/revision`),
  its value may be a placeholder, and a placeholder that is empty matches
  every item. A pick that finds nothing ends the operation: there is no
  such run, which is an answer rather than an error.
- `text = true` — the reply is text, kept as it is.
- `lines = "PATH"` — the reply is a JSON list, and each item's PATH is a
  line of the text.

The last step is the log, so it has `text` or `lines`. conch keeps the end
of it, where the error is.

Check a file with `conch integration check FILE`, and fix what it reports
until it reports nothing.
