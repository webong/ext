# Linux network resource guest

This private Ext guest serves `ext.system.network/v1`. Go handles only
`ext.io.pc/v1` transport and record translation; the native network read
is compiled from `pkg/ext-linux/net`.

On Linux, build `go build -o network-linux .` in this module. Seal the binary
with `scripts/release/pack-resource-guest.py` and its `ext.json`, then
install and trust `system.network.linux` through `ext package`.
