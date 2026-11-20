//! The guest registry through the Rust binding: the shared cases
//! (`lib/plugin/testdata/v1/registry-cases.json`) against the C engine's relay
//! guest (`lib/plugin/tests/registry.c`), started as `<guest> --guest <cases>
//! <name>`. Set EXT_REGISTRY_TEST_GUEST to the built `ext_registry_test` and the
//! tests run, otherwise they are skipped.
#![cfg(not(feature = "guest-only"))]
use ext_plugin_engine::{CallOptions, Endpoint, Error, Host, Process, Registry, RegistrySource};
use serde_json::Value;
use std::{process::Command, thread, time::Duration};

fn guest() -> Option<String> {
    match std::env::var("EXT_REGISTRY_TEST_GUEST") {
        Ok(path) if !path.is_empty() => Some(path),
        _ => {
            eprintln!("skipped: EXT_REGISTRY_TEST_GUEST is not set");
            None
        }
    }
}

fn cases_path() -> String {
    let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../../lib/plugin/testdata/v1/registry-cases.json");
    path.canonicalize().unwrap().to_string_lossy().into_owned()
}

fn deny(denied: bool) -> Result<(), Error> {
    if denied {
        Err(Error::DENIED)
    } else {
        Ok(())
    }
}

/// Opens the named relay guest as a source of `registry` and starts it.
fn open(
    guest: &str,
    cases: &str,
    name: &str,
    registry: &Registry,
    deny_forward: bool,
    deny_reverse: bool,
) -> Host {
    let out = Command::new(guest)
        .args(["--descriptor", cases, name])
        .output()
        .unwrap();
    assert!(out.status.success());
    let host = Host::process(
        Process {
            executable: guest,
            arguments: &["--guest", cases, name],
            environment: &[],
        },
        &out.stdout,
        Box::new(|_| Ok(())),
        Box::new(move |_| deny(deny_forward)),
    )
    .unwrap()
    .with_registry(
        registry,
        RegistrySource {
            authorize: Box::new(move |_| deny(deny_reverse)),
            timeout: Duration::ZERO,
            max_payload_bytes: 0,
            max_in_flight: 0,
        },
    )
    .unwrap();
    host.start(CallOptions::default()).unwrap();
    host
}

fn call(host: &Host, contract: &str, operation: &str, payload: &Value, timeout: Duration) -> Value {
    let input = serde_json::json!({"contract": {"name": contract, "version": "v1"}, "operation": operation, "payload": payload});
    let out = host
        .call(
            input.to_string().as_bytes(),
            CallOptions {
                timeout,
                ..CallOptions::default()
            },
        )
        .unwrap();
    let response: Value = serde_json::from_slice(&out).unwrap();
    response["payload"].clone()
}

fn names(v: &Value) -> Vec<String> {
    v.as_array()
        .map(|a| {
            a.iter()
                .filter_map(|s| s.as_str().map(String::from))
                .collect()
        })
        .unwrap_or_default()
}

fn route_all(registry: &Registry, cases: &Value) {
    for e in cases["edges"].as_array().unwrap() {
        let end = |v: &Value| {
            (
                v["name"].as_str().unwrap().to_owned(),
                v["version"].as_str().unwrap().to_owned(),
                v["operation"].as_str().unwrap().to_owned(),
            )
        };
        let (f, t) = (end(&e["from"]), end(&e["to"]));
        registry
            .route(
                Endpoint {
                    name: &f.0,
                    version: &f.1,
                    operation: &f.2,
                },
                Endpoint {
                    name: &t.0,
                    version: &t.1,
                    operation: &t.2,
                },
            )
            .unwrap();
    }
}

