//! The same guest runs natively or as a WASI Preview 1 module. No Go runtime.
use ctx_plugin_engine::{service, CallOptions, Guest, GuestReply};
use std::time::Duration;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let descriptor = include_bytes!("../../../../pkg/plugin/testdata/v1/descriptor.json");
    let guest = Guest::new(
        descriptor,
        Duration::from_secs(1),
        Box::new(|call| {
            assert!(!call.request.is_empty());
            Ok(GuestReply::Payload(b"{\"value\":7}".to_vec()))
        }),
    )?;
    service("descriptor.validate", &guest.descriptor()?)?;
    let request = br#"{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"echo","deadline":"2099-01-01T00:00:00Z","payload":{"value":7}}"#;
    let response = guest.invoke(request, CallOptions::default())?;
    let text = std::str::from_utf8(&response)?;
    assert!(text.contains("\"payload\":{\"value\":7}"), "{text}");
    println!("Rust binding with portable C guest core passed.");
    Ok(())
}
