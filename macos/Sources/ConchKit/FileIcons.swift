import Foundation

// Each file in the Files tab marked by what it is, as an editor's file
// tree does: its language's or tool's own logo where Simple Icons has one
// (Python's, Docker's whale, Go's, npm's — FileLogos), in its brand
// colour; otherwise a Lucide outline for its kind (code, settings, a
// picture, a lock file…) in its language's colour. The colours are GitHub Linguist's
// for each language, except where Linguist's is too dark to read on a
// dark sidebar (C, CSS, Dockerfile) and the language's own logo colour is
// used instead; the kinds that are not languages (pictures, archives,
// secrets) were chosen. Any of them still too close to the background is
// drawn in the text colour (fileTint).

public struct FileIcon: Equatable, Sendable {
    /// A LucideIcons name.
    public var icon: String
    /// Its colour; nil for the theme's muted one.
    public var color: String?
    /// A FileLogos slug: drawn filled instead of the outline, which stays
    /// for a build that lacks it.
    public var logo: String?

    public init(_ icon: String, _ color: String? = nil) {
        self.icon = icon
        self.color = color
    }

    /// A logo, in its own brand colour unless given another.
    public static func logo(_ slug: String, _ fallback: String = "file-code", color: String? = nil) -> FileIcon {
        var f = FileIcon(fallback, color ?? FileLogos.logos[slug]?.hex)
        f.logo = slug
        return f
    }
}

/// What marks a file, by its name: whole names first (go.mod, Dockerfile,
/// a lock file), then the extension, then a plain page.
public func fileIcon(_ name: String) -> FileIcon {
    let lower = name.lowercased()
    if let f = byName[lower] { return f }
    if lower.hasPrefix("readme") { return FileIcon("book-open", "#519ABA") }
    if lower.hasPrefix("license") || lower.hasPrefix("licence") || lower.hasPrefix("copying") { return FileIcon("scale", "#D4AA00") }
    if lower.hasPrefix("dockerfile") || lower.hasPrefix("docker-compose") || lower.hasPrefix("compose.y") || lower.hasSuffix(".dockerfile") {
        return .logo("docker", "container")
    }
    // A tool's own config, by the tool: vite.config.ts, .eslintrc.json…
    for (prefix, slug) in toolConfigs where lower.hasPrefix(prefix) { return .logo(slug, "file-cog") }
    if lower == ".env" || lower.hasPrefix(".env.") { return FileIcon("file-key", "#E5C07B") }
    let ext = (lower as NSString).pathExtension
    if !ext.isEmpty, let f = byExtension[ext] { return f }
    if lower.hasPrefix(".git") { return .logo("git", "git-branch") }
    if lower.hasPrefix(".") { return FileIcon("file-cog") } // a tool's dotfile
    return FileIcon("file")
}

/// The colour a file's mark is drawn in on bg: its own, unless that
/// would vanish into the background — a yellow on a light theme, Rust's
/// black on a dark one. Small marks need more than an agent's: 2, not 1.6.
public func fileTint(_ f: FileIcon, on bg: RGB, text fg: RGB, muted: RGB) -> RGB {
    guard let c = f.color.map(RGB.init) else { return muted }
    return contrast(c, bg) < 2 ? fg : c
}

private let toolConfigs: [(String, String)] = [
    ("vite.config", "vite"), ("webpack.config", "webpack"), (".eslintrc", "eslint"), ("eslint.config", "eslint"),
    (".prettierrc", "prettier"), ("prettier.config", "prettier"), ("tsconfig", "typescript"), ("jsconfig", "javascript"),
    (".npmrc", "npm"), (".nvmrc", "nodedotjs"), (".yarnrc", "yarn"), ("cmakelists.txt", "cmake"), ("nginx.conf", "nginx"),
    (".editorconfig", "editorconfig"), (".zshrc", "zsh"), (".zprofile", "zsh"), (".bashrc", "gnubash"), (".bash_profile", "gnubash"),
]

