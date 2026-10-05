use crate::{wire, Descriptor, Error, Method, Policy, Request, Result, DEFAULT_TIMEOUT, VERSION};
use serde::{de::DeserializeOwned, Serialize};
use serde_json::Value;
use std::time::{Duration, SystemTime};

/// A backend owns one connection. Implementations enforce the supplied deadline
/// or document cooperative I/O. Close must stop admission and release resources.
pub trait Backend: Send {
    fn exchange(&mut self, operation: u32, input: Vec<u8>, deadline: SystemTime)
        -> Result<Vec<u8>>;
    fn close(&mut self);
    /// Async cleanup implementations override this to observe actual disposal.
    fn wait_closed(&mut self, _timeout: Duration) -> Result<()> {
        Ok(())
    }
}
pub struct Session {
    descriptor: Descriptor,
    backend: Box<dyn Backend>,
    authorize: Policy,
    next: u64,
    closed: bool,
}
impl Session {
    /// Verification executes before connect; descriptor is owned and immutable.
    pub fn open(
        selected: Descriptor,
        verify: impl FnOnce(&Descriptor) -> Result<()>,
        connect: impl FnOnce() -> Result<Box<dyn Backend>>,
        authorize: Policy,
    ) -> Result<Self> {
        selected.validate()?;
        verify(&selected)?;
        let mut backend = connect()?;
        let deadline = SystemTime::now() + DEFAULT_TIMEOUT;
        let result = (|| {
            let bytes = backend.exchange(
                1,
                wire::encode(&serde_json::json!({"deadline":wire::timestamp(deadline)?}))?,
                deadline,
            )?;
            let actual: Descriptor = wire::typed(wire::decode(&bytes)?)?;
            selected.matches(&actual)?;
            if SystemTime::now() >= deadline {
                return Err(Error::Deadline);
            }
            Ok(())
        })();
        if let Err(e) = result {
            backend.close();
            return Err(e);
        }
        Ok(Self {
            descriptor: selected,
            backend,
            authorize,
            next: 0,
            closed: false,
        })
    }
    pub fn descriptor(&self) -> Descriptor {
        self.descriptor.clone()
    }
    pub fn call<I: Serialize, O: DeserializeOwned>(
        &mut self,
        m: &Method<I, O>,
        input: I,
    ) -> Result<O> {
        self.call_with_timeout(m, input, DEFAULT_TIMEOUT)
    }
    pub fn call_with_timeout<I: Serialize, O: DeserializeOwned>(
        &mut self,
        m: &Method<I, O>,
        input: I,
        timeout: Duration,
    ) -> Result<O> {
        (m.validate_input)(&input)?;
        let value = serde_json::to_value(input).map_err(|_| Error::Invalid)?;
        let out: O = wire::typed(self.call_raw(&m.contract, &m.operation.name, value, timeout)?)?;
        (m.validate_output)(&out)?;
        Ok(out)
    }
    pub fn call_raw(
        &mut self,
        contract: &crate::ContractRef,
        operation: &str,
        payload: Value,
        timeout: Duration,
    ) -> Result<Value> {
        if self.closed {
            return Err(Error::Closed);
        }
        let deadline = SystemTime::now()
            .checked_add(timeout.min(DEFAULT_TIMEOUT))
            .ok_or(Error::Invalid)?;
        self.next = self.next.checked_add(1).ok_or(Error::Invalid)?;
        let req = Request {
            api_version: VERSION.into(),
            id: self.next.to_string(),
            plugin: self.descriptor.identity.clone(),
            contract: contract.clone(),
            operation: operation.into(),
            surface: self.descriptor.lookup(contract, operation)?.surface.clone(),
            deadline: wire::timestamp(deadline)?,
            payload,
        };
        req.validate(&self.descriptor)?;
        (self.authorize)(&req)?;
        if SystemTime::now() >= deadline {
            return Err(Error::Deadline);
        }
        let input = wire::encode(&req)?;
        let result = (|| {
            let bytes = self.backend.exchange(2, input, deadline)?;
            let v = wire::decode(&bytes)?;
            wire::validate_response(&v, &req.id)?;
            if SystemTime::now() >= deadline {
                return Err(Error::Deadline);
            }
            Ok(v)
        })();
        let response = match result {
            Ok(v) => v,
            Err(e) => {
                self.close();
                return Err(e);
            }
        };
        if response.get("error").is_some_and(|e| !e.is_null()) {
            return Err(Error::Remote(wire::typed(response["error"].clone())?));
        }
        Ok(response["payload"].clone())
    }
    pub fn close(&mut self) {
        if !self.closed {
            self.closed = true;
            self.backend.close();
        }
    }
    pub fn close_and_wait(&mut self, timeout: Duration) -> Result<()> {
        self.close();
        self.backend.wait_closed(timeout)
    }
}
impl Drop for Session {
    fn drop(&mut self) {
        self.close();
    }
}
