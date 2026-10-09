// swift-tools-version:5.10
import Foundation
import PackageDescription

// The WAMR host is opt-in: it is part of the package only once scripts/plugin-ios-wamr-xcframework.sh
// has built Frameworks/CExtWamr.xcframework, so a build that does not want WAMR never needs it.
let wamrFramework = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
    .appendingPathComponent("Frameworks/CExtWamr.xcframework").path
let withWamr = FileManager.default.fileExists(atPath: wamrFramework)

// Development manifest. It builds against the xcframework produced by
// scripts/plugin-ios-xcframework.sh; a release manifest points the binary
// target at a published archive with a checksum instead.
let package = Package(
    name: "ExtPlugin",
    platforms: [.macOS(.v12), .iOS(.v13)],
    products: [
        // Author a plugin: Guest, Descriptor, Call, RemoteError.
        .library(name: "ExtPluginGuest", targets: ["ExtPluginGuest"]),
        // Export the four ext_plugin_* C symbols so any plugin-cshared host can
        // load the plugin. Needs one ext_plugin_guest_factory definition.
        .library(name: "ExtPluginExports", targets: ["ExtPluginExports"]),
        // Host a WebAssembly reactor in a hidden web view (docs/plugin-reactor-abi.md).
        .library(name: "ExtPluginWebView", targets: ["ExtPluginWebView"]),
        // Test fixture for the cross-language conformance suite.
        .library(name: "ExtConformancePlugin", type: .dynamic, targets: ["ExtConformancePlugin"]),
    ],
    targets: [
        .binaryTarget(name: "CExtEngine", path: "Frameworks/CExtEngine.xcframework"),
        .target(name: "ExtPluginGuest", dependencies: ["CExtEngine"]),
        .target(name: "CExtPluginShim", dependencies: ["CExtEngine"]),
        .target(name: "ExtPluginExports", dependencies: ["ExtPluginGuest", "CExtPluginShim", "CExtEngine"]),
        .target(name: "ExtConformancePlugin", dependencies: ["ExtPluginGuest", "ExtPluginExports"]),
        .target(name: "ExtPluginWebView"),
        .testTarget(name: "ExtPluginWebViewTests", dependencies: ["ExtPluginWebView"]),
        .testTarget(name: "ExtPluginGuestTests", dependencies: ["ExtPluginGuest", "ExtPluginExports", "CExtEngine"]),
    ],
    swiftLanguageVersions: [.v5]
)

if withWamr {
    package.products.append(.library(name: "ExtPluginWamr", targets: ["ExtPluginWamr"]))
    package.targets.append(contentsOf: [
        .binaryTarget(name: "CExtWamr", path: "Frameworks/CExtWamr.xcframework"),
        // CExtEngine supplies the engine core that CExtWamr's host objects use.
        .target(name: "ExtPluginWamr", dependencies: ["CExtWamr", "CExtEngine"]),
        .testTarget(name: "ExtPluginWamrTests", dependencies: ["ExtPluginWamr"]),
    ])
}
