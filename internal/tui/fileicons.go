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
//
// The glyphs follow the Nerd Fonts glyph list and nvim-web-devicons, so a
// file looks here the way it looks in the editors people also have open.
// Every one is in the Private Use Area of the BMP (U+E000–U+F8FF): the
// Material range that Nerd Fonts v3 moved to U+F0000 is avoided, since a
// v2 font draws nothing there, and a glyph that font has not got is worse
// than the plain two letters. Where the canonical icon is only in that
// range — a spreadsheet, a log — the nearest BMP glyph stands in.

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
	"cmakelists.txt":     {"\ue794", "cm", roleConfig},
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

	// Tooling that lives in a dot file
	".npmrc":                  {"\ue71e", "np", roleConfig},
	".npmignore":              {"\ue71e", "np", roleConfig},
	".nvmrc":                  {"\ue718", "nd", roleConfig},
	".node-version":           {"\ue718", "nd", roleConfig},
	".babelrc":                {"\ue639", "ba", roleConfig},
	".prettierrc":             {"\ue6b4", "pr", roleConfig},
	".prettierrc.json":        {"\ue6b4", "pr", roleConfig},
	".prettierignore":         {"\ue6b4", "pr", roleConfig},
	".eslintrc":               {"\ue655", "es", roleConfig},
	".eslintrc.json":          {"\ue655", "es", roleConfig},
	".eslintrc.js":            {"\ue655", "es", roleConfig},
	".eslintignore":           {"\ue655", "es", roleConfig},
	".clang-format":           {"\ue823", "cl", roleConfig},
	".clang-tidy":             {"\ue823", "cl", roleConfig},
	".pre-commit-config.yaml": {"\ue615", "pc", roleConfig},
	".python-version":         {"\ue73c", "py", roleConfig},
	".ruby-version":           {"\ue739", "rb", roleConfig},
	".tool-versions":          {"\ue615", "tv", roleConfig},
	".gitlab-ci.yml":          {"\uf296", "gl", roleConfig},
	".gitconfig":              {"\ue702", "gt", roleConfig},
	".mailmap":                {"\ue702", "gt", roleConfig},
	".gitkeep":                {"\ue702", "gt", roleConfig},
	".keep":                   {"\ue702", "gt", roleConfig},
	".terraform.lock.hcl":     {"\ue69a", "tf", roleConfig},
	".ds_store":               {"\ue615", "ds", roleFile},

	// Shell start-up files
	".bashrc":       {"\ue795", "sh", roleConfig},
	".bash_profile": {"\ue795", "sh", roleConfig},
	".zshrc":        {"\ue795", "sh", roleConfig},
	".zshenv":       {"\ue795", "sh", roleConfig},
	".zprofile":     {"\ue795", "sh", roleConfig},
	".profile":      {"\ue795", "sh", roleConfig},
	".vimrc":        {"\ue62b", "vi", roleConfig},

	// Manifests and lock files a project is built from
	"pom.xml":          {"\ue674", "mv", roleConfig},
	"build.gradle":     {"\ue660", "gd", roleConfig},
	"build.gradle.kts": {"\ue660", "gd", roleConfig},
	"settings.gradle":  {"\ue660", "gd", roleConfig},
	"gradlew":          {"\ue660", "gd", roleConfig},
	"jsconfig.json":    {"\ue74e", "js", roleConfig},
	"bun.lockb":        {"\ue76f", "bu", roleConfig},
	"bun.lock":         {"\ue76f", "bu", roleConfig},
	"composer.json":    {"\ue73d", "ph", roleConfig},
	"composer.lock":    {"\ue73d", "ph", roleConfig},
	"poetry.lock":      {"\ue73c", "py", roleConfig},
	"uv.lock":          {"\ue73c", "py", roleConfig},
	"setup.py":         {"\ue73c", "py", roleConfig},
	"setup.cfg":        {"\ue73c", "py", roleConfig},
	"gemfile.lock":     {"\ue739", "rb", roleConfig},
	"mix.exs":          {"\ue62d", "ex", roleConfig},
	"mix.lock":         {"\ue62d", "ex", roleConfig},
	"jenkinsfile":      {"\uf2ec", "jk", roleConfig},
	"vagrantfile":      {"\ue615", "vg", roleConfig},
	"procfile":         {"\ue615", "pf", roleConfig},
	"brewfile":         {"\uf0fc", "bw", roleConfig},
	"codeowners":       {"\ue702", "co", roleConfig},

	// Prose a repository keeps
	"changelog":          {"\uf1da", "ch", roleDocs},
	"changelog.md":       {"\uf1da", "ch", roleDocs},
	"contributing.md":    {"\uf0c0", "cb", roleDocs},
	"code_of_conduct.md": {"\uf0c0", "cb", roleDocs},
	"readme.rst":         {"\uf02d", "rd", roleDocs},
	"readme.txt":         {"\uf02d", "rd", roleDocs},
}

