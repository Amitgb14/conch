# conch provider file

A provider file tells conch how to use a sandbox provider's HTTP API: which
call makes a sandbox, looks at one, lists them, starts, stops and deletes
one, how the provider's states read, and how ssh gets in. conch reaches
every sandbox over ssh, so **a provider with no ssh way in cannot be
described** — if that is the case, write `NO_SSH.txt` saying so and why,
and no provider file.

It is TOML. Every call is `"METHOD /path"` under `api`; `{id}` in a path is
the sandbox's id. A body is a TOML table sent as JSON; a string value that
is exactly one placeholder takes that value's own type (a number stays a
number) and is left out when there is nothing to put there, so the
provider's default applies. Paths into a reply are dotted: `data.state`,
`items.0.id`.

```toml
name = "acme"                 # lowercase letters, digits, -; the file is NAME.toml
label = "Acme"                # how people write it
api = "https://api.acme.dev"  # https
key_env = "ACME_API_KEY"      # the variable the API key is read from
auth_header = "Authorization" # default
auth_value = "Bearer {key}"   # default; must contain {key}
docs = "https://docs.acme.dev/api"  # where you read it
adapted_by = "claude"         # who wrote this file
adapted = "2026-11-08"
note = ""                     # anything a person must know before making one

[create]
call = "POST /sandboxes"
# {name} is required: conch names every sandbox it makes, and finds its
# own by that name. Others: {label} {cpu} {memory_gib} {memory_mb}
# {disk_gib} {disk_mb} {snapshot} (an image or snapshot to start from)
# {env} (an object of variables to set inside, if the API takes them)
# {auto_stop_secs}
body = { name = "{name}", cpus = "{cpu}", memory_mb = "{memory_mb}", image = "{snapshot}" }
id = "id"                     # where the new sandbox's id is in the reply

[get]
call = "GET /sandboxes/{id}"
state = "status"              # required
name = "name"                 # required
reason = "error"              # optional: why it failed
cpu = "resources.cpus"        # optional
memory_mb = "resources.memory_mb"  # optional
disk_mb = "resources.disk_mb"      # optional
created = "created_at"        # optional: RFC 3339, or Unix seconds or milliseconds

[list]
call = "GET /sandboxes"
items = "sandboxes"           # where the list is; each item is read as [get] reads
id = "id"                     # each item's id
next = "next_cursor"          # optional: the next page's cursor…
cursor_param = "cursor"       # …sent back as this query parameter
mine = "conch-"               # the start of every name conch gives

[start]                       # bring a stopped one back
call = "POST /sandboxes/{id}/start"
[stop]                        # stop it, keeping its disk
call = "POST /sandboxes/{id}/stop"
[delete]
call = "DELETE /sandboxes/{id}"

[states]                      # every state the provider documents → conch's:
running = "started"           # starting started stopping stopped error gone
pending = "starting"
stopped = "stopped"
deleted = "gone"
failed = "error"

[ssh]
host = "ssh.acme.dev"         # may use {id} {name}, or {reply.PATH} after [ssh.access]
user = "{id}"
port = 22                     # 0 or 22: ssh's default
# Whose key opens it:
#   "account"   keys the person registers with their account there (say how in note)
#   "authorize" conch hands its public key over with [ssh.authorize]
#   "none"      a credential handed out per connection, read from [ssh.access]
key = "account"

# [ssh.authorize]
# call = "POST /sandboxes/{id}/ssh-keys"
# body = { public_key = "{public_key}" }

# [ssh.access]                # asked before each connection
# call = "POST /sandboxes/{id}/ssh-access"
# then e.g. user = "{reply.token}", host = "{reply.host}"
```

## Format 2

Three things the example does not need, for APIs shaped otherwise. A file
that uses any of them starts with `format = 2`; a conch too old to read
them then refuses the file rather than misreading it.

- **A wrapped reply.** When getting one sandbox answers `{"server": {...}}`,
  say `item = "server"` under `[get]`; the other `[get]` paths are then
  inside it, and a list's items are read with the same paths, unwrapped.
- **`{public_key}` in the create body.** For a provider with no key
  registry of its own, conch's public key can go into the sandbox as it is
  made, alone or inside a longer string — cloud-init, say:
  `user_data = "#cloud-config\nssh_authorized_keys:\n  - {public_key}\n"`,
  with `key = "account"`, since the key conch offers is that one.
- **A selector in a path.** `networks.v4[type=public].ip_address` is the
  first item of the list `networks.v4` whose `type` reads `public`. A
  selector's value cannot contain a dot. With `[ssh.access]` asking for the
  sandbox itself, `host = "{reply.droplet.networks.v4[type=public].ip_address}"`
  reaches a sandbox at its own address.

Check it with `conch sandbox provider check -offline ./provider.toml`, and
fix what it reports until it reports nothing.
