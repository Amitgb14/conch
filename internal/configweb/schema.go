package configweb

import (
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/keymap"
)

// The schema is what the page draws: every setting in config.toml, by its
// TOML key, with what it is for. It lives here rather than in the page so
// that a setting added to config.Config is added beside it in Go, where a
// test (TestSchemaCoversConfig) says whether one was missed.

// Kinds of field.
const (
	KindBool   = "bool"
	KindString = "string"
	KindSecret = "secret" // a string shown as dots until asked for
	KindText   = "text"   // a string of several lines, or a long command
	KindInt    = "int"
	KindFloat  = "float"
	KindSelect = "select" // one of Options; "" is allowed when Options has it
	KindList   = "list"   // a list of strings
	KindInts   = "ints"   // a list of integers
	KindMap    = "map"    // string keys to string values
	KindTables = "tables" // an array of tables, each with Fields
	KindKeyed  = "keyed"  // tables keyed by name, each with Fields
)

// Field is one setting.
type Field struct {
	Key         string   `json:"key"` // within its section's table
	Label       string   `json:"label"`
	Help        string   `json:"help,omitempty"`
	Kind        string   `json:"kind"`
	Options     []Option `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	// Default is what an absent key means, for a field that may be left
	// out (a pointer in config.Config): the page shows it, and writes the
	// key only once it is changed.
	Default any     `json:"default,omitempty"`
	Min     *int    `json:"min,omitempty"`
	Max     *int    `json:"max,omitempty"`
	Fields  []Field `json:"fields,omitempty"` // for tables and keyed
	// KeyLabel names the key of a keyed or map entry ("Provider", "Project").
	KeyLabel string   `json:"keyLabel,omitempty"`
	Suggest  []string `json:"suggest,omitempty"` // names a keyed field offers to add, or a string field offers to pick
}

// Option is one choice of a select.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Section is one table of config.toml — [ui], [notify] — or, for Key "",
// the top level, where the arrays of tables are.
type Section struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"` // the table; "" for the top level
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Icon        string  `json:"icon"` // a lucide icon name the page knows
	Fields      []Field `json:"fields"`
}

// Choices are what the select fields offer that only the caller knows:
// the TUI's themes, the agents this build has.
type Choices struct {
	// Cloud is whether Conch Cloud is enabled (config.CloudEnabled), which
	// the server works out from the file it reads: while it is not, its
	// section offers only the switch.
	Cloud  bool
	Themes []Option
	// LightThemes are the ones drawn on a light background, which is all
	// light_theme offers: it is the theme for macOS's light appearance,
	// and a dark one there turned the window dark as the system went light.
	LightThemes []Option
	Accents     []string
	Agents      []Option
	// Providers are the sandbox providers this build knows, offered as
	// tables to add; a table for one it does not is kept and shown too.
	Providers []string
	// ZshThemes are the Oh My Zsh themes installed here, offered for
	// [shell] omz_theme; any other name can still be typed.
	ZshThemes []string
}

func opts(values ...string) []Option {
	out := make([]Option, len(values))
	for i, v := range values {
		out[i] = Option{Value: v, Label: v}
	}
	return out
}

func intp(n int) *int { return &n }

// keyActions names every action a key after the prefix can do, for
// [keys.bind]'s list to offer rather than have them typed from memory.
func keyActions() []string {
	acts := keymap.Actions()
	out := make([]string, len(acts))
	for i, a := range acts {
		out[i] = string(a)
	}
	return out
}

