// swift-tools-version:5.9
// conch for macOS: a native window around conch's server. ConchKit is the
// part with no interface — the socket client, the protocol, the tree, the
// palette, the layout — so it is tested with `swift test`; ConchApp is the
// AppKit and SwiftUI window, each pane a terminal running `conch attach`.
import PackageDescription

let package = Package(
    name: "Conch",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "Conch", targets: ["ConchApp"]),
    ],
    dependencies: [
        // The terminal in each pane, behind one seam (TerminalHost.swift) so
        // Ghostty's can take its place once it can be built here.
        .package(url: "https://github.com/migueldeicaza/SwiftTerm", exact: "1.20.0"),
    ],
    targets: [
        .target(name: "ConchKit"),
        .executableTarget(name: "ConchApp", dependencies: ["ConchKit", "SwiftTerm"]),
        .testTarget(name: "ConchKitTests", dependencies: ["ConchKit"]),
    ]
)