private let byName: [String: FileIcon] = {
    var m: [String: FileIcon] = [:]
    let pkg: [(String, String)] = [("go.mod", "#00ADD8"), ("package.json", "#CB3837"), ("cargo.toml", "#DEA584"),
                                   ("package.swift", "#F05138"), ("pyproject.toml", "#3572A5"), ("gemfile", "#701516"),
                                   ("composer.json", "#4F5D95"), ("pom.xml", "#B07219"), ("build.gradle", "#02303A"),
                                   ("build.gradle.kts", "#A97BFF"), ("deno.json", "#70FFAF"), ("requirements.txt", "#3572A5")]
    for (n, c) in pkg { m[n] = FileIcon("package", c) }
    let logos: [(String, String)] = [
        ("go.mod", "go"), ("go.sum", "go"), ("go.work", "go"), ("package.json", "npm"), ("package-lock.json", "npm"),
        ("yarn.lock", "yarn"), ("pnpm-lock.yaml", "pnpm"), ("pnpm-workspace.yaml", "pnpm"), ("bun.lockb", "bun"), ("bun.lock", "bun"),
        ("deno.json", "deno"), ("deno.lock", "deno"), ("cargo.toml", "rust"), ("cargo.lock", "rust"), ("package.swift", "swift"),
        ("package.resolved", "swift"), ("pyproject.toml", "python"), ("requirements.txt", "python"), ("poetry.lock", "python"),
        ("uv.lock", "python"), ("pipfile", "python"), ("gemfile", "ruby"), ("gemfile.lock", "ruby"), ("rakefile", "ruby"),
        ("composer.json", "php"), ("composer.lock", "php"), ("pom.xml", "apachemaven"), ("build.gradle", "gradle"),
        ("build.gradle.kts", "gradle"), ("settings.gradle", "gradle"), ("flake.nix", "nixos"), ("flake.lock", "nixos"),
    ]
    let locks: [String] = ["go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "cargo.lock", "gemfile.lock",
                           "poetry.lock", "composer.lock", "package.resolved", "bun.lockb", "uv.lock", "flake.lock"]
    for n in locks { m[n] = FileIcon("file-lock") }
    for n in ["makefile", "gnumakefile", "justfile", "rakefile", "procfile", "brewfile", "vagrantfile"] { m[n] = FileIcon("file-terminal", "#6D8086") }
    m["makefile"] = FileIcon("file-terminal", "#427819")
    for n in [".gitignore", ".gitattributes", ".gitmodules", ".gitkeep"] { m[n] = FileIcon("git-branch", "#F05032") }
    m[".dockerignore"] = FileIcon("container", "#2496ED")
    m["codeowners"] = FileIcon("file-cog")
    for n in [".gitignore", ".gitattributes", ".gitmodules", ".gitkeep"] { m[n] = .logo("git", "git-branch") }
    m[".dockerignore"] = .logo("docker", "container")
    for (n, slug) in logos { m[n] = .logo(slug, m[n]?.icon ?? "package") }
    return m
}()

