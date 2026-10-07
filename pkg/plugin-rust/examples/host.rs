mod common;
use ctx_plugin::{host::Session, native::Worker, *};
use std::{path::Path, sync::Arc, time::Duration};
fn main() -> std::result::Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    if args.len() < 2 {
        return Err("usage: host cshared LIBRARY | host process PROGRAM [ARGS...]".into());
    }
    let selected = common::factory()?.descriptor();
    let d = selected.clone();
    // Explicit local fixture paths are trusted by the person running this demo.
    // Production verify callbacks authenticate artifacts before this loader runs.
    let mut session = Session::open(
        selected,
        |_| Ok(()),
        || {
            let path = Path::new(&args[1]);
            let b = match args[0].as_str() {
                "cshared" => Worker::cshared(path)?,
                "process" => Worker::process(path, &args[2..])?,
                _ => return Err(Error::Unsupported),
            };
            Ok(Box::new(b))
        },
        Arc::new(move |r| r.validate(&d)),
    )?;
    let c = ContractRef {
        name: "ext.conformance".into(),
        version: "v1".into(),
    };
    let out = session.call_raw(
        &c,
        "echo",
        serde_json::json!({"value":7}),
        Duration::from_secs(3),
    )?;
    if out != serde_json::json!({"value":7}) {
        return Err("echo mismatch".into());
    }
    match session.call_raw(
        &c,
        "public-error",
        serde_json::Value::Null,
        Duration::from_secs(3),
    ) {
        Err(Error::Remote(r)) if r.code == "busy" && r.retry_after_milliseconds == 10 => {}
        _ => return Err("public error mismatch".into()),
    }
    session.close_and_wait(Duration::from_secs(3))?;
    println!("CTX host roundtrip OK");
    Ok(())
}
