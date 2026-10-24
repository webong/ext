//! Progress events, reverse calls and data endpoints through the Rust binding.
//! The plugin is the C engine's scripted test guest (`lib/plugin/tests/reverse.c`),
//! started as `<guest> --guest <mode>`; set EXT_REVERSE_TEST_GUEST to the built
//! `ext_reverse_test` and the tests that need it run, otherwise they are skipped.
#![cfg(not(feature = "guest-only"))]
use ext_plugin_engine::{
    endpoint, CallOptions, Error, Host, Process, Progress, ReverseReply, Services,
};
use std::{
    process::Command,
    sync::{Arc, Mutex},
    time::Duration,
};

const REQUEST: &[u8] =
    br#"{"contract":{"name":"reverse.test","version":"v1"},"operation":"run","payload":{}}"#;

fn guest() -> Option<String> {
    match std::env::var("EXT_REVERSE_TEST_GUEST") {
        Ok(path) if !path.is_empty() => Some(path),
        _ => {
            eprintln!("skipped: EXT_REVERSE_TEST_GUEST is not set");
            None
        }
    }
}

fn descriptor(guest: &str) -> Vec<u8> {
    let out = Command::new(guest).arg("--descriptor").output().unwrap();
    assert!(out.status.success());
    out.stdout
}

fn open(guest: &str, mode: &str, services: Option<Services>) -> Host {
    let host = Host::process(
        Process {
            executable: guest,
            arguments: &["--guest", mode],
            environment: &[],
        },
        &descriptor(guest),
        Box::new(|_| Ok(())),
        Box::new(|_| Ok(())),
    )
    .unwrap();
    let host = match services {
        Some(s) => host.with_services(s).unwrap(),
        None => host,
    };
    host.start(CallOptions::default()).unwrap();
    host
}

#[derive(Default)]
struct Seen {
    authorized: Vec<String>,
    handled: Vec<String>,
}