#[test]
fn registry_cases() {
    let Some(guest) = guest() else { return };
    let path = cases_path();
    let cases: Value = serde_json::from_str(&std::fs::read_to_string(&path).unwrap()).unwrap();
    let guests = cases["guests"].as_object().unwrap();
    let mut failures = Vec::new();
    for case in cases["cases"].as_array().unwrap() {
        let name = case["name"].as_str().unwrap();
        let registry = Registry::new().unwrap();
        let (fdeny, rdeny) = (names(&case["forwardDeny"]), names(&case["reverseDeny"]));
        let hosts: Vec<(String, String, Host)> = guests
            .iter()
            .map(|(g, contract)| {
                let contract = contract.as_str().unwrap().to_owned();
                let host = open(
                    &guest,
                    &path,
                    g,
                    &registry,
                    fdeny.contains(g),
                    rdeny.contains(g),
                );
                registry.register(&contract, "v1", &host).unwrap();
                (g.clone(), contract, host)
            })
            .collect();
        route_all(&registry, &cases);
        for (i, step) in case["steps"].as_array().unwrap().iter().enumerate() {
            if let Some(g) = step["unregister"].as_str() {
                let contract = &hosts.iter().find(|h| h.0 == g).unwrap().1;
                registry.unregister(contract, "v1").unwrap();
                continue;
            }
            let g = step["call"].as_str().unwrap();
            let (_, contract, host) = hosts.iter().find(|h| h.0 == g).unwrap();
            let timeout = Duration::from_millis(step["timeoutMs"].as_u64().unwrap_or(5000));
            let got = call(
                host,
                contract,
                step["operation"].as_str().unwrap(),
                &step["payload"],
                timeout,
            );
            let ok = match step.get("result") {
                Some(want) => &got == want,
                None => names(&step["resultError"])
                    .contains(&got["error"]["code"].as_str().unwrap_or("").to_owned()),
            };
            if !ok {
                failures.push(format!("{name} step {i}: got {got}"));
            }
        }
    }
    assert!(failures.is_empty(), "{failures:#?}");
}

#[test]
fn registry_configuration() {
    let Some(guest) = guest() else { return };
    let path = cases_path();
    let registry = Registry::new().unwrap();
    let b = open(&guest, &path, "b", &registry, false, false);
    assert_eq!(
        registry.register("Not A Name", "v1", &b),
        Err(Error::INVALID)
    );
    assert_eq!(
        registry.register("registry.other", "v1", &b),
        Err(Error::UNSUPPORTED)
    );
    assert_eq!(registry.register("registry.b", "v1", &b), Ok(()));
    assert_eq!(
        registry.register("registry.b", "v1", &b),
        Err(Error::EXISTS)
    );
    let edge = (
        Endpoint {
            name: "registry.ask",
            version: "v1",
            operation: "echo-b",
        },
        Endpoint {
            name: "registry.b",
            version: "v1",
            operation: "echo",
        },
    );
    assert_eq!(registry.route(edge.0, edge.1), Ok(()));
    assert_eq!(registry.route(edge.0, edge.1), Err(Error::EXISTS));
    assert_eq!(
        registry.route(
            Endpoint {
                operation: "other",
                ..edge.0
            },
            Endpoint {
                operation: "Bad Name",
                ..edge.1
            }
        ),
        Err(Error::INVALID)
    );
    assert_eq!(
        registry.unregister("registry.x", "v1"),
        Err(Error::NOT_FOUND)
    );
    assert_eq!(registry.unregister("registry.b", "v1"), Ok(()));
    // A host dropped while registered leaves the registry first.
    assert_eq!(registry.register("registry.b", "v1", &b), Ok(()));
    drop(b);
    assert_eq!(
        registry.unregister("registry.b", "v1"),
        Err(Error::NOT_FOUND)
    );
}

#[test]
fn registry_routes_from_many_threads() {
    let Some(guest) = guest() else { return };
    let path = cases_path();
    let registry = Registry::new().unwrap();
    let b = open(&guest, &path, "b", &registry, false, false);
    registry.register("registry.b", "v1", &b).unwrap();
    registry
        .route(
            Endpoint {
                name: "registry.ask",
                version: "v1",
                operation: "echo-b",
            },
            Endpoint {
                name: "registry.b",
                version: "v1",
                operation: "echo",
            },
        )
        .unwrap();
    let sources: Vec<Host> = (0..4)
        .map(|_| open(&guest, &path, "a", &registry, false, false))
        .collect();
    thread::scope(|s| {
        for a in &sources {
            s.spawn(move || {
                for i in 0..20 {
                    let got = call(
                        a,
                        "registry.a",
                        "relay",
                        &serde_json::json!({"operation": "echo-b", "payload": i}),
                        Duration::from_secs(10),
                    );
                    assert!(
                        got["payload"] == i || got["error"]["code"] == "reverse_unavailable",
                        "{got}"
                    );
                }
            });
        }
        for _ in 0..10 {
            thread::sleep(Duration::from_millis(5));
            registry.unregister("registry.b", "v1").unwrap();
            registry.register("registry.b", "v1", &b).unwrap();
        }
    });
}
