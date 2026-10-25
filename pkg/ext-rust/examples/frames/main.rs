// The frames guest in the default mode: threaded where there are threads, inline
// where there are not (a WASI build).
mod guest;
fn main() -> Result<(), Box<dyn std::error::Error>> {
    guest::run(ext_plugin::Mode::default())
}
