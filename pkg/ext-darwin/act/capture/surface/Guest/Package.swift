// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ExtSurfaceGuest",
    platforms: [.macOS(.v12)],
    dependencies: [.package(path: "../../../..")],
    targets: [
        .executableTarget(name: "ExtSurfaceGuest", dependencies: [
            .product(name: "ExtPluginGuest", package: "ext-darwin"),
            .product(name: "ExtSystem", package: "ext-darwin"),
            .product(name: "ExtHost", package: "ext-darwin"),
        ]),
    ]
)
