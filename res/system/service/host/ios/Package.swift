// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ExtHost",
    platforms: [.macOS(.v12), .iOS(.v13)],
    products: [.library(name: "ExtHost", targets: ["ExtHost"])],
    targets: [
        .target(name: "ExtHost"),
        .testTarget(name: "ExtHostTests", dependencies: ["ExtHost"]),
    ],
    swiftLanguageVersions: [.v5]
)
