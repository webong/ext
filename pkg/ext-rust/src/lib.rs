//! ext for Rust: one crate with a module per area, each behind a Cargo feature of the same name.
//! Only `plugin` (the ext.plugin/v1 host and guest SDK) exists so far; `system`, `web` and `int`
//! are planned. A build that enables one area compiles none of another area's code or dependencies.
#[cfg(feature = "plugin")]
pub mod plugin;
