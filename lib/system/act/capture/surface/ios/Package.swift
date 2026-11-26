// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ExtSystem",
    platforms: [.macOS(.v12), .iOS(.v13)],
    products: [.library(name: "ExtSystem", targets: ["ExtSystem"])],
    targets: [
        .target(name: "ExtSystem"),
        .testTarget(name: "ExtSystemTests", dependencies: ["ExtSystem"]),
    ],
    swiftLanguageVersions: [.v5]
)
