package configweb

import "github.com/Amitgb14/conch/internal/config"

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
	Suggest  []string `json:"suggest,omitempty"` // names a keyed field offers to add
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
	Themes  []Option
	Accents []string
	Agents  []Option
	// Providers are the sandbox providers this build knows, offered as
	// tables to add; a table for one it does not is kept and shown too.
	Providers []string
}

func opts(values ...string) []Option {
	out := make([]Option, len(values))
	for i, v := range values {
		out[i] = Option{Value: v, Label: v}
	}
	return out
}

func intp(n int) *int { return &n }

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
			Help: "Overrides the provider's API address."},
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
				{Key: "theme", Label: "Theme", Kind: KindSelect, Options: themes},
				{Key: "accent", Label: "Accent", Kind: KindSelect, Options: accents,
					Help: "Overrides the theme's accent: selections, focused borders, dialogs. A #rrggbb value works too, in the raw file."},
				{Key: "mouse", Label: "Mouse", Kind: KindBool, Help: "Let conch handle clicks, the wheel and drags. Hold shift (option in iTerm2) to select with the terminal instead."},
				{Key: "hover", Label: "Hover", Kind: KindBool, Help: "Light the row, tab or button under the pointer. A message per cell crossed, which a slow ssh link feels."},
				{Key: "cost", Label: "Show cost", Kind: KindBool, Help: "What agents have spent, on tree rows, Sessions and the project and machine pages."},
				{Key: "diff", Label: "Diff layout", Kind: KindSelect, Options: []Option{{"", "Auto, by width"}, {"side", "Side by side"}, {"unified", "Unified"}}},
				{Key: "icons", Label: "File icons", Kind: KindSelect, Options: []Option{{"", "Letters, in any font"}, {"nerd", "Nerd Font glyphs"}, {"off", "None"}}},
				{Key: "tree_groups", Label: "Group a project's panes by", Kind: KindSelect, Options: []Option{{"", "Sections: Agents and Terminals"}, {"tabs", "The tab each is open in"}}},
			}},
		{ID: "keys", Key: "keys", Title: "Keys", Icon: "keyboard", Description: "The key that hands control back to conch.",
			Fields: []Field{
				{Key: "prefix", Label: "Prefix", Kind: KindString, Placeholder: "ctrl+b", Help: "Bubble Tea key notation, e.g. ctrl+b or ctrl+a. Takes effect when the TUI next starts."},
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
		{ID: "git", Key: "git", Title: "Git", Icon: "git-merge", Description: "How conch's own git actions behave.",
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
			}},
		{ID: "shell", Key: "shell", Title: "Terminals", Icon: "terminal", Description: "New terminal panes.",
			Fields: []Field{
				{Key: "omz_theme", Label: "Oh My Zsh theme", Kind: KindString, Placeholder: "the one .zshrc chooses", Help: "Applied after your own .zshrc in new zsh terminals."},
			}},
		{ID: "pane", Key: "pane", Title: "Panes", Icon: "square-terminal", Description: "Defaults for new panes.",
			Fields: []Field{
				{Key: "default_command", Label: "Default command", Kind: KindString, Placeholder: "$SHELL"},
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
		{ID: "web", Key: "web", Title: "Phone (conch web)", Icon: "smartphone", Description: "Your agents from your phone, over Tailscale.",
			Fields: []Field{
				{Key: "url", Label: "Address phones open", Kind: KindString, Placeholder: "https://laptop.tail1234.ts.net"},
				{Key: "port", Label: "Port", Kind: KindInt, Min: intp(0), Max: intp(65535), Placeholder: "8722"},
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
