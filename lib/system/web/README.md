# Web libraries

`lib/system/web` owns the platform web libraries: Swift `ExtWeb` and
`ExtWebKit` for Apple apps, Java web rules and Android WebView integration,
and C# WebView2 integration. `res/web` owns the Ext-facing contracts, shared
fixtures and the existing Go host module. The platform SDKs package the
native libraries without copying their source into `pkg/`.