private let byExtension: [String: FileIcon] = {
    var m: [String: FileIcon] = [:]
    func code(_ c: String, _ exts: String...) { for e in exts { m[e] = FileIcon("file-code", c) } }
    code("#00ADD8", "go")
    code("#F05138", "swift")
    code("#3178C6", "ts", "tsx", "mts", "cts")
    code("#F1E05A", "js", "jsx", "mjs", "cjs")
    code("#3572A5", "py", "pyi")
    code("#DEA584", "rs")
    code("#701516", "rb", "erb")
    code("#B07219", "java")
    code("#A97BFF", "kt", "kts")
    code("#A8B9CC", "c", "h") // C's logo; Linguist's #555555 disappears on dark
    code("#F34B7D", "cc", "cpp", "cxx", "hpp", "hh", "hxx")
    code("#438EFF", "m", "mm")
    code("#178600", "cs")
    code("#4F5D95", "php")
    code("#E34C26", "html", "htm", "xhtml")
    code("#663399", "css") // CSS's own purple; Linguist's #563D7C is too dark
    code("#C6538C", "scss", "sass")
    code("#1D365D", "less")
    code("#41B883", "vue")
    code("#FF3E00", "svelte")
    code("#FF5A03", "astro")
    code("#000080", "lua")
    code("#6E4A7E", "ex", "exs")
    code("#5E5086", "hs")
    code("#DC322F", "scala")
    code("#00B4AB", "dart")
    code("#EC915C", "zig")
    code("#A270BA", "jl")
    code("#198CE7", "r")
    code("#F7A80D", "nim")
    code("#0298C3", "pl", "pm")
    code("#60B5CC", "elm")
    code("#B845FC", "graphql", "gql")
    code("#7E7EFF", "nix")
    code("#844FBA", "tf", "hcl")
    code("#E38C00", "sql")
    code("#ECDEBE", "proto")
    for e in ["sh", "bash", "zsh", "fish", "ksh", "command"] { m[e] = FileIcon("file-terminal", "#89E051") }
    m["ps1"] = FileIcon("file-terminal", "#012456")
    m["bat"] = FileIcon("file-terminal", "#C1F12E")
    for e in ["json", "jsonc", "json5", "jsonl", "ndjson"] { m[e] = FileIcon("file-braces", "#CBCB41") }
    m["yaml"] = FileIcon("file-cog", "#CB171E")
    m["yml"] = FileIcon("file-cog", "#CB171E")
    m["toml"] = FileIcon("file-cog", "#9C4221")
    for e in ["ini", "cfg", "conf", "properties", "editorconfig", "plist", "xml", "xcconfig", "entitlements", "mod", "service"] { m[e] = FileIcon("file-cog") }
    for e in ["md", "mdx", "markdown"] { m[e] = FileIcon("file-text", "#519ABA") }
    for e in ["txt", "rst", "adoc", "org", "log", "rtf"] { m[e] = FileIcon("file-text") }
    m["pdf"] = FileIcon("file-text", "#E5252A")
    for e in ["png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff", "heic", "heif", "ico", "icns", "svg", "avif"] { m[e] = FileIcon("file-image", "#A074C4") }
    for e in ["mp4", "m4v", "mov", "qt", "webm", "mkv", "avi"] { m[e] = FileIcon("file-video-camera", "#FD971F") }
    for e in ["mp3", "m4a", "aac", "wav", "aif", "aiff", "caf", "flac", "ogg", "opus"] { m[e] = FileIcon("file-music", "#EF6C9F") }
    for e in ["zip", "tar", "gz", "tgz", "bz2", "xz", "zst", "7z", "rar", "jar", "war", "dmg", "pkg", "deb", "rpm"] { m[e] = FileIcon("file-archive", "#ECA517") }
    for e in ["csv", "tsv", "xls", "xlsx", "numbers", "ods", "parquet"] { m[e] = FileIcon("file-spreadsheet", "#89E051") }
    for e in ["ttf", "otf", "woff", "woff2", "eot"] { m[e] = FileIcon("file-type") }
    for e in ["pem", "key", "crt", "cer", "p12", "pfx", "pub", "asc", "gpg"] { m[e] = FileIcon("file-key", "#E5C07B") }
    for e in ["diff", "patch"] { m[e] = FileIcon("file-diff", "#41B883") }
    for e in ["db", "sqlite", "sqlite3"] { m[e] = FileIcon("database", "#E38C00") }
    m["lock"] = FileIcon("file-lock")
    let langs: [(String, [String])] = [
        ("go", ["go"]), ("python", ["py", "pyi", "pyw"]), ("typescript", ["ts", "mts", "cts"]), ("javascript", ["js", "mjs", "cjs"]),
        ("react", ["jsx", "tsx"]), ("rust", ["rs"]), ("swift", ["swift"]), ("ruby", ["rb", "erb", "gemspec"]), ("openjdk", ["java"]),
        ("kotlin", ["kt", "kts"]), ("c", ["c", "h"]), ("cplusplus", ["cc", "cpp", "cxx", "hpp", "hh", "hxx"]), ("dotnet", ["cs", "csproj", "sln"]),
        ("fsharp", ["fs", "fsx"]), ("php", ["php"]), ("html5", ["html", "htm", "xhtml"]), ("css", ["css"]), ("sass", ["scss", "sass"]),
        ("less", ["less"]), ("vuedotjs", ["vue"]), ("svelte", ["svelte"]), ("astro", ["astro"]), ("lua", ["lua"]),
        ("elixir", ["ex", "exs"]), ("erlang", ["erl", "hrl"]), ("haskell", ["hs"]), ("scala", ["scala", "sc"]), ("dart", ["dart"]),
        ("zig", ["zig"]), ("julia", ["jl"]), ("r", ["r", "rmd"]), ("perl", ["pl", "pm"]), ("elm", ["elm"]),
        ("graphql", ["graphql", "gql"]), ("nixos", ["nix"]), ("terraform", ["tf", "tfvars", "hcl"]), ("ocaml", ["ml", "mli"]),
        ("clojure", ["clj", "cljs", "edn"]), ("solidity", ["sol"]), ("gnubash", ["sh", "bash"]), ("zsh", ["zsh"]),
        ("jupyter", ["ipynb"]), ("latex", ["tex", "sty", "bib"]), ("sqlite", ["sqlite", "sqlite3", "db"]), ("mdx", ["mdx"]),
    ]
    for (slug, exts) in langs { for e in exts { m[e] = .logo(slug, m[e]?.icon ?? "file-code") } }
    for e in ["json", "jsonc", "json5"] { m[e] = .logo("json", "file-braces", color: "#CBCB41") } // its logo is black
    m["yaml"] = .logo("yaml", "file-cog")
    m["yml"] = .logo("yaml", "file-cog")
    m["toml"] = .logo("toml", "file-cog")
    for e in ["md", "markdown"] { m[e] = .logo("markdown", "file-text", color: "#519ABA") } // its logo is black
    m["dockerfile"] = .logo("docker", "container")
    return m
}()
