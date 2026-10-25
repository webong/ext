pub use crate::serve::{serve, serve_stdio, serve_with, Mode};
use crate::{
    frames::{Progress, ProgressOutcome},
    serve::{CallFrames, Reverse},
    wire, ContractRef, Descriptor, Error, Identity, Operation, Policy, ProgressLimits, Request,
    Result, DEFAULT_TIMEOUT, MAX_FRAME, VERSION,
};
use serde::{de::DeserializeOwned, Serialize};
use serde_json::Value;
use std::{
    collections::BTreeMap,
    io::BufRead,
    marker::PhantomData,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    time::SystemTime,
};

pub struct CallContext {
    pub deadline: SystemTime,
    pub(crate) canceled: Arc<AtomicBool>,
    pub(crate) frames: Option<CallFrames>,
}
impl CallContext {
    /// Reports one progress or log event of this call to the host. Dropped by the
    /// rate limit is `Ok(Dropped)`: routine, and the call goes on. Errors:
    /// `Unsupported` when the operation did not declare progress or the transport
    /// carries no frames (stop reporting and return the text in the result),
    /// `Invalid` for an event that breaks the rules (nothing was sent), `Closed`
    /// when the call is over or the connection ended.
    pub fn progress(&self, event: Progress) -> Result<ProgressOutcome> {
        match self.frames.as_ref().and_then(|f| f.reporter.as_ref()) {
            Some(reporter) => reporter.report(event),
            None => Err(Error::Unsupported),
        }
    }
    /// The event rate the host set for this call, with defaults filled in; None
    /// when the operation did not declare progress. A handler that streams a long
    /// output can coalesce lines to stay inside it.
    pub fn progress_limits(&self) -> Option<ProgressLimits> {
        self.frames.as_ref().and_then(|f| f.limits)
    }
    /// Calls the host's operations on behalf of this call, when the transport
    /// carries frames and the host may be asked.
    pub fn reverse(&self) -> Option<&Reverse> {
        self.frames.as_ref().and_then(|f| f.reverse.as_ref())
    }
    /// A caller that outlives this call, for background work; None where no thread
    /// reads the connection (inline mode, or a transport without frames).
    pub fn session_reverse(&self) -> Option<&Reverse> {
        self.frames.as_ref().and_then(|f| f.session.as_ref())
    }
    pub fn check(&self) -> Result<()> {
        if self.canceled.load(Ordering::Acquire) {
            Err(Error::Closed)
        } else if SystemTime::now() >= self.deadline {
            Err(Error::Deadline)
        } else {
            Ok(())
        }
    }
}
pub struct Method<I, O> {
    pub contract: ContractRef,
    pub operation: Operation,
    pub validate_input: fn(&I) -> Result<()>,
    pub validate_output: fn(&O) -> Result<()>,
    _types: PhantomData<fn(I) -> O>,
}
impl<I, O> Clone for Method<I, O> {
    fn clone(&self) -> Self {
        Self {
            contract: self.contract.clone(),
            operation: self.operation.clone(),
            validate_input: self.validate_input,
            validate_output: self.validate_output,
            _types: PhantomData,
        }
    }
}
impl<I, O> Method<I, O> {
    pub fn new(contract: ContractRef, operation: Operation) -> Self {
        Self {
            contract,
            operation,
            validate_input: |_| Ok(()),
            validate_output: |_| Ok(()),
            _types: PhantomData,
        }
    }
}
type Handler = Arc<dyn Fn(&CallContext, &Request) -> Result<Value> + Send + Sync>;
type Key = (String, String, String);
#[derive(Clone)]
pub struct Guest {
    descriptor: Descriptor,
    handlers: BTreeMap<Key, Handler>,
    authorize: Policy,
}
pub struct Registry {
    identity: Identity,
    methods: BTreeMap<Key, (Operation, Handler)>,
    reverse: Vec<wire::Contract>,
}

fn invalid_input() -> Error {
    Error::Remote(crate::RemoteError {
        code: "invalid_input".into(),
        message: "request payload does not satisfy operation contract".into(),
        retry_after_milliseconds: 0,
    })
}

