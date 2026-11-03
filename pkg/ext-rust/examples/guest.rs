mod common;
fn main() -> Result<(), Box<dyn std::error::Error>> {
    let guest = common::factory()?;
    ext_io::plugin::guest::serve_stdio(&guest)?;
    Ok(())
}
