const sdk = @import("ext_io");
const common = @import("common.zig");
comptime {
    sdk.cabi.exportGuest(common.factory);
}
