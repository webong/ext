//! The frames conformance guest (lib/plugin/testdata/v1/frames-descriptor.json,
//! docs/plugin-guest-frames.md): reports progress and calls its host. It serves
//! standard input and output, natively or as a WASI module; without threads it
//! answers the operations that need them with the public error "unsupported".
use ext_plugin::{
    frames::{split_progress_text, Progress, ProgressOutcome},
    guest::{serve_with, Registry},
    Mode, *,
};
use serde_json::{json, Value};
use std::{
    sync::{mpsc, Arc, Mutex},
    time::Duration,
};

const FRAMES: (&str, &str) = ("ext.frames", "v1");
const HOST: (&str, &str) = ("host.svc", "v1");

fn reference(c: (&str, &str)) -> ContractRef {
    ContractRef {
        name: c.0.into(),
        version: c.1.into(),
    }
}
fn public(code: &str) -> Error {
    Error::Remote(RemoteError {
        code: code.into(),
        message: "not supported here".into(),
        retry_after_milliseconds: 0,
    })
}
// How a reverse call came out, in the shape the suite reads.
fn outcome(result: Result<Value>) -> Value {
    match result {
        Ok(v) => json!({"result": v}),
        Err(Error::Remote(r)) => {
            let mut e = json!({"code": r.code, "message": r.message});
            if r.retry_after_milliseconds != 0 {
                e["retry"] = r.retry_after_milliseconds.into();
            }
            json!({"error": e})
        }
        Err(Error::Closed) => json!({"error": {"other": "closed"}}),
        Err(Error::Deadline) => json!({"error": {"other": "deadline"}}),
        Err(_) => json!({"error": {"other": "failed"}}),
    }
}
fn number(input: &Value, key: &str, default: u64) -> u64 {
    input.get(key).and_then(Value::as_u64).unwrap_or(default)
}

pub fn run(mode: Mode) -> std::result::Result<(), Box<dyn std::error::Error>> {
    let mut registry = Registry::new(Identity {
        id: "ctx/frames".into(),
        revision: "fixture-1".into(),
        version: String::new(),
    })?;
    // Outcomes of session-scoped calls, from background work to "collect".
    let background = Arc::new((
        Mutex::new(mpsc::channel::<Value>().0),
        Mutex::new(None::<mpsc::Receiver<Value>>),
    ));
    {
        let (tx, rx) = mpsc::channel();
        *background.0.lock().unwrap() = tx;
        *background.1.lock().unwrap() = Some(rx);
    }
    for (name, progress) in [
        ("stream", true),
        ("flood", true),
        ("limits", true),
        ("split", true),
        ("plain", false),
        ("call", false),
        ("twice", false),
        ("idle", false),
        ("collect", false),
        ("burst", false),
    ] {
        let method = Method::<Value, Value>::new(
            reference(FRAMES),
            Operation {
                progress,
                ..Operation::new(name)
            },
        );
        let background = background.clone();
        registry.register(method, move |ctx, req, input| {
            let host = reference(HOST);
            match req.operation.as_str() {
                "stream" => {
                    let _ = ctx.progress(Progress::fraction(1, 3));
                    let _ = ctx.progress(Progress::out("hello"));
                    let _ = ctx.progress(Progress::err("oops"));
                    Ok(json!({"done": true}))
                }
                "flood" => {
                    let mut dropped = 0u64;
                    for _ in 0..number(&input, "count", 300) {
                        if matches!(
                            ctx.progress(Progress::log("x")),
                            Ok(ProgressOutcome::Dropped)
                        ) {
                            dropped += 1;
                        }
                    }
                    Ok(json!({"dropped": dropped}))
                }
                "limits" => {
                    let l = ctx.progress_limits();
                    Ok(json!({
                        "declared": l.is_some(),
                        "events": l.map_or(0, |l| l.events_per_second),
                        "bytes": l.map_or(0, |l| l.bytes_per_second),
                    }))
                }
                "split" => {
                    let text = input["text"].as_str().ok_or(Error::Invalid)?;
                    let pieces = split_progress_text(text);
                    for piece in &pieces {
                        let _ = ctx.progress(Progress::out(piece));
                    }
                    Ok(json!({"pieces": pieces.len()}))
                }
                "plain" => {
                    let r = ctx.progress(Progress::log("never sent"));
                    Ok(json!({
                        "unsupported": matches!(r, Err(Error::Unsupported)),
                        "limits": ctx.progress_limits().is_some(),
                    }))
                }
                "call" => {
                    let Some(reverse) = ctx.reverse() else {
                        return Ok(json!({"error": {"other": "unsupported"}}));
                    };
                    let op = input["op"].as_str().ok_or(Error::Invalid)?;
                    Ok(outcome(reverse.call(&host, op, &input["payload"])))
                }
                "twice" => {
                    let reverse = ctx.reverse().ok_or_else(|| public("unsupported"))?;
                    let first = reverse.call(&host, "sign", &json!(1))?;
                    let second = reverse.call(&host, "sign", &json!(2))?;
                    Ok(json!({"first": first, "second": second}))
                }
                "idle" => {
                    let session = ctx
                        .session_reverse()
                        .ok_or_else(|| public("unsupported"))?
                        .clone();
                    let tx = background.0.lock().unwrap().clone();
                    std::thread::spawn(move || {
                        std::thread::sleep(Duration::from_millis(20));
                        let _ = tx.send(outcome(session.call(&host, "sign", &json!({"n": 1}))));
                    });
                    Ok(json!({"started": true}))
                }
                "collect" => {
                    ctx.session_reverse().ok_or_else(|| public("unsupported"))?;
                    let rx = background.1.lock().unwrap();
                    Ok(rx
                        .as_ref()
                        .and_then(|rx| rx.recv_timeout(Duration::from_secs(5)).ok())
                        .unwrap_or_else(|| json!({"timeout": true})))
                }
                "burst" => {
                    let reverse = ctx
                        .reverse()
                        .filter(|r| r.concurrent())
                        .ok_or_else(|| public("unsupported"))?;
                    let counts = Mutex::new(std::collections::BTreeMap::<String, u64>::new());
                    std::thread::scope(|scope| {
                        for _ in 0..6 {
                            scope.spawn(|| {
                                let key = match reverse.call(&host, "hold", &Value::Null) {
                                    Ok(_) => "ok".to_string(),
                                    Err(Error::Remote(r)) => r.code,
                                    Err(_) => "other".to_string(),
                                };
                                *counts.lock().unwrap().entry(key).or_default() += 1;
                            });
                        }
                    });
                    Ok(json!(counts.into_inner().unwrap()))
                }
                _ => Err(Error::Unsupported),
            }
        })?;
    }
    registry.declare_reverse(
        reference(HOST),
        [
            "sign",
            "secret",
            "empty",
            "fail",
            "errok",
            "errretryok",
            "big",
            "hold",
        ]
        .iter()
        .map(|name| Operation {
            surface: if *name == "sign" {
                "signing".into()
            } else {
                String::new()
            },
            ..Operation::new(name)
        })
        .collect(),
    )?;
    // These examples explicitly allow only their declared local fixture API.
    let d = registry.descriptor();
    let guest = registry.guest(Arc::new(move |req| req.validate(&d)))?;
    serve_with(
        &guest,
        std::io::BufReader::new(std::io::stdin()),
        std::io::stdout(),
        mode,
    )?;
    Ok(())
}
