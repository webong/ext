// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ExtML",
    platforms: [.macOS(.v12), .iOS(.v13)],
    products: [
        .library(name: "ExtML", targets: ["ExtML"]),
        .executable(name: "ext-coreml-guest", targets: ["ExtCoreMLGuest"]),
    ],
    targets: [
        .target(name: "ExtML"),
        .executableTarget(name: "ExtCoreMLGuest", dependencies: ["ExtML"]),
        .testTarget(name: "ExtMLTests", dependencies: ["ExtML"]),
    ],
    swiftLanguageVersions: [.v5]
)
