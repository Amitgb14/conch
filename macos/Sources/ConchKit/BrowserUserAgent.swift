import Foundation

// The words a browser tab adds after WebKit's own user agent. WebKit alone
// ends "AppleWebKit/605.1.15 (KHTML, like Gecko)", which is what an app's
// embedded web view sends, and sites read it so: claude.ai's artifacts
// switch their features off for a user agent with AppleWebKit and no
// Safari, or one ending "(KHTML, like Gecko)". A browser tab is a browser,
// so it says what Safari says. "Safari/605.1.15" is frozen, as WebKit's is.

/// "Version/V Safari/605.1.15": V is Safari's version on this Mac, else the
/// one that came with this macOS.
public func browserApplicationName(safariVersion: String?, system: OperatingSystemVersion) -> String {
    let given = safariVersion?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    let version = given.range(of: #"^\d+(\.\d+){0,2}$"#, options: .regularExpression) != nil ? given : shippedSafari(for: system)
    return "Version/\(version) Safari/605.1.15"
}

/// Safari's version that shipped with a macOS: the same number from macOS
/// 26 on, three ahead of it before (macOS 14 had Safari 17).
func shippedSafari(for s: OperatingSystemVersion) -> String {
    let major = s.majorVersion >= 26 ? s.majorVersion : max(s.majorVersion + 3, 17)
    return s.majorVersion >= 26 && s.minorVersion > 0 ? "\(major).\(s.minorVersion)" : "\(major).0"
}

/// Safari's own version, read from its bundle; nil without one.
public func installedSafariVersion(at path: String = "/Applications/Safari.app/Contents/Info.plist") -> String? {
    (NSDictionary(contentsOfFile: path)?["CFBundleShortVersionString"]) as? String
}
