// The page document of the web-engine host. The glue that instantiates a reactor and
// answers handshake and invoke messages is pkg/plugin-wasm/page/reactor.js, the one
// source shared with the Android host; see its header for the message protocol.

enum PageGlue {
    /// The page document. The module is embedded as base64 because a hidden
    /// page has no server to fetch it from.
    static func html(moduleBase64: String) -> String {
        """
        <!doctype html><html><head><meta charset="utf-8"></head><body><script>
        const MODULE_BASE64 = "\(moduleBase64)";
        \(script)
        </script></body></html>
        """
    }

    /// pkg/plugin-wasm/page/reactor.js, embedded by scripts/plugin-mobile-assets.sh.
    static var script: String { GeneratedAssets.reactorPage }
}
