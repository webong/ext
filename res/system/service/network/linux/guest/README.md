# Linux network resource guest

This private Ext guest serves `ext.system.network/v1`. Go handles only
`ext.plugin/v1` transport and record translation; the native network read
is compiled from `lib/system/net/linux`.

On Linux, build `go build -o network-linux .` in this module. Seal the binary
with `scripts/release/pack-resource-guest.py` and its `ext.json`, then
install and trust `system.network.linux` through `ext package`.
