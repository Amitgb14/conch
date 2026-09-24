package tui

import (
	"path"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// File icons for the file explorer.
//
// Each file gets an icon by its whole name first — Dockerfile, go.mod,
// package.json — then by its extension, so .ts and .tsx can differ. The
// glyphs come in two sets, because conch runs over ssh into whatever font the
// user has: Nerd Font glyphs, which are tofu in a plain font, and a coloured
// two-letter tag that reads everywhere. [ui] icons picks one, or none.
//
// An icon never names a colour. It names a role, and the role takes a
// colour the active theme already has, so Gruvbox gets a Gruvbox file tree.

// Icon modes, the values of [ui] icons.
const (
	iconsText = "text" // the default: "" means this too
	iconsNerd = "nerd"
	iconsOff  = "off"
)

// iconModes in the order the settings screen cycles them.
var iconModes = []string{iconsText, iconsNerd, iconsOff}

// iconMode is a [ui] icons value made safe: anything unknown is text.
func iconMode(s string) string {
	switch s = strings.ToLower(strings.TrimSpace(s)); s {
	case iconsNerd, iconsOff:
		return s
	}
	return iconsText
}

// iconWidth is the column an icon takes, measured, whatever it is drawn
// with: Nerd glyphs sit in the Private Use Area, where fonts and terminals
// disagree about width, so the column is fixed rather than trusted.
const iconWidth = 2

type iconRole int

const (
	roleFile    iconRole = iota // anything without a better role
	roleWeb                     // JavaScript, TypeScript, HTML, CSS
	roleUI                      // components: React, Vue, Svelte
	roleSystems                 // compiled languages
	roleScript                  // interpreted languages and shells
	roleDocs                    // prose
	roleConfig                  // settings, manifests, lock files
	roleData                    // tables and databases
	roleMedia                   // images, audio, video, fonts
	roleArchive                 // compressed bundles
	roleBinary                  // executables and objects
	roleFolder
)

// roleColor picks the theme's colour for a role.
func roleColor(t palette, r iconRole) lipgloss.Color {
	switch r {
	case roleWeb:
		return t.work
	case roleUI, roleMedia:
		return t.merged
	case roleSystems:
		return t.ok
	case roleScript, roleData:
		return t.warn
	case roleDocs, roleFolder:
		return t.accent
	case roleArchive, roleBinary:
		return t.err
	}
	return t.muted
}

// roleStyles are the active theme's icon styles, set by applyTheme.
var roleStyles = map[iconRole]lipgloss.Style{}

func applyIconTheme(t palette) {
	for r := roleFile; r <= roleFolder; r++ {
		roleStyles[r] = lipgloss.NewStyle().Foreground(roleColor(t, r))
	}
}

// fileIcon is how one kind of file is drawn.
type fileIcon struct {
	nerd string // one Nerd Font glyph
	text string // two ASCII letters
	role iconRole
}

var (
	iconDefault    = fileIcon{"\uf15b", "  ", roleFile}
	iconFolder     = fileIcon{"\uf07b", "  ", roleFolder}
	iconFolderOpen = fileIcon{"\uf07c", "  ", roleFolder}
	iconLink       = fileIcon{"\uf0c1", "->", roleFile}
)

// iconNames are matched on the whole name, lower-cased, before extensions.
var iconNames = map[string]fileIcon{
	"dockerfile":         {"\ue7b0", "dk", roleConfig},
	"containerfile":      {"\ue7b0", "dk", roleConfig},
	"docker-compose.yml": {"\ue7b0", "dk", roleConfig},
	"compose.yaml":       {"\ue7b0", "dk", roleConfig},
	".dockerignore":      {"\ue7b0", "dk", roleConfig},
	"makefile":           {"\ue615", "mk", roleConfig},
	"justfile":           {"\ue615", "mk", roleConfig},
	"cmakelists.txt":     {"\ue615", "mk", roleConfig},
	"go.mod":             {"\ue627", "go", roleSystems},
	"go.sum":             {"\ue627", "go", roleConfig},
	"go.work":            {"\ue627", "go", roleSystems},
	"package.json":       {"\ue71e", "np", roleConfig},
	"package-lock.json":  {"\ue71e", "np", roleConfig},
	"pnpm-lock.yaml":     {"\ue71e", "np", roleConfig},
	"yarn.lock":          {"\ue71e", "np", roleConfig},
	"tsconfig.json":      {"\ue628", "ts", roleConfig},
	"cargo.toml":         {"\ue7a8", "rs", roleConfig},
	"cargo.lock":         {"\ue7a8", "rs", roleConfig},
	"gemfile":            {"\ue739", "rb", roleConfig},
	"rakefile":           {"\ue739", "rb", roleScript},
	"requirements.txt":   {"\ue73c", "py", roleConfig},
	"pyproject.toml":     {"\ue73c", "py", roleConfig},
	".gitignore":         {"\ue702", "gt", roleConfig},
	".gitattributes":     {"\ue702", "gt", roleConfig},
	".gitmodules":        {"\ue702", "gt", roleConfig},
	".editorconfig":      {"\ue615", "cf", roleConfig},
	".env":               {"\uf084", "en", roleConfig},
	"readme":             {"\uf02d", "rd", roleDocs},
	"readme.md":          {"\uf02d", "rd", roleDocs},
	"license":            {"\uf0a3", "li", roleDocs},
	"license.md":         {"\uf0a3", "li", roleDocs},
	"license.txt":        {"\uf0a3", "li", roleDocs},
	"claude.md":          {"\ue73e", "ai", roleDocs},
	"agents.md":          {"\ue73e", "ai", roleDocs},
	"gemini.md":          {"\ue73e", "ai", roleDocs},
}

// iconExts are matched on the last extension, lower-cased, dot included.
var iconExts = map[string]fileIcon{
	".go":     {"\ue627", "go", roleSystems},
	".ts":     {"\ue628", "ts", roleWeb},
	".tsx":    {"\ue7ba", "tx", roleUI},
	".js":     {"\ue74e", "js", roleWeb},
	".mjs":    {"\ue74e", "js", roleWeb},
	".cjs":    {"\ue74e", "js", roleWeb},
	".jsx":    {"\ue7ba", "jx", roleUI},
	".vue":    {"\ue6a0", "vu", roleUI},
	".svelte": {"\ue697", "sv", roleUI},
	".html":   {"\ue736", "ht", roleWeb},
	".htm":    {"\ue736", "ht", roleWeb},
	".css":    {"\ue749", "cs", roleWeb},
	".scss":   {"\ue74b", "sc", roleWeb},
	".sass":   {"\ue74b", "sc", roleWeb},
	".rs":     {"\ue7a8", "rs", roleSystems},
	".c":      {"\ue61e", "c ", roleSystems},
	".h":      {"\ue61e", "h ", roleSystems},
	".cc":     {"\ue61d", "c+", roleSystems},
	".cpp":    {"\ue61d", "c+", roleSystems},
	".hpp":    {"\ue61d", "h+", roleSystems},
	".java":   {"\ue738", "jv", roleSystems},
	".kt":     {"\ue634", "kt", roleSystems},
	".swift":  {"\ue755", "sw", roleSystems},
	".cs":     {"\ue648", "c#", roleSystems},
	".zig":    {"\ue6a9", "zg", roleSystems},
	".hs":     {"\ue777", "hs", roleSystems},
	".scala":  {"\ue737", "sl", roleSystems},
	".dart":   {"\ue798", "dt", roleSystems},
	".py":     {"\ue73c", "py", roleScript},
	".rb":     {"\ue739", "rb", roleScript},
	".php":    {"\ue73d", "ph", roleScript},
	".lua":    {"\ue620", "lu", roleScript},
	".pl":     {"\ue769", "pl", roleScript},
	".sh":     {"\ue795", "sh", roleScript},
	".bash":   {"\ue795", "sh", roleScript},
	".zsh":    {"\ue795", "sh", roleScript},
	".fish":   {"\ue795", "sh", roleScript},
	".ps1":    {"\ue795", "ps", roleScript},
	".vim":    {"\ue7c5", "vi", roleScript},
	".md":     {"\ue73e", "md", roleDocs},
	".mdx":    {"\ue73e", "mx", roleDocs},
	".txt":    {"\uf0f6", "tt", roleDocs},
	".rst":    {"\uf0f6", "rt", roleDocs},
	".pdf":    {"\uf1c1", "pd", roleDocs},
	".json":   {"\ue60b", "{}", roleConfig},
	".jsonc":  {"\ue60b", "{}", roleConfig},
	".yaml":   {"\ue615", "ym", roleConfig},
	".yml":    {"\ue615", "ym", roleConfig},
	".toml":   {"\ue615", "tm", roleConfig},
	".ini":    {"\ue615", "in", roleConfig},
	".xml":    {"\uf121", "<>", roleConfig},
	".lock":   {"\uf023", "lk", roleConfig},
	".nix":    {"\uf313", "nx", roleConfig},
	".proto":  {"\uf121", "pb", roleConfig},
	".sql":    {"\ue706", "sq", roleData},
	".db":     {"\ue706", "db", roleData},
	".csv":    {"\uf0ce", "cv", roleData},
	".png":    {"\uf1c5", "im", roleMedia},
	".jpg":    {"\uf1c5", "im", roleMedia},
	".jpeg":   {"\uf1c5", "im", roleMedia},
	".gif":    {"\uf1c5", "im", roleMedia},
	".webp":   {"\uf1c5", "im", roleMedia},
	".svg":    {"\uf1c5", "sg", roleMedia},
	".ico":    {"\uf1c5", "im", roleMedia},
	".mp3":    {"\uf1c7", "au", roleMedia},
	".wav":    {"\uf1c7", "au", roleMedia},
	".mp4":    {"\uf1c8", "vd", roleMedia},
	".mov":    {"\uf1c8", "vd", roleMedia},
	".ttf":    {"\uf031", "ft", roleMedia},
	".woff":   {"\uf031", "ft", roleMedia},
	".woff2":  {"\uf031", "ft", roleMedia},
	".zip":    {"\uf1c6", "zp", roleArchive},
	".tar":    {"\uf1c6", "zp", roleArchive},
	".gz":     {"\uf1c6", "zp", roleArchive},
	".tgz":    {"\uf1c6", "zp", roleArchive},
	".xz":     {"\uf1c6", "zp", roleArchive},
	".7z":     {"\uf1c6", "zp", roleArchive},
	".exe":    {"\uf471", "bn", roleBinary},
	".bin":    {"\uf471", "bn", roleBinary},
	".o":      {"\uf471", "bn", roleBinary},
	".so":     {"\uf471", "bn", roleBinary},
	".dylib":  {"\uf471", "bn", roleBinary},
	".a":      {"\uf471", "bn", roleBinary},
	".wasm":   {"\uf471", "wa", roleBinary},
}

// iconFor is the icon for an entry: a folder (open or not), a symlink to
// nothing, or a file by name then extension.
func iconFor(name string, dir, open, broken bool) fileIcon {
	switch {
	case dir && open:
		return iconFolderOpen
	case dir:
		return iconFolder
	case broken:
		return iconLink
	}
	lower := strings.ToLower(name)
	if ic, ok := iconNames[lower]; ok {
		return ic
	}
	switch {
	case strings.HasPrefix(lower, "dockerfile."):
		return iconNames["dockerfile"]
	case strings.HasPrefix(lower, ".env."):
		return iconNames[".env"]
	}
	if ext := path.Ext(lower); ext != "" && ext != lower {
		if ic, ok := iconExts[ext]; ok {
			return ic
		}
	}
	return iconDefault
}

// renderIcon draws ic in mode, exactly iconWidth cells wide, or "" when
// icons are off. plain leaves out the colour, for a selected row whose
// own colours would fight it.
func renderIcon(ic fileIcon, mode string, plain bool) string {
	var s string
	switch mode {
	case iconsOff:
		return ""
	case iconsNerd:
		s = ic.nerd
	default:
		s = ic.text
	}
	s = fitCells(s, iconWidth)
	if plain {
		return s
	}
	return roleStyles[ic.role].Render(s)
}

// iconSample shows a mode on a few files, for the settings screen.
func iconSample(mode string) string {
	if mode == iconsOff {
		return styleMuted.Render("names only")
	}
	var parts []string
	for _, name := range []string{"main.go", "app.ts", "app.tsx", "README.md"} {
		parts = append(parts, renderIcon(iconFor(name, false, false, false), mode, false))
	}
	return strings.Join(parts, " ")
}

// fitCells pads or cuts s to exactly n cells, by measuring it.
func fitCells(s string, n int) string {
	if w := ansi.StringWidth(s); w > n {
		s = ansi.Truncate(s, n, "")
	}
	if w := ansi.StringWidth(s); w < n {
		s += strings.Repeat(" ", n-w)
	}
	return s
}
