// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ExtWebKit",
    platforms: [.macOS(.v12), .iOS(.v13)],
    products: [.library(name: "ExtWebKit", targets: ["ExtWebKit"])],
    dependencies: [.package(path: "../../res/web/ios")],
    targets: [
        .target(name: "ExtWebKit", dependencies: [.product(name: "ExtWeb", package: "ios")], path: "ios/Sources/ExtWebKit"),
        .testTarget(name: "ExtWebKitTests", dependencies: ["ExtWebKit", .product(name: "ExtWeb", package: "ios")], path: "ios/Tests/ExtWebKitTests"),
    ],
    swiftLanguageVersions: [.v5]
)
