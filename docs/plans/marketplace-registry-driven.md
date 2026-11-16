# The catalog drawn from the registry

Status: proposal, 2026-11-16. Nothing here is built yet.

## Why

Conch.app now draws each integration's logo from a table compiled into the
app (`macos/Sources/ConchKit/IntegrationLogos.swift`), and its kinds, their
names and blurbs ("Sandboxes — machines in the cloud for agents") are in
the app too. So adding an integration to the registry gives it a letter
instead of a logo until somebody ships a new Conch.app, and a new kind
cannot be shown at all. The registry (github.com/Amitgb14/conch-integrations)
should say everything the catalog shows; the app and the CLI only draw it.

What stays in conch: what each kind *does* (code), and the agents' own
logos (`AgentLogos`, which are not integrations).

## What the registry has to do

### 1. An icon in every integration's folder

```
sandbox/boat/
  provider.toml
  meta.json
  icon.svg          ← new
ci/circleci/
  integration.toml
  meta.json
  icon.svg          ← new
```

`icon.svg` rules, checked by `scripts/registry.py check` (CI fails otherwise):

- `viewBox="0 0 24 24"`, one colour, only `<path d="…">` elements (no
  `<script>`, `<style>`, `<image>`, `<use>`, `href`, transforms, gradients,
  text). It is drawn white on the brand colour, the same way for every
  integration, so it cannot look like something else's.
- at most 8 KB.
- `registry.py icon fit FILE.svg` turns a service's own icon (any viewBox,
  translated or scaled) into that form: what I did by hand for boat.dev,
  Daytona, Tensorlake and InstaBox.
- where it came from goes in `meta.json` (`"icon_source"`), and the README
  says logos are their owners' trademarks, shown to name the service.

`meta.json` gains:

```json
{ "color": "263FA9", "icon_source": "https://boat.dev/icon.svg" }
```

### 2. Built-ins get folders too

conch's own integrations (GitHub, GitHub Actions, GitHub Issues, Linear,
Jira, and the sandboxes boat.dev, Devin Cloud, Daytona, Tensorlake) are in
`builtin.json` today with no icon. They get `builtin/NAME/{meta.json,icon.svg}`
and nothing to install, so every card is drawn the same way.

### 3. The kinds, in the index

`kinds.json` at the root, copied into the index:

```json
[
  {"kind": "host",    "label": "Code hosts",      "about": "where the code and its pull requests live"},
  {"kind": "ci",      "label": "CI",              "about": "what runs a branch's checks"},
  {"kind": "tickets", "label": "Ticket trackers", "about": "where the work is written down"},
  {"kind": "sandbox", "label": "Sandboxes",       "about": "machines in the cloud for agents"},
  {"kind": "notify",  "label": "Notifications",   "about": "where conch says an agent is waiting"}
]
```

The order is the sidebar's order. A kind this conch has no code for is still
listed, its cards marked *needs a newer conch* and not installable.

### 4. The index carries all of it

`index.json` (format 2, so older conches still read it — both Go and Swift
ignore fields they don't know) gains `kinds`, and each entry an `icon`:

```json
{"kind": "sandbox", "name": "boat", "label": "boat.dev", "builtin": true,
 "icon": {"path": "M23.566 18.332…", "color": "263FA9"}, …}
```

The icon is **inline**, not a link: one fetch, no extra request per card,
and `index.json.sig` covers it, so a signed index vouches for its icons
too. Sixteen icons add roughly 30 KB.

## What conch has to do

- **C1 (Go, `internal/integration`):**
  - `Entry.Icon` and `Index.Kinds`.
  - Keep the last good index in `$CONCH_HOME/integrations/index.json`, so the
    catalog has its icons offline and on first draw.
  - `conch integration search/info/ls -json` give each row its `icon`,
    `label` and `kind`. An installed one with nothing in the index keeps the
    icon its install record saved.
  - Tests: an icon that breaks the rules is dropped, not drawn.
- **C2 (Conch.app):**
  - Draw `IntegrationMark` from the row's icon. Take the sidebar's kinds,
    names and blurbs from `kinds`.
  - Delete `IntegrationLogos.swift`'s table. With no icon, a card shows its
    initial, as now.
- **C3 (docs):** add "an icon.svg, and how to fit one" to the registry
  README and `format.md`, and say where the catalog comes from on the
  Integrations docs page.

## Order

1. R1, the registry: icons for its 16 entries and 10 built-ins, `kinds.json`,
   `registry.py check` and `build`, `icon fit`. One PR there.
2. C1 then C2 here, with C3 alongside. An app built before R1 merges keeps
   its letters, so the order is safe either way.

## Open questions for the owner

1. Inline icons in the index (proposed), or a separate `icon.svg` per
   entry fetched and checked against a hash?
2. Remove the compiled-in logo table entirely (proposed), or keep it as the
   fallback before the first fetch? The cached index covers that case
   after the first run.
3. Should `kinds` be able to add a kind conch has no code for, shown as
   "needs a newer conch", or only rename and reorder the five there are?
