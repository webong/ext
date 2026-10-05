//! Typed CTX hosts and guests. Domain policy is mandatory on both sides.
pub mod cabi;
pub mod guest;
pub mod host;
#[cfg(not(target_os = "wasi"))]
pub mod native;
pub mod wire;
pub use guest::{CallContext, Guest, Method, Registry};
pub use wire::{ContractRef, Descriptor, Identity, Operation, RemoteError, Request};
pub const VERSION: &str = "ctx.plugin/v1";
pub const MAX_FRAME: usize = 24 << 20;
pub const DEFAULT_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(30);
#[derive(Debug)]
pub enum Error {
    Invalid,
    Denied,
    Mismatch,
    Unsupported,
    Closed,
    Deadline,
    Transport,
    Remote(RemoteError),
}
impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{self:?}")
    }
}
impl std::error::Error for Error {}
pub type Result<T> = std::result::Result<T, Error>;
pub type Policy = std::sync::Arc<dyn Fn(&Request) -> Result<()> + Send + Sync>;