impl Registry {
    pub fn new(identity: Identity) -> Result<Self> {
        identity.validate()?;
        Ok(Self {
            identity,
            methods: BTreeMap::new(),
            reverse: Vec::new(),
        })
    }
    /// Declares host operations the plugin may call (`CallContext::reverse`).
    pub fn declare_reverse(
        &mut self,
        contract: ContractRef,
        operations: Vec<Operation>,
    ) -> Result<()> {
        self.reverse.push(wire::Contract {
            name: contract.name,
            version: contract.version,
            operations,
        });
        if let Err(e) = self.descriptor().validate() {
            self.reverse.pop();
            return Err(e);
        }
        Ok(())
    }
    pub fn register<I: DeserializeOwned + 'static, O: Serialize + 'static>(
        &mut self,
        m: Method<I, O>,
        handler: impl Fn(&CallContext, &Request, I) -> Result<O> + Send + Sync + 'static,
    ) -> Result<()> {
        let key = (
            m.contract.name.clone(),
            m.contract.version.clone(),
            m.operation.name.clone(),
        );
        if self.methods.contains_key(&key) {
            return Err(Error::Invalid);
        }
        let op = m.operation.clone();
        let dispatch: Handler = Arc::new(move |ctx, req| {
            let input: I = wire::typed(req.payload.clone()).map_err(|_| invalid_input())?;
            (m.validate_input)(&input).map_err(|_| invalid_input())?;
            let out = handler(ctx, req, input)?;
            (m.validate_output)(&out)?;
            serde_json::to_value(out).map_err(|_| Error::Invalid)
        });
        self.methods.insert(key.clone(), (op, dispatch));
        if let Err(e) = self.descriptor().validate() {
            self.methods.remove(&key);
            return Err(e);
        }
        Ok(())
    }
    pub fn descriptor(&self) -> Descriptor {
        let mut contracts: Vec<wire::Contract> = Vec::new();
        for ((name, version, _), (op, _)) in &self.methods {
            if let Some(c) = contracts
                .last_mut()
                .filter(|c| c.name == *name && c.version == *version)
            {
                c.operations.push(op.clone());
            } else {
                contracts.push(wire::Contract {
                    name: name.clone(),
                    version: version.clone(),
                    operations: vec![op.clone()],
                });
            }
        }
        Descriptor {
            api_version: VERSION.into(),
            identity: self.identity.clone(),
            contracts,
            reverse: self.reverse.clone(),
        }
    }
    pub fn guest(&self, authorize: Policy) -> Result<Guest> {
        let descriptor = self.descriptor();
        descriptor.validate()?;
        Ok(Guest {
            descriptor,
            handlers: self
                .methods
                .iter()
                .map(|(k, (_, h))| (k.clone(), h.clone()))
                .collect(),
            authorize,
        })
    }
}
impl Guest {
    pub fn descriptor(&self) -> Descriptor {
        self.descriptor.clone()
    }
    pub fn invoke(&self, request: Request, canceled: Arc<AtomicBool>) -> Result<Value> {
        self.invoke_in(request, canceled, None)
    }
    pub(crate) fn invoke_in(
        &self,
        request: Request,
        canceled: Arc<AtomicBool>,
        frames: Option<CallFrames>,
    ) -> Result<Value> {
        if request.validate(&self.descriptor).is_err() {
            return wire::response(
                &request.id,
                Err(Error::Remote(crate::RemoteError {
                    code: "invalid_request".into(),
                    message: "request does not match selected contract".into(),
                    retry_after_milliseconds: 0,
                })),
            );
        }
        let deadline =
            wire::parse_deadline(&request.deadline)?.min(SystemTime::now() + DEFAULT_TIMEOUT);
        let ctx = CallContext {
            deadline,
            canceled,
            frames: frames.map(|f| f.at(deadline)),
        };
        let result = (|| {
            ctx.check()?;
            (self.authorize)(&request)?;
            ctx.check()?;
            let h = self
                .handlers
                .get(&(
                    request.contract.name.clone(),
                    request.contract.version.clone(),
                    request.operation.clone(),
                ))
                .ok_or(Error::Unsupported)?;
            let value = h(&ctx, &request)?;
            ctx.check()?;
            Ok(value)
        })();
        wire::response(&request.id, result)
    }
}
pub fn read_frame<R: BufRead>(reader: &mut R) -> Result<Option<Vec<u8>>> {
    let mut out = Vec::new();
    loop {
        let available = reader.fill_buf().map_err(|_| Error::Transport)?;
        if available.is_empty() {
            return if out.is_empty() {
                Ok(None)
            } else {
                Err(Error::Transport)
            };
        }
        let newline = available.iter().position(|b| *b == b'\n');
        let n = newline.map_or(available.len(), |n| n + 1);
        if out.len() + n > MAX_FRAME + 1 {
            return Err(Error::Invalid);
        }
        out.extend_from_slice(&available[..n]);
        reader.consume(n);
        if newline.is_some() {
            out.pop();
            return Ok(Some(out));
        }
    }
}
pub(crate) fn validate_hello(v: &Value) -> Result<()> {
    let m = v.as_object().ok_or(Error::Invalid)?;
    if m.keys().any(|k| {
        ![
            "apiVersion",
            "id",
            "operation",
            "deadline",
            "plugin",
            "contract",
            "surface",
        ]
        .contains(&k.as_str())
    }) || v["apiVersion"] != VERSION
        || v["id"] != "hello"
        || v["operation"] != "plugin.hello"
    {
        return Err(Error::Invalid);
    }
    for (name, keys) in [
        ("plugin", &["id", "revision", "version"][..]),
        ("contract", &["name", "version"][..]),
    ] {
        if let Some(value) = m.get(name) {
            let fields = value.as_object().ok_or(Error::Invalid)?;
            if fields
                .iter()
                .any(|(k, v)| !keys.contains(&k.as_str()) || v != "")
            {
                return Err(Error::Invalid);
            }
        }
    }
    if m.get("surface").is_some_and(|v| v != "") {
        return Err(Error::Invalid);
    }
    if SystemTime::now() >= wire::parse_deadline(v["deadline"].as_str().ok_or(Error::Invalid)?)? {
        return Err(Error::Deadline);
    }
    Ok(())
}
