mod common;
fn main() -> Result<(), Box<dyn std::error::Error>> {
    let guest = common::factory()?;
    ext_plugin::guest::serve(&guest, std::io::stdin().lock(), std::io::stdout().lock())?;
    Ok(())
}
