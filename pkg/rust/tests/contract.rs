use ctx_plugin::{
    guest,
    host::{Backend, Session},
    wire, *,
};
use serde::{Deserialize, Serialize};
use std::{
    sync::{
        atomic::{AtomicUsize, Ordering},
        Arc,
    },
    time::{Duration, SystemTime},
};
fn fixture() -> Descriptor {
    wire::typed(
        wire::decode(include_bytes!(
            "../../../pkg/plugin/testdata/v1/descriptor.json"
        ))
        .unwrap(),
    )
    .unwrap()
}
#[test]
fn frozen_wire_and_strict_json() {
    let d = fixture();
    d.validate().unwrap();
    d.matches(&d).unwrap();
    let invalid: Vec<String> =
        serde_json::from_str(include_str!("../../../pkg/plugin/testdata/v1/invalid.json")).unwrap();
    for s in invalid {
        assert!(wire::decode(s.as_bytes()).is_err(), "{s}");
    }
    assert!(wire::decode(&[255]).is_err());
    assert!(wire::decode(format!("{}0{}", "[".repeat(66), "]".repeat(66)).as_bytes()).is_err());
    let mut v = serde_json::to_value(d.clone()).unwrap();
    v["extra"] = true.into();
    assert!(wire::typed::<Descriptor>(v).is_err());
    let mut wrong = d.clone();
    wrong.identity.revision = "wrong".into();
    assert!(d.matches(&wrong).is_err());
    let mut reordered = d.clone();
    reordered.contracts[0].operations.reverse();
    d.matches(&reordered).unwrap();
    assert!(wire::validate_response(
        &serde_json::json!({"apiVersion":VERSION,"id":"1","payload":null}),
        "1"
    )
    .is_ok());
    assert!(wire::validate_response(&serde_json::json!({"apiVersion":VERSION,"id":"1","payload":null,"error":{"code":"bad","message":"bad"}}),"1").is_err());
}
#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Message {
    text: String,
}
fn method() -> Method<Message, Message> {
    let mut m = Method::new(
        ContractRef {
            name: "example.echo".into(),
            version: "v1".into(),
        },
        Operation {
            name: "echo".into(),
            surface: String::new(),
        },
    );
    m.validate_input = |v: &Message| {
        if v.text.len() > 8 {
            Err(Error::Invalid)
        } else {
            Ok(())
        }
    };
    m
}
fn registry() -> Registry {
    let mut r = Registry::new(Identity {
        id: "example/typed".into(),
        revision: "1".into(),
        version: String::new(),
    })
    .unwrap();
    r.register(method(), |_, _, input| Ok(input)).unwrap();
    r
}
struct Local {
    g: Guest,
    closed: Arc<AtomicUsize>,
}
impl Backend for Local {
    fn exchange(&mut self, op: u32, input: Vec<u8>, _: SystemTime) -> Result<Vec<u8>> {
        if op == 1 {
            wire::encode(&self.g.descriptor())
        } else {
            wire::encode(&self.g.invoke(
                wire::typed(wire::decode(&input)?)?,
                Arc::new(std::sync::atomic::AtomicBool::new(false)),
            )?)
        }
    }
    fn close(&mut self) {
        self.closed.fetch_add(1, Ordering::SeqCst);
    }
}
#[test]
fn typed_host_guest_policy_and_cleanup() {
    let r = registry();
    let g = r.guest(Arc::new(|_| Ok(()))).unwrap();
    let closed = Arc::new(AtomicUsize::new(0));
    let mut s = Session::open(
        r.descriptor(),
        |_| Ok(()),
        || {
            Ok(Box::new(Local {
                g,
                closed: closed.clone(),
            }))
        },
        Arc::new(|_| Ok(())),
    )
    .unwrap();
    assert_eq!(
        s.call(
            &method(),
            Message {
                text: "hello".into()
            }
        )
        .unwrap()
        .text,
        "hello"
    );
    assert!(s
        .call(
            &method(),
            Message {
                text: "too long input".into()
            }
        )
        .is_err());
    s.close();
    s.close();
    assert_eq!(closed.load(Ordering::SeqCst), 1);
    assert!(s
        .call(
            &method(),
            Message {
                text: "hello".into()
            }
        )
        .is_err());
    let called = AtomicUsize::new(0);
    assert!(Session::open(
        r.descriptor(),
        |_| Err(Error::Denied),
        || {
            called.fetch_add(1, Ordering::SeqCst);
            Err(Error::Transport)
        },
        Arc::new(|_| Ok(()))
    )
    .is_err());
    assert_eq!(called.load(Ordering::SeqCst), 0);
}
#[test]
fn guest_denial_and_cabi_ownership() {
    fn factory() -> Result<Guest> {
        registry().guest(Arc::new(|_| Err(Error::Denied)))
    }
    let server = cabi::Server::new(factory, 1);
    let id = server.open();
    assert_ne!(id, 0);
    assert_eq!(server.open(), 0);
    let hello=wire::encode(&serde_json::json!({"deadline":wire::timestamp(SystemTime::now()+Duration::from_secs(5)).unwrap()})).unwrap();
    let d: Descriptor =
        wire::typed(wire::decode(&server.call(id, 1, &hello).unwrap()).unwrap()).unwrap();
    let req = Request {
        api_version: VERSION.into(),
        id: "1".into(),
        plugin: d.identity,
        contract: method().contract,
        operation: "echo".into(),
        surface: String::new(),
        deadline: wire::timestamp(SystemTime::now() + Duration::from_secs(5)).unwrap(),
        payload: serde_json::json!({"text":"hello"}),
    };
    let v = wire::decode(&server.call(id, 2, &wire::encode(&req).unwrap()).unwrap()).unwrap();
    assert_eq!(v["error"]["code"], "operation_failed");
    server.close(id);
    assert!(server.call(id, 1, &hello).is_err());
    assert_ne!(server.open(), 0);
    let mut len = 99;
    unsafe {
        assert_ne!(
            server.call_into(
                id,
                1,
                std::ptr::null(),
                0,
                std::ptr::null_mut(),
                0,
                &mut len
            ),
            0
        );
    }
    assert_eq!(len, 0);
    let mut input = std::io::Cursor::new(b"{}".to_vec());
    assert!(guest::read_frame(&mut input).is_err());
}
