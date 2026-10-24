// The frames guest forced into inline mode, so the mode a WASI build uses is
// exercised natively too.
#[path = "frames/guest.rs"]
mod guest;
fn main() -> Result<(), Box<dyn std::error::Error>> {
    guest::run(ext_plugin::Mode::Inline)
}