// iconExts are matched on the last extension, lower-cased, dot included.
var iconExts = map[string]fileIcon{
	// Web and UI
	".ts":       {"\ue628", "ts", roleWeb},
	".mts":      {"\ue628", "ts", roleWeb},
	".cts":      {"\ue628", "ts", roleWeb},
	".js":       {"\ue74e", "js", roleWeb},
	".mjs":      {"\ue74e", "js", roleWeb},
	".cjs":      {"\ue74e", "js", roleWeb},
	".map":      {"\ue60b", "mp", roleConfig},
	".tsx":      {"\ue7ba", "tx", roleUI},
	".jsx":      {"\ue7ba", "jx", roleUI},
	".vue":      {"\ue6a0", "vu", roleUI},
	".svelte":   {"\ue697", "sv", roleUI},
	".astro":    {"\ue6b3", "at", roleUI},
	".elm":      {"\ue62c", "em", roleUI},
	".html":     {"\ue736", "ht", roleWeb},
	".htm":      {"\ue736", "ht", roleWeb},
	".css":      {"\ue749", "cs", roleWeb},
	".scss":     {"\ue74b", "sc", roleWeb},
	".sass":     {"\ue74b", "sc", roleWeb},
	".less":     {"\ue614", "le", roleWeb},
	".styl":     {"\ue600", "st", roleWeb},
	".coffee":   {"\ue61b", "co", roleWeb},
	".graphql":  {"\uf20e", "gq", roleWeb},
	".gql":      {"\uf20e", "gq", roleWeb},
	".hbs":      {"\ue60f", "hb", roleWeb},
	".mustache": {"\ue60f", "mu", roleWeb},
	".ejs":      {"\ue60e", "ej", roleWeb},
	".pug":      {"\ue686", "pg", roleWeb},
	".haml":     {"\ue664", "hm", roleWeb},
	".liquid":   {"\ue670", "lq", roleWeb},
	".twig":     {"\ue61c", "tw", roleWeb},
	".http":     {"\uf1d8", "hp", roleWeb},
	// Compiled languages
	".go":     {"\ue627", "go", roleSystems},
	".rs":     {"\ue7a8", "rs", roleSystems},
	".c":      {"\ue61e", "c ", roleSystems},
	".h":      {"\ue61e", "h ", roleSystems},
	".cc":     {"\ue61d", "c+", roleSystems},
	".cpp":    {"\ue61d", "c+", roleSystems},
	".cxx":    {"\ue61d", "c+", roleSystems},
	".hpp":    {"\ue61d", "h+", roleSystems},
	".hh":     {"\ue61d", "h+", roleSystems},
	".m":      {"\ue61e", "m ", roleSystems},
	".mm":     {"\ue61d", "m+", roleSystems},
	".java":   {"\ue738", "jv", roleSystems},
	".kt":     {"\ue634", "kt", roleSystems},
	".kts":    {"\ue634", "kt", roleSystems},
	".scala":  {"\ue737", "sl", roleSystems},
	".sbt":    {"\ue737", "sb", roleSystems},
	".groovy": {"\ue775", "gv", roleSystems},
	".swift":  {"\ue755", "sw", roleSystems},
	".cs":     {"\ue648", "c#", roleSystems},
	".vb":     {"\ue70c", "vb", roleSystems},
	".fs":     {"\ue7a7", "f#", roleSystems},
	".fsx":    {"\ue7a7", "f#", roleSystems},
	".zig":    {"\ue6a9", "zg", roleSystems},
	".nim":    {"\ue677", "nm", roleSystems},
	".cr":     {"\ue62f", "cr", roleSystems},
	".d":      {"\ue7af", "d ", roleSystems},
	".hx":     {"\ue666", "hx", roleSystems},
	".sol":    {"\ue656", "so", roleSystems},
	".hs":     {"\ue777", "hs", roleSystems},
	".ml":     {"\ue67a", "ml", roleSystems},
	".mli":    {"\ue67a", "mi", roleSystems},
	".dart":   {"\ue798", "dt", roleSystems},
	".asm":    {"\ue637", "as", roleSystems},
	".s":      {"\ue637", "as", roleSystems},
	// Interpreted languages and shells
	".py":    {"\ue73c", "py", roleScript},
	".pyi":   {"\ue73c", "py", roleScript},
	".ipynb": {"\ue80f", "nb", roleScript},
	".rb":    {"\ue739", "rb", roleScript},
	".php":   {"\ue73d", "ph", roleScript},
	".lua":   {"\ue620", "lu", roleScript},
	".pl":    {"\ue769", "pl", roleScript},
	".pm":    {"\ue769", "pm", roleScript},
	".ex":    {"\ue62d", "ex", roleScript},
	".exs":   {"\ue62d", "ex", roleScript},
	".erl":   {"\ue7b1", "er", roleScript},
	".hrl":   {"\ue7b1", "hr", roleScript},
	".clj":   {"\ue768", "cj", roleScript},
	".cljs":  {"\ue76a", "cl", roleScript},
	".cljc":  {"\ue768", "cj", roleScript},
	".el":    {"\ue632", "el", roleScript},
	".lisp":  {"\ue632", "lp", roleScript},
	".scm":   {"\ue632", "sm", roleScript},
	".rkt":   {"\ue632", "rk", roleScript},
	".jl":    {"\ue624", "jl", roleScript},
	".r":     {"\ue68a", "r ", roleScript},
	".sh":    {"\ue795", "sh", roleScript},
	".bash":  {"\ue795", "sh", roleScript},
	".zsh":   {"\ue795", "sh", roleScript},
	".fish":  {"\ue795", "sh", roleScript},
	".awk":   {"\ue795", "aw", roleScript},
	".bat":   {"\ue795", "bt", roleScript},
	".cmd":   {"\ue795", "bt", roleScript},
	".ps1":   {"\ue795", "ps", roleScript},
	".vim":   {"\ue7c5", "vi", roleScript},
	// Prose
	".md":       {"\ue73e", "md", roleDocs},
	".mdx":      {"\ue73e", "mx", roleDocs},
	".rmd":      {"\ue609", "rm", roleDocs},
	".txt":      {"\uf0f6", "tt", roleDocs},
	".rst":      {"\uf0f6", "rt", roleDocs},
	".adoc":     {"\uf0f6", "ad", roleDocs},
	".asciidoc": {"\uf0f6", "ad", roleDocs},
	".rtf":      {"\uf0f6", "rf", roleDocs},
	".org":      {"\ue633", "og", roleDocs},
	".tex":      {"\ue69b", "te", roleDocs},
	".bib":      {"\ue69b", "bb", roleDocs},
	".pdf":      {"\uf1c1", "pd", roleDocs},
	".epub":     {"\ue28b", "ep", roleDocs},
	".doc":      {"\ue6a5", "dc", roleDocs},
	".docx":     {"\ue6a5", "dc", roleDocs},
	".ppt":      {"\uf1c4", "pt", roleDocs},
	".pptx":     {"\uf1c4", "pt", roleDocs},
	// Settings, manifests and lock files
	".json":       {"\ue60b", "{}", roleConfig},
	".jsonc":      {"\ue60b", "{}", roleConfig},
	".json5":      {"\ue60b", "{}", roleConfig},
	".yaml":       {"\ue615", "ym", roleConfig},
	".yml":        {"\ue615", "ym", roleConfig},
	".toml":       {"\ue615", "tm", roleConfig},
	".ini":        {"\ue615", "in", roleConfig},
	".conf":       {"\ue615", "cf", roleConfig},
	".cfg":        {"\ue615", "cg", roleConfig},
	".properties": {"\ue615", "pp", roleConfig},
	".plist":      {"\ue615", "pi", roleConfig},
	".xml":        {"\uf121", "<>", roleConfig},
	".proto":      {"\uf121", "pb", roleConfig},
	".lock":       {"\uf023", "lk", roleConfig},
	".nix":        {"\uf313", "nx", roleConfig},
	".tf":         {"\ue69a", "tf", roleConfig},
	".tfvars":     {"\ue69a", "tf", roleConfig},
	".hcl":        {"\ue69a", "hc", roleConfig},
	".bicep":      {"\ue63b", "bc", roleConfig},
	".gradle":     {"\ue660", "gd", roleConfig},
	".cmake":      {"\ue794", "cm", roleConfig},
	".diff":       {"\ue728", "df", roleConfig},
	".patch":      {"\ue728", "pa", roleConfig},
	".pem":        {"\uf0a3", "ce", roleConfig},
	".crt":        {"\uf0a3", "ce", roleConfig},
	".cer":        {"\uf0a3", "ce", roleConfig},
	".key":        {"\uf084", "ky", roleConfig},
	".pub":        {"\uf084", "ky", roleConfig},
	".gpg":        {"\uf084", "ky", roleConfig},
	".asc":        {"\uf084", "ky", roleConfig},
	".log":        {"\uf0f6", "lg", roleFile},
	// Tables and databases
	".sql":     {"\ue706", "sq", roleData},
	".db":      {"\ue706", "db", roleData},
	".sqlite":  {"\ue706", "sq", roleData},
	".sqlite3": {"\ue706", "sq", roleData},
	".csv":     {"\uf0ce", "cv", roleData},
	".tsv":     {"\uf0ce", "tv", roleData},
	".parquet": {"\uf0ce", "pq", roleData},
	".xls":     {"\ue6a6", "xl", roleData},
	".xlsx":    {"\ue6a6", "xl", roleData},
	".jsonl":   {"\ue60b", "jn", roleData},
	".ndjson":  {"\ue60b", "nd", roleData},
	".prisma":  {"\ue684", "pr", roleData},
	// Images, sound, video and fonts
	".png":   {"\uf1c5", "im", roleMedia},
	".jpg":   {"\uf1c5", "im", roleMedia},
	".jpeg":  {"\uf1c5", "im", roleMedia},
	".gif":   {"\uf1c5", "im", roleMedia},
	".webp":  {"\uf1c5", "im", roleMedia},
	".bmp":   {"\uf1c5", "im", roleMedia},
	".tiff":  {"\uf1c5", "im", roleMedia},
	".tif":   {"\uf1c5", "im", roleMedia},
	".avif":  {"\uf1c5", "im", roleMedia},
	".heic":  {"\uf1c5", "im", roleMedia},
	".ico":   {"\uf1c5", "im", roleMedia},
	".svg":   {"\uf1c5", "sg", roleMedia},
	".psd":   {"\ue7b8", "px", roleMedia},
	".mp3":   {"\uf1c7", "au", roleMedia},
	".wav":   {"\uf1c7", "au", roleMedia},
	".flac":  {"\uf1c7", "au", roleMedia},
	".ogg":   {"\uf1c7", "au", roleMedia},
	".m4a":   {"\uf1c7", "au", roleMedia},
	".aac":   {"\uf1c7", "au", roleMedia},
	".opus":  {"\uf1c7", "au", roleMedia},
	".mp4":   {"\uf1c8", "vd", roleMedia},
	".mov":   {"\uf1c8", "vd", roleMedia},
	".mkv":   {"\uf1c8", "vd", roleMedia},
	".avi":   {"\uf1c8", "vd", roleMedia},
	".webm":  {"\uf1c8", "vd", roleMedia},
	".wmv":   {"\uf1c8", "vd", roleMedia},
	".flv":   {"\uf1c8", "vd", roleMedia},
	".ttf":   {"\uf031", "ft", roleMedia},
	".otf":   {"\uf031", "ft", roleMedia},
	".woff":  {"\uf031", "ft", roleMedia},
	".woff2": {"\uf031", "ft", roleMedia},
	".eot":   {"\uf031", "ft", roleMedia},
	// Bundles
	".zip": {"\uf1c6", "zp", roleArchive},
	".tar": {"\uf1c6", "zp", roleArchive},
	".gz":  {"\uf1c6", "zp", roleArchive},
	".tgz": {"\uf1c6", "zp", roleArchive},
	".xz":  {"\uf1c6", "zp", roleArchive},
	".7z":  {"\uf1c6", "zp", roleArchive},
	".bz2": {"\uf1c6", "zp", roleArchive},
	".zst": {"\uf1c6", "zp", roleArchive},
	".rar": {"\uf1c6", "zp", roleArchive},
	".lz4": {"\uf1c6", "zp", roleArchive},
	".iso": {"\uf1c6", "is", roleArchive},
	".dmg": {"\uf1c6", "dm", roleArchive},
	".deb": {"\uf1c6", "de", roleArchive},
	".rpm": {"\uf1c6", "rp", roleArchive},
	".pkg": {"\uf1c6", "pk", roleArchive},
	".apk": {"\ue70e", "ap", roleArchive},
	".jar": {"\ue738", "jr", roleArchive},
	".war": {"\ue738", "wr", roleArchive},
	".whl": {"\ue73c", "wh", roleArchive},
	// Executables and objects
	".exe":   {"\uf471", "bn", roleBinary},
	".bin":   {"\uf471", "bn", roleBinary},
	".o":     {"\uf471", "bn", roleBinary},
	".so":    {"\uf471", "bn", roleBinary},
	".dylib": {"\uf471", "bn", roleBinary},
	".a":     {"\uf471", "bn", roleBinary},
	".dll":   {"\uf471", "bn", roleBinary},
	".lib":   {"\uf471", "bn", roleBinary},
	".pdb":   {"\uf471", "bn", roleBinary},
	".pyc":   {"\uf471", "pc", roleBinary},
	".class": {"\uf471", "cz", roleBinary},
	".wasm":  {"\uf471", "wa", roleBinary},
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
