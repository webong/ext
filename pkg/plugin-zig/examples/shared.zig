const sdk = @import("ext_plugin");
const common = @import("common.zig");
comptime {
    sdk.cabi.exportGuest(common.factory);
}
