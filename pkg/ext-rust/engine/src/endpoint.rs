//! Loopback data endpoints: the engine's rules for admitting an endpoint a host
//! hands a plugin (`endpoint.check` and `endpoint.validate`, see
//! lib/plugin/services.md). An outcome is a status and never text, because an
//! endpoint is a credential: [`Error::INVALID`] for a malformed one,
//! [`Error::DENIED`] for one the policy does not admit and [`Error::EXPIRED`] for
//! one that has run out. Granting the endpoint (its lifetime, one-time tokens,
//! dialing) stays with the caller.
use crate::{service, Error};

/// Checks the shape rules alone: strict decoding, scheme, loopback address, path,
/// token and expiry. `endpoint` is the endpoint as JSON.
pub fn check(endpoint: &[u8]) -> Result<(), Error> {
    service("endpoint.check", endpoint).map(|_| ())
}

/// Checks the shape rules and the policy. `policy` is the JSON of
/// `{"schemes":[...],"maxLifetimeMilliseconds":n,"now":"RFC 3339"}`; every field
/// is optional, and a policy that names no scheme admits nothing.
pub fn validate(endpoint: &[u8], policy: &[u8]) -> Result<(), Error> {
    let mut input = Vec::with_capacity(endpoint.len() + policy.len() + 32);
    input.extend_from_slice(br#"{"endpoint":"#);
    input.extend_from_slice(endpoint);
    input.extend_from_slice(br#","policy":"#);
    input.extend_from_slice(policy);
    input.push(b'}');
    service("endpoint.validate", &input).map(|_| ())
}
