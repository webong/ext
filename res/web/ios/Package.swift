// swift-tools-version:5.10
import PackageDescription

// The web rules of res/web for Apple platforms: cookies, userscripts and bookmarklets, written
// out in Swift and checked against the language-neutral fixtures in ../testdata. It needs
// nothing else: no engine, no plugin code. pkg/ext-ios ships these sources next to its plugin
// code (its Sources/ExtWeb is a link to this target).
let package = Package(
    name: "ExtWeb",
    platforms: [.macOS(.v12), .iOS(.v13)],
    products: [
        .library(name: "ExtWeb", targets: ["ExtWeb"])
    ],
    targets: [
        .target(name: "ExtWeb"),
        .testTarget(name: "ExtWebTests", dependencies: ["ExtWeb"]),
    ],
    swiftLanguageVersions: [.v5]
)
