// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ExtAudio",
    platforms: [.macOS(.v12), .iOS(.v13)],
    products: [.library(name: "ExtAudio", targets: ["ExtAudio"])],
    targets: [
        .target(name: "ExtAudio"),
        .testTarget(name: "ExtAudioTests", dependencies: ["ExtAudio"]),
    ],
    swiftLanguageVersions: [.v5]
)
