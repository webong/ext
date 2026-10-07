// swift-tools-version:5.10
import PackageDescription

// Development manifest. It builds against the xcframework produced by
// scripts/plugin-swift-xcframework.sh; a release manifest points the binary
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
        // Test fixture for the cross-language conformance suite.
        .library(name: "ExtConformancePlugin", type: .dynamic, targets: ["ExtConformancePlugin"]),
    ],
    targets: [
        .binaryTarget(name: "CExtEngine", path: "Frameworks/CExtEngine.xcframework"),
        .target(name: "ExtPluginGuest", dependencies: ["CExtEngine"]),
        .target(name: "CExtPluginShim", dependencies: ["CExtEngine"]),
        .target(name: "ExtPluginExports", dependencies: ["ExtPluginGuest", "CExtPluginShim", "CExtEngine"]),
        .target(name: "ExtConformancePlugin", dependencies: ["ExtPluginGuest", "ExtPluginExports"]),
        .testTarget(name: "ExtPluginGuestTests", dependencies: ["ExtPluginGuest", "ExtPluginExports", "CExtEngine"]),
    ],
    swiftLanguageVersions: [.v5]
)
