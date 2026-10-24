use ext_plugin_engine::{CallOptions, Host, Process};
use std::fs;
fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().collect();
    if args.len() < 4 {
        return Err("expected guest descriptor request files".into());
    }
    // Fixture policy only. Applications verify artifact provenance and enforce
    // per-operation authorization in these callbacks.
    let host = Host::process(
        Process {
            executable: &args[1],
            ..Default::default()
        },
        &fs::read(&args[2])?,
        Box::new(|_| Ok(())),
        Box::new(|_| Ok(())),
    )?;
    host.start(CallOptions::default())?;
    for file in &args[3..] {
        let response = host.invoke(&fs::read(file)?, CallOptions::default())?;
        println!("{}", String::from_utf8(response)?);
    }
    host.drain(CallOptions::default())?;
    Ok(())
}
