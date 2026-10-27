// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ExtML",
    platforms: [.macOS(.v12), .iOS(.v13)],
    products: [.library(name: "ExtML", targets: ["ExtML"])],
    targets: [
        .target(name: "ExtML"),
        .testTarget(name: "ExtMLTests", dependencies: ["ExtML"]),
    ],
    swiftLanguageVersions: [.v5]
)
