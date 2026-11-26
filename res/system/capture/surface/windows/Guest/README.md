# Windows surface resource guest

The private `system.surface.windows` Ext package invokes the native .NET
`WindowSurface` in `lib/system/act/capture/surface/windows`. It selects an
observed window by opaque ID and rechecks PID, process generation, bounds, and
focus before native capture or input. The `ext-windows` release builds and
seals the single-file guest; CTX requires it installed and trusted.