// Schema is every section, in the order the page lists them.
func Schema(c Choices) []Section {
	themes := c.Themes
	if len(themes) == 0 {
		themes = opts("conch")
	}
	accents := append([]Option{{Value: "", Label: "The theme's own"}}, opts(c.Accents...)...)
	agents := c.Agents
	if len(agents) == 0 {
		agents = opts("claude", "codex", "gemini", "opencode")
	}
	reviewers := append([]Option{{Value: "", Label: "Nobody"}}, agents...)

	provider := []Field{
		{Key: "api_key", Label: "API key", Kind: KindSecret,
			Help: "The key itself, when you would rather keep it here than in your environment. config.toml is written 0600, but anything running as you can read it. Empty uses the variable below."},
		{Key: "api_key_env", Label: "API key variable", Kind: KindString, Placeholder: "the provider's own, e.g. DAYTONA_API_KEY",
			Help: "The environment variable holding the key. A key set above wins over it."},
		{Key: "api_url", Label: "API endpoint", Kind: KindString, Placeholder: "the provider's own",
			Help: "Overrides the provider's API address: its base, e.g. https://api.tensorlake.ai — not a path under it such as …/sandboxes."},
		{Key: "target", Label: "Region", Kind: KindString, Placeholder: "the account default", Help: "e.g. us or eu."},
		{Key: "snapshot", Label: "Snapshot", Kind: KindString, Placeholder: "the provider's own", Help: "The image new sandboxes start from."},
		{Key: "auto_stop", Label: "Provider idle timer (minutes)", Kind: KindInt, Min: intp(0),
			Help: "The provider's own idle timer; 0 turns it off. Providers only see traffic from outside, so a quietly working agent looks idle to them."},
		{Key: "idle_stop", Label: "conch stops it after idle (minutes)", Kind: KindInt, Min: intp(0), Default: config.IdleStopDefault,
			Help: "Minutes without an agent working or a pane printing before conch stops the sandbox, keeping its files. 0 never stops one."},
		{Key: "restore_running", Label: "Offer back what was running", Kind: KindBool, Default: true,
			Help: "Write down what a sandbox was running when it stops, and offer it back when it starts again."},
		{Key: "price_cpu_hour", Label: "Price per CPU hour", Kind: KindFloat, Min: intp(0), Help: "From the provider's pricing page; nothing is shown until a price is set."},
		{Key: "price_gib_hour", Label: "Price per GiB of memory hour", Kind: KindFloat, Min: intp(0)},
		{Key: "price_disk_gib_hour", Label: "Price per GiB of disk hour", Kind: KindFloat, Min: intp(0), Help: "Charged while a sandbox is stopped, too."},
		{Key: "env", Label: "Variables passed in", Kind: KindList, Placeholder: "CLAUDE_CODE_OAUTH_TOKEN",
			Help: "Variables of this environment given to new sandboxes; the provider keeps their values with the sandbox."},
	}

	return []Section{
		{ID: "ui", Key: "ui", Title: "Appearance", Icon: "palette", Description: "Theme, mouse and how the TUI draws things.",
			Fields: []Field{
				{Key: "theme", Label: "Theme", Kind: KindSelect, Options: themes,
					Help: "The TUI's colours, and Conch.app's — in dark appearance only, when a light theme is set below."},
				{Key: "light_theme", Label: "Light theme", Kind: KindSelect,
					Options: append([]Option{{Value: "", Label: "Off"}}, c.LightThemes...),
					Help:    "Conch.app follows macOS: this theme in light appearance, the one above in dark, switching as the system does. Off keeps the app on the theme above. The TUI always uses the theme above."},
				{Key: "accent", Label: "Accent", Kind: KindSelect, Options: accents,
					Help: "Overrides the theme's accent: selections, focused borders, dialogs. A #rrggbb value works too, in the raw file."},
				{Key: "mouse", Label: "Mouse", Kind: KindBool, Help: "Let conch handle clicks, the wheel and drags. Hold shift (option in iTerm2) to select with the terminal instead."},
				{Key: "hover", Label: "Hover", Kind: KindBool, Help: "Light the row, tab or button under the pointer. A message per cell crossed, which a slow ssh link feels."},
				{Key: "cost", Label: "Show cost", Kind: KindBool, Help: "What agents have spent, on tree rows, Sessions and the project and machine pages."},
				{Key: "diff", Label: "Diff layout", Kind: KindSelect, Options: []Option{{"", "Auto, by width"}, {"side", "Side by side"}, {"unified", "Unified"}}},
				{Key: "icons", Label: "File icons", Kind: KindSelect, Options: []Option{{"", "Letters, in any font"}, {"nerd", "Nerd Font glyphs"}, {"off", "None"}}},
				{Key: "tree_groups", Label: "Group a project's panes by", Kind: KindSelect, Options: []Option{{"", "Sections: Agents and Terminals"}, {"tabs", "The tab each is open in"}}},
			}},
		{ID: "keys", Key: "keys", Title: "Keys", Icon: "keyboard", Description: "The key that hands control back to conch, and what the keys after it do.",
			Fields: []Field{
				{Key: "prefix", Label: "Prefix", Kind: KindString, Placeholder: "ctrl+b", Help: "Bubble Tea key notation, e.g. ctrl+b or ctrl+a. Takes effect when the TUI next starts."},
				{Key: "bind", Label: "Rebind a key after the prefix", Kind: KindMap, KeyLabel: "What it does", Placeholder: "\\",
					Help: "As tmux's bind-key: the name of what the key does (split_right, next_tab, zoom — `conch keys` lists them all) and the key to put it on. tmux's spelling is taken too (C-n, Space). An empty key unbinds it; what is not named here keeps its default.", Suggest: keyActions()},
			}},
		{ID: "notify", Key: "notify", Title: "Notifications", Icon: "bell", Description: "How conch tells you an agent needs you.",
			Fields: []Field{
				{Key: "enabled", Label: "Notifications", Kind: KindBool, Help: "The master switch."},
				{Key: "desktop", Label: "Desktop notification", Kind: KindBool},
				{Key: "sound", Label: "Sound", Kind: KindBool},
				{Key: "bell", Label: "Terminal bell", Kind: KindBool},
				{Key: "waiting", Label: "When an agent needs an answer", Kind: KindBool},
				{Key: "done", Label: "When an agent finishes", Kind: KindBool},
				{Key: "limits", Label: "Plan limits", Kind: KindBool, Help: "When Claude's or Codex's limit window passes a percentage below."},
				{Key: "limit_at", Label: "Limit alerts at (%)", Kind: KindInts, Placeholder: "80, 95"},
				{Key: "quiet_start", Label: "Quiet from", Kind: KindString, Placeholder: "22:00", Help: "Alerts are silenced every day between these; waiting agents still show."},
				{Key: "quiet_end", Label: "Quiet until", Kind: KindString, Placeholder: "08:00"},
				{Key: "silence", Label: "Quiet after (seconds)", Kind: KindInt, Min: intp(1), Help: "Seconds without output that count as quiet, for a pane watched with ctrl+b M."},
				{Key: "command", Label: "Command for every alert", Kind: KindText, Placeholder: "curl -d \"$CONCH_NOTIFY_BODY\" ntfy.sh/my-topic",
					Help: "Run in sh with the alert as JSON on stdin and in CONCH_NOTIFY_TITLE and CONCH_NOTIFY_BODY."},
			}},
		{ID: "agents", Key: "agents", Title: "Agents", Icon: "bot", Description: "Which agent c starts, and how permission rules are used.",
			Fields: []Field{
				{Key: "default", Label: "Default agent", Kind: KindSelect, Options: agents},
				{Key: "permission_mode", Label: "Permission rules", Kind: KindSelect,
					Options: []Option{{"", "On: answer by them"}, {config.PermissionsDryRun, "Dry run: only record"}, {config.PermissionsOff, "Off"}}},
				{Key: "unrestricted_launch", Label: "Let agents start agents however they like", Kind: KindBool,
					Help: "Off: an agent driving conch picks which agent to start and its --model, nothing else, and types only into agents, never a shell. On: any command, environment and flags, a helper that asks nobody before it acts among them, and typing into shells."},
			}},
		{ID: "verify", Key: "verify", Title: "Checks & autopilot", Icon: "shield-check", Description: "What runs when an agent finishes, and what conch does with the result.",
			Fields: []Field{
				{Key: "commands", Label: "Check command per project", Kind: KindMap, KeyLabel: "Project ID", Placeholder: "go test ./...",
					Help: "Run in the branch's worktree when its agent finishes. Nothing runs until you name one."},
				{Key: "auto_fix", Label: "Send a failed check back (times)", Kind: KindInt, Min: intp(0), Help: "0 never does: f in the review queue sends it when you choose."},
				{Key: "auto_pr", Label: "Open a pull request when it passes", Kind: KindBool, Help: "Publishes the agent's work."},
				{Key: "auto_merge", Label: "Merge when CI is green", Kind: KindBool, Help: "Writes to the base branch. Held back for drafts, conflicts and requested changes."},
				{Key: "auto_resolve", Label: "Resolve conflicts (times)", Kind: KindInt, Min: intp(0)},
				{Key: "require_tests", Label: "Draft a PR that changes code and no test", Kind: KindBool},
				{Key: "max_diff", Label: "Draft past this many changed lines", Kind: KindInt, Min: intp(0), Help: "0: no limit."},
				{Key: "reviewer", Label: "Reviewer agent", Kind: KindSelect, Options: reviewers, Help: "Asked to read a branch's diff and report. Every review spends plan usage."},
			}},
		{ID: "git", Key: "git", Title: "Git", Icon: "git-merge", Description: "How conch merges, and what a finished branch goes through on its way to the base.",
			Fields: []Field{
				{Key: "squash", Label: "Squash when merging", Kind: KindBool, Help: "One commit on the base instead of a merge commit."},
			}},
		{ID: "brain", Key: "brain", Title: "Brain", Icon: "sparkles", Description: "The model behind ✦ Ask and agent summaries.",
			Fields: []Field{
				{Key: "provider", Label: "Provider", Kind: KindSelect, Options: []Option{{"claude", "Claude Code CLI"}, {"anthropic", "Anthropic API"}, {"openai", "OpenAI-compatible"}}},
				{Key: "model", Label: "Model", Kind: KindString, Placeholder: "the provider's default"},
				{Key: "summary_model", Label: "Summary model", Kind: KindString, Placeholder: "a small, fast model"},
				{Key: "summaries", Label: "Summarise agents on their own", Kind: KindBool, Help: "Each summary is a (small) model request."},
				{Key: "base_url", Label: "API endpoint", Kind: KindString, Placeholder: "https://api.openai.com/v1", Help: "For anthropic and openai: Ollama, LM Studio and the like."},
				{Key: "api_key_env", Label: "API key variable", Kind: KindString, Placeholder: "ANTHROPIC_API_KEY"},
				{Key: "command", Label: "claude binary", Kind: KindString, Placeholder: "found on PATH"},
				{Key: "no_fast_path", Label: "Always ask the model", Kind: KindBool, Help: "Otherwise show, close, tell one agent or everyone, and what is waiting are planned at once, without a model call."},
			}},
		{ID: "shell", Key: "shell", Title: "Terminals", Icon: "terminal", Description: "New terminal panes.",
			Fields: []Field{
				{Key: "omz_theme", Label: "Oh My Zsh theme", Kind: KindString, Placeholder: "the one .zshrc chooses", Help: "Applied after your own .zshrc in zsh terminals — the ones already open too, redrawn as it is saved.", Suggest: c.ZshThemes},
			}},
		{ID: "remote", Key: "remote", Title: "Remote machines", Icon: "server", Description: "Panes on machines reached over ssh.",
			Fields: []Field{
				{Key: "upload_drops", Label: "Upload dropped files", Kind: KindBool, Help: "Copy local files dropped into a remote pane to that machine and paste their paths there."},
				{Key: "upload_max_mb", Label: "Largest upload (MB)", Kind: KindInt, Min: intp(0), Help: "0 is the default, 25."},
				{Key: "no_compression", Label: "Don't compress ssh", Kind: KindBool, Help: "Only worth it where the link is as fast as the processor. Applies to new connections."},
			}},
		{ID: "update", Key: "update", Title: "Updates", Icon: "refresh-cw", Description: "How conch looks for newer builds.",
			Fields: []Field{
				{Key: "check_releases", Label: "Check for releases daily", Kind: KindBool},
				{Key: "auto", Label: "Install them on their own", Kind: KindBool, Help: "Needs the daily check. conch update rollback goes back."},
			}},
		{ID: "web", Key: "web", Title: "Phone (conch web)", Icon: "smartphone", Description: "Your agents from your phone, over Tailscale or a relay.",
			Fields: []Field{
				{Key: "url", Label: "Address phones open", Kind: KindString, Placeholder: "https://laptop.tail1234.ts.net"},
				{Key: "port", Label: "Port", Kind: KindInt, Min: intp(0), Max: intp(65535), Placeholder: "8722"},
				{Key: "relay", Label: "Relay", Kind: KindString, Placeholder: "wss://relay.example.com",
					Help: "A relay conch web connects out to, so a phone reaches this computer from any network without Tailscale. " +
						"Run one with `conch relay` on a server of your own. Phone and computer encrypt everything with a key from the pairing QR code, but the relay serves the phone's app, so use only a relay you trust as you trust this computer."},
			}},
		cloudSection(c.Cloud),
		{ID: "app", Key: "app", Title: "Mac app", Icon: "laptop", Description: "How Conch.app draws its window. The TUI keeps its own.",
			Fields: []Field{
				{Key: "tabs", Label: "Tabs", Kind: KindSelect, Options: []Option{{"", "Above the panes"}, {"left", "Left, grouped by machine"}, {"right", "Right"}},
					Help: "On the left, tabs sit in the sidebar under each machine, the tree below them."},
				{Key: "density", Label: "Density", Kind: KindSelect, Options: []Option{{"", "Standard"}, {"compact", "Compact"}},
					Help: "Compact puts a pane's name in its border, closes the gaps between splits and shortens the toolbar."},
				{Key: "sidebar", Label: "Sidebar", Kind: KindSelect, Options: []Option{{"", "Solid"}, {"glass", "Glass"}},
					Help: "Glass lets the desktop through the sidebar, as Finder's does. Panes stay solid."},
				{Key: "ports", Label: "Ports in pane headers", Kind: KindBool, Default: true,
					Help: "The ports a pane's programs listen on, a click opening one in the browser."},
				{Key: "scratch_key", Label: "Option-space anywhere", Kind: KindBool,
					Help: "Show the scratch layer with Conch.app in the background, as Ghostty's quick terminal does. Off, option-space works in the window only."},
				{Key: "prefix_keys", Label: "Prefix keys in panes", Kind: KindBool, Default: true,
					Help: "The TUI's prefix (ctrl+b) and the keys after it, from [keys], work in the app's panes too. Off, the prefix goes to the program."},
				{Key: "text_size", Label: "Window text size", Kind: KindInt, Placeholder: "100",
					Help: "Percent, for the window's own text — the sidebar, tabs, the git tab, the status bar — 80 to 160; empty is 100. Shift-command-plus and minus step it: 90, 110, 125, 140."},
				{Key: "icons", Label: "Icon set", Kind: KindSelect, Options: []Option{{"", "Lucide"}, {"sf", "SF Symbols"}, {"sf-fill", "SF Symbols Fill"}},
					Help: "The pictures beside each section of Settings and each row of the window's sidebar: Lucide's outlines, or the Mac's own SF Symbols, line or filled. In the app, hover one to try it; click to choose."},
				{Key: "icon_style", Label: "Style", Kind: KindSelect, Options: []Option{{"", "Plain"}, {"tinted", "Tinted"}, {"tiles", "Tiles"}},
					Help: "Plain follows the text colour; Tinted gives each section a colour; Tiles puts it on a coloured square, as System Settings does."},
				{Key: "font_size", Label: "Terminal font size", Kind: KindFloat, Placeholder: "14",
					Help: "Points, for the text in every pane, 9 to 28; empty is 14. Command-plus and minus change it too."},
			}},
		{ID: "sandbox", Key: "sandbox", Title: "Sandboxes", Icon: "box", Description: "Hosted sandbox providers, one table each.",
			Fields: []Field{
				{Key: "", Label: "Providers", Kind: KindKeyed, KeyLabel: "Provider", Fields: provider, Suggest: c.Providers},
			}},
		{ID: "actions", Key: "", Title: "Actions", Icon: "zap", Description: "Commands of your own on the row menus (m, or right-click).",
			Fields: []Field{
				{Key: "actions", Label: "Actions", Kind: KindTables, Fields: []Field{
					{Key: "name", Label: "Name", Kind: KindString, Placeholder: "Run tests"},
					{Key: "run", Label: "Command", Kind: KindText, Placeholder: "go test ./...", Help: "Whatever sh -lc takes. CONCH_MACHINE, CONCH_PROJECT, CONCH_BRANCH, CONCH_WORKTREE and CONCH_PANE are set."},
					{Key: "on", Label: "Offered on", Kind: KindList, Options: opts(config.ActionPlaces...), Help: "None is every kind of row."},
					{Key: "keep", Label: "Keep the terminal open after", Kind: KindBool, Default: true},
				}},
			}},
		{ID: "permissions", Key: "", Title: "Permission rules", Icon: "lock", Description: "Answer an agent's tool requests by rule; the first that matches decides.",
			Fields: []Field{
				{Key: "permissions", Label: "Rules", Kind: KindTables, Fields: []Field{
					{Key: "tool", Label: "Tool", Kind: KindString, Placeholder: "Bash", Help: "As the agent names it: Bash, Edit, mcp__github__create_issue. * and ? work."},
					{Key: "match", Label: "Matching", Kind: KindString, Placeholder: "go test *", Help: "The command, file or URL; * for anything. Empty is anything."},
					{Key: "action", Label: "Answer", Kind: KindSelect, Options: opts("allow", "deny")},
					{Key: "agent", Label: "Agent", Kind: KindSelect, Options: append([]Option{{Value: "", Label: "Every agent"}}, agents...)},
					{Key: "project", Label: "Project", Kind: KindString, Placeholder: "every project", Help: "By name or path."},
				}},
			}},
	}
}

// cloudSection is [cloud]. Conch Cloud is hidden in this release, so
// while it is off the section is only the switch that turns it on; its
// server is shown once it is on. A hidden key is still in the page's
// copy of the file, so saving keeps it.
func cloudSection(on bool) Section {
	enabled := Field{Key: "enabled", Label: "Enabled", Kind: KindBool,
		Help: "Offers Conch Cloud: conch login, cloud machines, and moving a project to one. Not enabled in this release; CONCH_CLOUD=1 or 0 in the environment overrides this."}
	if !on {
		return Section{ID: "cloud", Key: "cloud", Title: "Conch Cloud", Icon: "server",
			Description: "Not enabled in this release. Turn it on to try it.", Fields: []Field{enabled}}
	}
	return Section{ID: "cloud", Key: "cloud", Title: "Conch Cloud", Icon: "server", Description: "Cloud machines that keep your agents working with the laptop closed.",
		Fields: []Field{
			enabled,
			{Key: "api", Label: "Server", Kind: KindString, Placeholder: "https://api.cloud.conch.dev",
				Help: "The Conch Cloud server conch login signs in to. Empty is Conch Cloud itself; CONCH_CLOUD_URL overrides it. Takes effect at the next sign-in."},
		}}
}
