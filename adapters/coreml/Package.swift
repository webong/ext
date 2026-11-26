// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ExtCoreMLGuest",
    platforms: [.macOS(.v12)],
    products: [
        .executable(name: "ext-coreml-guest", targets: ["ExtCoreMLGuest"]),
    ],
    dependencies: [
        // ext-macos-0.1.0 on dist/ext-macos. SwiftPM cannot resolve this
        // distribution's prefixed tag as a version, so pin its commit.
        .package(url: "https://github.com/webong/ext.git",
                 revision: "a1e69d860f610a5a0c1567a18c61fbc03780c9d8"),
    ],
    targets: [
        .executableTarget(name: "ExtCoreMLGuest", dependencies: [
            .product(name: "ExtML", package: "ext"),
            .product(name: "ExtPluginGuest", package: "ext"),
        ]),
    ],
    swiftLanguageVersions: [.v5]
)
