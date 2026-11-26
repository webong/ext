// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ExtCoreMLGuest",
    platforms: [.macOS(.v12)],
    products: [
        .executable(name: "ext-coreml-guest", targets: ["ExtCoreMLGuest"]),
    ],
    dependencies: [
        .package(path: "../../../../lib/system/int/model/ios"),
    ],
    targets: [
        .executableTarget(name: "ExtCoreMLGuest", dependencies: [.product(name: "ExtML", package: "ios")]),
    ],
    swiftLanguageVersions: [.v5]
)
