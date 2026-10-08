#!/usr/bin/env bash
# Build and run an Objective-C program against pkg/plugin-ios through its EXT*
# facades and the ext_plugin_* C ABI. Set EXT_RUST_REACTOR to also check the web-view
# host (the Rust shared example built for wasm32-wasip1).
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Darwin ]] || { echo 'plugin-ios-objc.sh needs macOS and Xcode' >&2; exit 1; }
scripts/plugin-ios-xcframework.sh >/dev/null
sdk=pkg/plugin-ios
swift build --package-path "$sdk" >/dev/null
build="$(swift build --package-path "$sdk" --show-bin-path)"
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-objc.XXXXXX")"
trap 'rm -rf "$work"' EXIT
objects=()
for target in ExtPluginGuest ExtPluginExports ExtPluginWebView CExtPluginShim; do
  while IFS= read -r object; do objects+=("$object"); done < <(find "$build/$target.build" -name '*.o')
done
# Swift objects auto-link compatibility libraries that live in the toolchain.
toolchain_swift="$(dirname "$(xcrun -f swiftc)")/../lib/swift/macosx"
clang -fobjc-arc -fmodules -Wall -Wextra -Werror -Wno-unused-parameter \
  -I "$build/ExtPluginGuest.build" -I "$build/ExtPluginExports.build" -I "$build/ExtPluginWebView.build" \
  "$sdk/Tests/ObjC/main.m" "${objects[@]}" "$sdk/Frameworks/CExtEngine.xcframework/macos-arm64_x86_64/libext_guest.a" \
  -framework Foundation -framework WebKit -L/usr/lib/swift -L"$(xcrun --show-sdk-path)/usr/lib/swift" -L"$toolchain_swift" \
  -Wl,-rpath,/usr/lib/swift -o "$work/objc-check"
"$work/objc-check"
