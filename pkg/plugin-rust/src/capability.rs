//! Typed definitions of the SDK's optional capability contracts, with the same
//! wire shapes and validation as the Go library: `ext.health`,
//! `ext.configuration` and `ext.stream`. A guest registers handlers for these
//! methods; a host calls them through the same `Method` values.
use crate::{ContractRef, Error, Method, Operation, Result};
use serde::{Deserialize, Serialize};
use serde_json::Value;

fn contract(name: &str) -> ContractRef {
    ContractRef {
        name: name.into(),
        version: "v1".into(),
    }
}
fn operation(name: &str, surface: &str) -> Operation {
    Operation {
        name: name.into(),
        surface: surface.into(),
    }
}

/// `ext.health` `check`.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct HealthRequest {}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Health {
    pub status: String,
    pub ready: bool,
}
pub fn health_method() -> Method<HealthRequest, Health> {
    let mut m: Method<HealthRequest, Health> =
        Method::new(contract("ext.health"), operation("check", "observation"));
    m.validate_output = |h: &Health| match h.status.as_str() {
        "healthy" | "degraded" | "unhealthy" => Ok(()),
        _ => Err(Error::Invalid),
    };
    m
}

/// `ext.configuration` `update`. `values` is any JSON document the guest's own
/// schema then checks; it must not carry credentials.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct Configuration {
    pub instance: String,
    pub revision: String,
    pub values: Value,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Configured {
    pub instance: String,
    pub revision: String,
}
pub fn configuration_method() -> Method<Configuration, Configured> {
    let mut m: Method<Configuration, Configured> = Method::new(
        contract("ext.configuration"),
        operation("update", "configuration"),
    );
    m.validate_input = |c: &Configuration| {
        if c.instance.is_empty()
            || c.instance.len() > 256
            || c.revision.is_empty()
            || c.revision.len() > 256
        {
            return Err(Error::Invalid);
        }
        Ok(())
    };
    m
}

/// `ext.stream`: a pull stream of JSON items, read in numbered batches.
pub mod stream {
    use super::*;
    pub const MAX_ITEMS: usize = 256;
    pub const MAX_BATCH_BYTES: usize = 1 << 20;
    const MAX_ID: usize = 64;

    #[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
    #[serde(deny_unknown_fields)]
    pub struct OpenRequest {
        pub parameters: Value,
    }
    #[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
    #[serde(deny_unknown_fields)]
    pub struct OpenResponse {
        pub id: String,
    }
    #[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
    #[serde(deny_unknown_fields)]
    pub struct ReadRequest {
        pub id: String,
        pub sequence: u64,
        pub limit: i64,
    }
    #[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
    #[serde(deny_unknown_fields)]
    pub struct Batch {
        pub items: Vec<Value>,
        pub done: bool,
    }
    #[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
    #[serde(deny_unknown_fields)]
    pub struct CloseRequest {
        pub id: String,
    }
    #[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
    #[serde(deny_unknown_fields)]
    pub struct Empty {}

    fn stream_contract() -> ContractRef {
        contract("ext.stream")
    }
    pub fn open_method() -> Method<OpenRequest, OpenResponse> {
        Method::new(stream_contract(), operation("open", ""))
    }
    pub fn read_method() -> Method<ReadRequest, Batch> {
        let mut m: Method<ReadRequest, Batch> =
            Method::new(stream_contract(), operation("read", ""));
        m.validate_input = |r: &ReadRequest| {
            if r.id.is_empty()
                || r.id.len() > MAX_ID
                || r.sequence == 0
                || r.limit < 1
                || r.limit as usize > MAX_ITEMS
            {
                return Err(Error::Invalid);
            }
            Ok(())
        };
        m.validate_output = validate_batch;
        m
    }
    pub fn close_method() -> Method<CloseRequest, Empty> {
        let mut m: Method<CloseRequest, Empty> =
            Method::new(stream_contract(), operation("close", ""));
        m.validate_input = |r: &CloseRequest| {
            if r.id.is_empty() || r.id.len() > MAX_ID {
                return Err(Error::Invalid);
            }
            Ok(())
        };
        m
    }
    /// A batch has at most `MAX_ITEMS` items and `MAX_BATCH_BYTES` of JSON.
    pub fn validate_batch(b: &Batch) -> Result<()> {
        if b.items.len() > MAX_ITEMS {
            return Err(Error::Invalid);
        }
        let mut size = 0usize;
        for item in &b.items {
            size += serde_json::to_vec(item).map_err(|_| Error::Invalid)?.len();
            if size > MAX_BATCH_BYTES {
                return Err(Error::Invalid);
            }
        }
        Ok(())
    }
}
