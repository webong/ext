const sdk = @import("ctx_plugin");
const common = @import("common.zig");
comptime {
    sdk.cabi.exportGuest(common.factory);
}
