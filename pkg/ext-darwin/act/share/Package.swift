// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ExtShare",
    platforms: [.macOS(.v12), .iOS(.v13)],
    products: [.library(name: "ExtShare", targets: ["ExtShare"])],
    targets: [
        .target(name: "ExtShare"),
        .testTarget(name: "ExtShareTests", dependencies: ["ExtShare"]),
    ],
    swiftLanguageVersions: [.v5]
)
