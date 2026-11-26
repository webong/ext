# Windows network resource guest

This private Ext guest serves `ext.system.network/v1` from the native
`lib/system/net/windows` .NET library. It reports bounded local interfaces,
addresses and resolver state. Route collection is explicitly unsupported on
Windows.

Publish it as a self-contained single file for `win-x64`, then seal the
published executable with `scripts/release/pack-resource-guest.py` and
`ext.json`. Install with `ext package install` and approve the installed
digest with `ext package trust system.network.windows`.
