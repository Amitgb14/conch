import Foundation

// Conch Cloud is hidden in this release. The switch is conch's
// (config.CloudEnabled in internal/config): CONCH_CLOUD, when set to
// anything but "", wins — 1, true, yes or on is on, anything else off —
// else [cloud] enabled in config.toml, else off. The app reads the same
// variable and the same key, so it offers Cloud exactly when the conch it
// runs does; the settings page's [cloud] section comes from conch itself.
// Hidden is not broken: a cloud machine already in machines.json is still
// shown, reached, woken and put to sleep.

/// The variable that turns Conch Cloud on or off over config.toml.
public let cloudEnv = "CONCH_CLOUD"

/// Whether a machine is listed with the other machines where a project
/// can go: always, unless it is a cloud machine and Conch Cloud is offered,
/// which lists its machines in a group of its own.
public func listedWithMachines(cloud: Bool, offerCloud: Bool) -> Bool { !cloud || !offerCloud }

/// Whether Conch Cloud is offered, from config.toml's text and the
/// environment, as conch decides it.
public func cloudEnabled(toml: String?, env: [String: String]) -> Bool {
    if let v = env[cloudEnv]?.trimmingCharacters(in: .whitespaces), !v.isEmpty {
        return ["1", "true", "yes", "on"].contains(v.lowercased())
    }
    return tomlSetting("cloud", "enabled", in: toml) == "true"
}