/// Services that record what they are shown. `authorize` denies the operation
/// "secret"; `handle` answers by operation.
fn services(seen: &Arc<Mutex<Seen>>) -> Services {
    let (a, h) = (seen.clone(), seen.clone());
    Services {
        authorize: Box::new(move |call| {
            let text = String::from_utf8_lossy(call.request).into_owned();
            a.lock().unwrap().authorized.push(text.clone());
            assert!(!call.is_canceled());
            assert!(call.remaining > Duration::ZERO);
            if text.contains("\"operation\":\"secret\"") {
                Err(Error::DENIED)
            } else {
                Ok(())
            }
        }),
        handle: Box::new(move |call| {
            let text = String::from_utf8_lossy(call.request).into_owned();
            h.lock().unwrap().handled.push(text.clone());
            if text.contains("\"operation\":\"sign\"") {
                Ok(ReverseReply::Payload(br#""signed""#.to_vec()))
            } else if text.contains("\"operation\":\"empty\"") {
                Ok(ReverseReply::Payload(Vec::new()))
            } else if text.contains("\"operation\":\"errok\"") {
                Ok(ReverseReply::PublicError(
                    br#"{"code":"policy","message":"refused for you","retryAfterMilliseconds":5}"#
                        .to_vec(),
                ))
            } else if text.contains("\"operation\":\"fail\"") {
                Err(Error(7))
            } else {
                panic!("a hook that panics is a private failure")
            }
        }),
        timeout: Duration::from_millis(150),
        max_payload_bytes: 64,
        max_in_flight: 0,
    }
}

fn body(host: &Host) -> Result<String, Error> {
    host.call(REQUEST, CallOptions::default())
        .map(|b| String::from_utf8(b).unwrap())
}

#[test]
fn reverse_calls_are_answered_by_the_hooks() {
    let Some(guest) = guest() else { return };
    let seen = Arc::new(Mutex::new(Seen::default()));
    // The request the hooks are shown, and the answer the plugin got.
    let host = open(&guest, "call:sign", Some(services(&seen)));
    let text = body(&host).unwrap();
    assert!(text.contains(r#""id":"g1","payload":"signed""#), "{text}");
    {
        let seen = seen.lock().unwrap();
        assert_eq!((seen.authorized.len(), seen.handled.len()), (1, 1));
        let request = &seen.handled[0];
        assert!(request.contains(r#""plugin":{"id":"reverse/guest","revision":"r1"}"#));
        assert!(request.contains(r#""surface":"signing""#));
        assert!(request.contains(r#""payload":{"n":1}"#));
        assert!(request.contains(r#""parent":"1""#), "{request}");
    }
    drop(host);
    // Every way a hook can answer, and what the plugin sees.
    for (mode, want) in [
        ("call:empty", r#""payload":null"#),
        (
            "call:errok",
            r#""code":"policy","message":"refused for you","retryAfterMilliseconds":5"#,
        ),
        ("call:fail", r#""code":"operation_failed""#),
        ("call:badcase", r#""code":"reverse_undeclared""#),
    ] {
        let host = open(&guest, mode, Some(services(&seen)));
        let text = body(&host).unwrap();
        assert!(text.contains(want), "{mode}: {text}");
    }
    // A hook that panics is contained and reaches the plugin as a private failure.
    let host = open(&guest, "call:slow", Some(services(&seen)));
    assert!(body(&host)
        .unwrap()
        .contains(r#""code":"operation_failed""#));
}

#[test]
fn reverse_policy_and_refusals() {
    let Some(guest) = guest() else { return };
    let seen = Arc::new(Mutex::new(Seen::default()));
    // The policy denies before the handler runs.
    let host = open(&guest, "call:secret", Some(services(&seen)));
    assert!(body(&host).unwrap().contains(r#""code":"denied""#));
    assert_eq!(seen.lock().unwrap().handled.len(), 0);
    drop(host);
    // The engine refuses what the plugin did not declare without asking the hooks.
    seen.lock().unwrap().authorized.clear();
    let host = open(&guest, "call:nothere", Some(services(&seen)));
    assert!(body(&host)
        .unwrap()
        .contains(r#""code":"reverse_undeclared""#));
    assert_eq!(seen.lock().unwrap().authorized.len(), 0);
    drop(host);
    // A host that offers no services answers every request as unavailable.
    let host = open(&guest, "call:sign", None);
    assert!(body(&host)
        .unwrap()
        .contains(r#""code":"reverse_unavailable""#));
}

#[test]
fn a_plugin_that_breaks_the_framing_ends_the_session() {
    let Some(guest) = guest() else { return };
    let seen = Arc::new(Mutex::new(Seen::default()));
    for mode in ["wrongparent", "bad-id", "bad-extra"] {
        let host = open(&guest, mode, Some(services(&seen)));
        assert_eq!(body(&host), Err(Error::PROTOCOL), "{mode}");
        // The host is failed: it serves no more calls.
        assert_ne!(host.state(), 1, "{mode}");
        assert!(body(&host).is_err(), "{mode}");
    }
}

#[test]
fn progress_events_arrive_in_order_before_the_result() {
    let Some(guest) = guest() else { return };
    let seen = Arc::new(Mutex::new(Seen::default()));
    let events = Mutex::new(Vec::<String>::new());
    let on_event = |event: &[u8]| {
        events
            .lock()
            .unwrap()
            .push(String::from_utf8_lossy(event).into_owned())
    };
    // Events and a reverse call on the same connection.
    let host = open(&guest, "interleave", Some(services(&seen)));
    let progress = Progress {
        on_event: Some(&on_event),
        ..Progress::default()
    };
    let out = host
        .call_progress(REQUEST, CallOptions::default(), &progress)
        .unwrap();
    let text = String::from_utf8(out).unwrap();
    assert!(text.contains(r#""payload":"signed""#), "{text}");
    {
        let events = events.lock().unwrap();
        assert_eq!(events.len(), 2, "{events:?}");
        assert_eq!(events[0], r#"{"kind":"log","text":"one"}"#);
        assert_eq!(events[1], r#"{"kind":"log","text":"two"}"#);
    }
    // Without a callback the events are limited and discarded, and the call works.
    let out = host
        .call_progress(REQUEST, CallOptions::default(), &Progress::default())
        .unwrap();
    assert!(String::from_utf8(out)
        .unwrap()
        .contains(r#""payload":"signed""#));
    assert_eq!(events.lock().unwrap().len(), 2);
    // A plain call is the same call without events.
    assert!(body(&host).unwrap().contains(r#""payload":"signed""#));
}

/// The raw JSON of the end of the value that starts at `i`.
fn value_end(b: &[u8], mut i: usize) -> usize {
    let (mut depth, mut quoted) = (0usize, false);
    while i < b.len() {
        let c = b[i];
        if quoted {
            if c == b'\\' {
                i += 1;
            } else if c == b'"' {
                quoted = false;
                if depth == 0 {
                    return i + 1;
                }
            }
        } else {
            match c {
                b'"' => quoted = true,
                b'{' | b'[' => depth += 1,
                b'}' | b']' => {
                    if depth == 0 {
                        return i;
                    }
                    depth -= 1;
                    if depth == 0 {
                        return i + 1;
                    }
                }
                b',' if depth == 0 => return i,
                _ => {}
            }
        }
        i += 1;
    }
    i
}

/// The raw JSON text of a top-level member of the object `s`.
fn member<'a>(s: &'a str, key: &str) -> &'a str {
    let b = s.as_bytes();
    let mut i = s.find('{').unwrap() + 1;
    loop {
        while b[i].is_ascii_whitespace() || b[i] == b',' {
            i += 1;
        }
        assert_eq!(b[i], b'"', "{s}");
        let end = i + 1 + s[i + 1..].find('"').unwrap();
        let name = &s[i + 1..end];
        i = end + 1;
        while b[i].is_ascii_whitespace() || b[i] == b':' {
            i += 1;
        }
        let stop = value_end(b, i);
        if name == key {
            return &s[i..stop];
        }
        i = stop;
    }
}

fn outcome(name: &str) -> Result<(), Error> {
    match name.trim_matches('"') {
        "ok" => Ok(()),
        "invalid" => Err(Error::INVALID),
        "denied" => Err(Error::DENIED),
        "expired" => Err(Error::EXPIRED),
        other => panic!("unknown outcome {other}"),
    }
}

/// Every outcome of the shared fixture, which each binding must reproduce.
#[test]
fn endpoint_fixture() {
    let fixture = include_str!("../../../../lib/plugin/testdata/v1/endpoint-cases.json");
    let mut counts = [0usize; 4];
    let mut total = 0;
    for line in fixture
        .lines()
        .map(str::trim)
        .filter(|l| l.starts_with("{\"name\""))
    {
        let line = line.trim_end_matches(',');
        let name = member(line, "name");
        let (endpoint_json, policy) = (member(line, "endpoint"), member(line, "policy"));
        let (check, validate) = (
            outcome(member(line, "check")),
            outcome(member(line, "validate")),
        );
        assert_eq!(
            endpoint::check(endpoint_json.as_bytes()),
            check,
            "check: {name}"
        );
        assert_eq!(
            endpoint::validate(endpoint_json.as_bytes(), policy.as_bytes()),
            validate,
            "validate: {name}"
        );
        counts[match validate {
            Ok(()) => 0,
            Err(Error::INVALID) => 1,
            Err(Error::DENIED) => 2,
            _ => 3,
        }] += 1;
        total += 1;
    }
    assert_eq!(total, 213);
    // A fixture that states one kind of outcome proves little.
    assert!(counts.iter().all(|n| *n >= 5), "{counts:?}");
}

#[test]
fn endpoint_inputs_that_are_not_endpoints() {
    assert_eq!(endpoint::check(b"[]"), Err(Error::INVALID));
    assert_eq!(endpoint::check(b""), Err(Error::INVALID));
    assert_eq!(endpoint::validate(b"{}", b"[]"), Err(Error::INVALID));
    // A policy that names no scheme admits nothing.
    let endpoint_json =
        br#"{"scheme":"ws","address":"localhost:80","expiresAt":"2030-01-01T00:30:00Z"}"#;
    assert_eq!(endpoint::check(endpoint_json), Ok(()));
    assert_eq!(
        endpoint::validate(endpoint_json, br#"{"now":"2030-01-01T00:00:00Z"}"#),
        Err(Error::DENIED)
    );
    assert_eq!(
        endpoint::validate(
            endpoint_json,
            br#"{"schemes":["ws"],"now":"2030-01-01T00:00:00Z"}"#
        ),
        Ok(())
    );
}
