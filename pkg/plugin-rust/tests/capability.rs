use ext_plugin::capability::{self, stream, Configuration, Health, HealthRequest};
use ext_plugin::Error;
use serde_json::json;

// The JSON below is what the Go library puts on the wire for the same values.
#[test]
fn health_matches_the_go_wire_shape() {
    let m = capability::health_method();
    assert_eq!(m.contract.name, "ext.health");
    assert_eq!(m.contract.version, "v1");
    assert_eq!(m.operation.name, "check");
    assert_eq!(m.operation.surface, "observation");
    assert_eq!(serde_json::to_value(HealthRequest {}).unwrap(), json!({}));
    let ok: Health = serde_json::from_value(json!({"status":"degraded","ready":false})).unwrap();
    (m.validate_output)(&ok).unwrap();
    for status in ["", "great", "HEALTHY"] {
        let bad = Health {
            status: status.into(),
            ready: true,
        };
        assert!(matches!((m.validate_output)(&bad), Err(Error::Invalid)), "{status}");
    }
    assert!(serde_json::from_value::<Health>(json!({"status":"healthy"})).is_err());
    assert!(serde_json::from_value::<Health>(json!({"status":"healthy","ready":true,"x":1})).is_err());
}

#[test]
fn configuration_validates_like_go() {
    let m = capability::configuration_method();
    assert_eq!(m.operation.surface, "configuration");
    let c = |instance: &str, revision: &str| Configuration {
        instance: instance.into(),
        revision: revision.into(),
        values: json!({"name":"a"}),
    };
    (m.validate_input)(&c("one", "r1")).unwrap();
    (m.validate_input)(&c(&"x".repeat(256), &"r".repeat(256))).unwrap();
    for bad in [c("", "r1"), c("one", ""), c(&"x".repeat(257), "r1"), c("one", &"r".repeat(257))] {
        assert!(matches!((m.validate_input)(&bad), Err(Error::Invalid)));
    }
    let wire = serde_json::to_value(c("one", "r1")).unwrap();
    assert_eq!(wire, json!({"instance":"one","revision":"r1","values":{"name":"a"}}));
}

#[test]
fn stream_limits_match_go() {
    let read = stream::read_method();
    let r = |id: &str, sequence: u64, limit: i64| stream::ReadRequest {
        id: id.into(),
        sequence,
        limit,
    };
    (read.validate_input)(&r("s1", 1, 1)).unwrap();
    (read.validate_input)(&r(&"i".repeat(64), 7, stream::MAX_ITEMS as i64)).unwrap();
    for bad in [
        r("", 1, 1),
        r(&"i".repeat(65), 1, 1),
        r("s1", 0, 1),
        r("s1", 1, 0),
        r("s1", 1, -1),
        r("s1", 1, stream::MAX_ITEMS as i64 + 1),
    ] {
        assert!(matches!((read.validate_input)(&bad), Err(Error::Invalid)), "{bad:?}");
    }
    let close = stream::close_method();
    (close.validate_input)(&stream::CloseRequest { id: "s".into() }).unwrap();
    assert!(matches!((close.validate_input)(&stream::CloseRequest { id: "".into() }), Err(Error::Invalid)));

    let batch = |n: usize| stream::Batch {
        items: (0..n).map(|i| json!({"v": i})).collect(),
        done: false,
    };
    stream::validate_batch(&batch(stream::MAX_ITEMS)).unwrap();
    assert!(stream::validate_batch(&batch(stream::MAX_ITEMS + 1)).is_err());
    let big = json!("x".repeat(600 << 10));
    let oversize = stream::Batch { items: vec![big.clone(), big], done: false };
    assert!(stream::validate_batch(&oversize).is_err());
    assert_eq!(
        serde_json::to_value(stream::Batch { items: vec![], done: true }).unwrap(),
        json!({"items":[],"done":true})
    );
    assert_eq!(serde_json::to_value(stream::Empty {}).unwrap(), json!({}));
    assert_eq!(serde_json::to_value(r("s", 2, 3)).unwrap(), json!({"id":"s","sequence":2,"limit":3}));
}
