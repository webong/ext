// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ExtNetwork",
    platforms: [.macOS(.v12), .iOS(.v13)],
    products: [.library(name: "ExtNetwork", targets: ["ExtNetwork"])],
    targets: [
        .target(name: "ExtNetwork"),
        .testTarget(name: "ExtNetworkTests", dependencies: ["ExtNetwork"]),
    ],
    swiftLanguageVersions: [.v5]
)
