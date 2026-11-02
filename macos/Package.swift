// swift-tools-version:5.9
// conch for macOS: a native window around conch's server. ConchKit is the
// part with no interface — the socket client, the protocol, the tree, the
// palette, the layout — so it is tested with `swift test`; ConchApp is the
// AppKit and SwiftUI window, each pane a terminal running `conch attach`.
import Foundation
import PackageDescription

// The terminal in each pane is Ghostty's when its library has been built
// (macos/ghostty.sh, which needs Xcode and Zig), and SwiftTerm's otherwise
// — so a checkout with only the Command Line Tools still builds, the same
// app with the other terminal behind the same seam (TerminalHost.swift).
let here = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
let ghosttyKit = "vendor/GhosttyKit.xcframework"
let withGhostty = FileManager.default.fileExists(atPath: here.appendingPathComponent(ghosttyKit).path)

var appDeps: [Target.Dependency] = ["ConchKit", "SwiftTerm"]
var targets: [Target] = [
    .target(name: "ConchKit"),
    .testTarget(name: "ConchKitTests", dependencies: ["ConchKit"]),
]
var swiftSettings: [SwiftSetting] = []
var linker: [LinkerSetting] = []
if withGhostty {
    targets.append(.binaryTarget(name: "GhosttyKit", path: ghosttyKit))
    appDeps.append("GhosttyKit")
    swiftSettings.append(.define("GHOSTTY"))
    // What libghostty itself links against on macOS.
    linker += ["c++", "z"].map { .linkedLibrary($0) }
    linker += ["Carbon", "CoreText", "CoreGraphics", "Metal", "MetalKit", "QuartzCore", "IOSurface",
               "UniformTypeIdentifiers", "AppKit", "CoreVideo"].map { .linkedFramework($0) }
}
targets.append(.executableTarget(name: "ConchApp", dependencies: appDeps,
                                 swiftSettings: swiftSettings, linkerSettings: linker))

let package = Package(
    name: "Conch",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "Conch", targets: ["ConchApp"]),
    ],
    dependencies: [
        .package(url: "https://github.com/migueldeicaza/SwiftTerm", exact: "1.20.0"),
    ],
    targets: targets
)
