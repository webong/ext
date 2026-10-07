use ext_plugin::{guest::Registry, *};
use serde_json::Value;
use std::{sync::Arc, time::Duration};
pub fn factory() -> Result<Guest> {
    let mut r = Registry::new(Identity {
        id: "ctx/conformance".into(),
        revision: "fixture-1".into(),
        version: String::new(),
    })?;
    for name in ["echo", "wait", "private-error", "public-error"] {
        let method = Method::<Value, Value>::new(
            ContractRef {
                name: "ext.conformance".into(),
                version: "v1".into(),
            },
            Operation {
                name: name.into(),
                surface: String::new(),
            },
        );
        r.register(method, |ctx, req, input| match req.operation.as_str() {
            "wait" => loop {
                ctx.check()?;
                std::thread::sleep(Duration::from_millis(1));
            },
            "private-error" => Err(Error::Transport),
            "public-error" => Err(Error::Remote(RemoteError {
                code: "busy".into(),
                message: "try later".into(),
                retry_after_milliseconds: 10,
            })),
            _ => Ok(input),
        })?;
    }
    // These examples explicitly allow only their declared local fixture API.
    let d = r.descriptor();
    r.guest(Arc::new(move |req| req.validate(&d)))
}
