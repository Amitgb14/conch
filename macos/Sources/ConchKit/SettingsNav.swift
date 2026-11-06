import Foundation

/// The Settings window's sections: config.toml's, from the settings page's
/// schema, and the app's own that are not settings in that file.
public enum SettingsNav {
    /// Skills & MCP servers: conch's skills for agents and the library of
    /// MCP servers and skills, per machine. They are written to the agents'
    /// own folders, not to config.toml, so they are a section of the app's
    /// rather than of the schema.
    public static let skills = "skills"
    public static let skillsTitle = "Skills & MCP servers"
    public static let skillsIcon = "book-open"

    /// The sidebar's order: the schema's sections, with Skills & MCP
    /// servers just after Agents — or last, for a schema without one. A
    /// schema that comes to name a section "skills" itself keeps its own.
    public static func sidebar(_ schema: [String]) -> [String] {
        if schema.contains(skills) { return schema }
        var out = schema
        if let i = out.firstIndex(of: "agents") {
            out.insert(skills, at: i + 1)
        } else {
            out.append(skills)
        }
        return out
    }
}
