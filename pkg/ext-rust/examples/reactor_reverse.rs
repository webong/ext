//! A reactor that calls its host (docs/plugin-reactor-abi.md, Calling the host):
//! `reactor.test/v1` `ask` makes one reverse call of `host.svc/v1`, the operation
//! and payload its input names, and returns how it came out. The WAMR backend's
//! test (`lib/plugin/wamr/tests`) and the mobile WAMR hosts run it.
use ext_io::plugin::{guest::Registry, *};
use serde_json::{json, Value};
use std::sync::Arc;

fn factory() -> Result<Guest> {
    let mut r = Registry::new(Identity {
        id: "ctx/reactor-reverse".into(),
        revision: "fixture-1".into(),
        version: String::new(),
    })?;
    let host = ContractRef {
        name: "host.svc".into(),
        version: "v1".into(),
    };
    let declared = host.clone();
    let ask = Method::<Value, Value>::new(
        ContractRef {
            name: "reactor.test".into(),
            version: "v1".into(),
        },
        Operation::new("ask"),
    );
    r.register(ask, move |ctx, _req, input| {
        let Some(reverse) = ctx.reverse() else {
            return Ok(json!({"error": {"other": "no reverse caller"}}));
        };
        let operation = input["operation"].as_str().unwrap_or("sign");
        Ok(match reverse.call(&host, operation, &input["payload"]) {
            Ok(v) => json!({"result": v}),
            Err(Error::Remote(e)) => json!({"error": {"code": e.code, "message": e.message}}),
            Err(e) => json!({"error": {"other": format!("{e:?}")}}),
        })
    })?;
    // Declared once a contract is registered: a descriptor needs one to be valid.
    r.declare_reverse(
        declared,
        ["sign", "secret", "fail"]
            .iter()
            .map(|n| Operation::new(n))
            .collect(),
    )?;
    let d = r.descriptor();
    r.guest(Arc::new(move |req| req.validate(&d)))
}

ext_io::plugin::export_guest!(factory);
