// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ExtClipboard",
    platforms: [.macOS(.v12), .iOS(.v13)],
    products: [.library(name: "ExtClipboard", targets: ["ExtClipboard"])],
    targets: [
        .target(name: "ExtClipboard"),
        .testTarget(name: "ExtClipboardTests", dependencies: ["ExtClipboard"]),
    ],
    swiftLanguageVersions: [.v5]
)
