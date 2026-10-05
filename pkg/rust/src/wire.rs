use crate::{Error, Result, MAX_FRAME, VERSION};
use serde::{
    de::{self, DeserializeSeed, MapAccess, SeqAccess, Visitor},
    Deserialize, Serialize,
};
use serde_json::{Map, Value};
use std::{
    collections::HashSet,
    fmt,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

// Validate duplicates before deserializing typed envelopes (including escaped
// aliases and nested payload keys). Value's ordinary decoder is last-key-wins.
struct Strict(usize);
impl<'de> DeserializeSeed<'de> for Strict {
    type Value = Value;
    fn deserialize<D: de::Deserializer<'de>>(self, d: D) -> std::result::Result<Value, D::Error> {
        if self.0 > 64 {
            return Err(de::Error::custom("JSON nesting exceeds 64"));
        }
        d.deserialize_any(self)
    }
}
impl<'de> Visitor<'de> for Strict {
    type Value = Value;
    fn expecting(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.write_str("bounded JSON")
    }
    fn visit_bool<E: de::Error>(self, v: bool) -> std::result::Result<Value, E> {
        Ok(v.into())
    }
    fn visit_i64<E: de::Error>(self, v: i64) -> std::result::Result<Value, E> {
        Ok(v.into())
    }
    fn visit_u64<E: de::Error>(self, v: u64) -> std::result::Result<Value, E> {
        Ok(v.into())
    }
    fn visit_f64<E: de::Error>(self, v: f64) -> std::result::Result<Value, E> {
        serde_json::Number::from_f64(v)
            .map(Value::Number)
            .ok_or_else(|| E::custom("nonfinite number"))
    }
    fn visit_str<E: de::Error>(self, v: &str) -> std::result::Result<Value, E> {
        Ok(v.into())
    }
    fn visit_string<E: de::Error>(self, v: String) -> std::result::Result<Value, E> {
        Ok(v.into())
    }
    fn visit_unit<E: de::Error>(self) -> std::result::Result<Value, E> {
        Ok(Value::Null)
    }
    fn visit_seq<A: SeqAccess<'de>>(self, mut seq: A) -> std::result::Result<Value, A::Error> {
        let mut out = Vec::new();
        while let Some(v) = seq.next_element_seed(Strict(self.0 + 1))? {
            out.push(v);
        }
        Ok(out.into())
    }
    fn visit_map<A: MapAccess<'de>>(self, mut map: A) -> std::result::Result<Value, A::Error> {
        let mut out = Map::new();
        while let Some(k) = map.next_key::<String>()? {
            if out.contains_key(&k) {
                return Err(de::Error::custom("duplicate JSON key"));
            }
            out.insert(k, map.next_value_seed(Strict(self.0 + 1))?);
        }
        Ok(out.into())
    }
}
pub fn decode(data: &[u8]) -> Result<Value> {
    if data.len() > MAX_FRAME || std::str::from_utf8(data).is_err() {
        return Err(Error::Invalid);
    }
    let mut d = serde_json::Deserializer::from_slice(data);
    let v = Strict(0).deserialize(&mut d).map_err(|_| Error::Invalid)?;
    d.end().map_err(|_| Error::Invalid)?;
    Ok(v)
}
pub fn encode<T: Serialize>(v: &T) -> Result<Vec<u8>> {
    let bytes = serde_json::to_vec(v).map_err(|_| Error::Invalid)?;
    decode(&bytes)?;
    Ok(bytes)
}
pub fn typed<T: de::DeserializeOwned>(value: Value) -> Result<T> {
    serde_json::from_value(value).map_err(|_| Error::Invalid)
}
pub fn identifier(v: &str, revision: bool) -> bool {
    !v.is_empty()
        && v.len() <= 256
        && v.as_bytes()[0].is_ascii_alphanumeric()
        && v.bytes().all(|b| {
            b.is_ascii_lowercase()
                || b.is_ascii_digit()
                || b"._/-".contains(&b)
                || revision && (b.is_ascii_uppercase() || b"+:".contains(&b))
        })
        && (revision || !v.as_bytes()[0].is_ascii_uppercase())
}
fn empty(v: &str) -> bool {
    v.is_empty()
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Identity {
    pub id: String,
    pub revision: String,
    #[serde(default, skip_serializing_if = "empty")]
    pub version: String,
}
impl Identity {
    pub fn validate(&self) -> Result<()> {
        if identifier(&self.id, false)
            && identifier(&self.revision, true)
            && (self.version.is_empty() || identifier(&self.version, true))
        {
            Ok(())
        } else {
            Err(Error::Invalid)
        }
    }
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ContractRef {
    pub name: String,
    pub version: String,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Operation {
    pub name: String,
    #[serde(default, skip_serializing_if = "empty")]
    pub surface: String,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Contract {
    pub name: String,
    pub version: String,
    pub operations: Vec<Operation>,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Descriptor {
    #[serde(rename = "apiVersion")]
    pub api_version: String,
    pub identity: Identity,
    pub contracts: Vec<Contract>,
}
impl Descriptor {
    pub fn validate(&self) -> Result<()> {
        self.identity.validate()?;
        if self.api_version != VERSION || self.contracts.is_empty() || self.contracts.len() > 64 {
            return Err(Error::Invalid);
        }
        let mut refs = HashSet::new();
        for c in &self.contracts {
            if !identifier(&c.name, false)
                || !identifier(&c.version, true)
                || !refs.insert((&c.name, &c.version))
                || c.operations.is_empty()
                || c.operations.len() > 256
            {
                return Err(Error::Invalid);
            }
            let mut ops = HashSet::new();
            for op in &c.operations {
                if !identifier(&op.name, false)
                    || op.name == "plugin.hello"
                    || !ops.insert(&op.name)
                    || (!op.surface.is_empty() && !identifier(&op.surface, false))
                {
                    return Err(Error::Invalid);
                }
            }
        }
        Ok(())
    }
    pub fn lookup(&self, c: &ContractRef, name: &str) -> Result<&Operation> {
        self.contracts
            .iter()
            .find(|v| v.name == c.name && v.version == c.version)
            .and_then(|v| v.operations.iter().find(|o| o.name == name))
            .ok_or(Error::Unsupported)
    }
    pub fn matches(&self, actual: &Self) -> Result<()> {
        self.validate()?;
        actual.validate()?;
        if self.identity != actual.identity || self.contracts.len() != actual.contracts.len() {
            return Err(Error::Mismatch);
        }
        for c in &self.contracts {
            let a = actual
                .contracts
                .iter()
                .find(|a| a.name == c.name && a.version == c.version)
                .ok_or(Error::Mismatch)?;
            if a.operations.len() != c.operations.len()
                || c.operations.iter().any(|op| !a.operations.contains(op))
            {
                return Err(Error::Mismatch);
            }
        }
        Ok(())
    }
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Request {
    #[serde(rename = "apiVersion")]
    pub api_version: String,
    pub id: String,
    pub plugin: Identity,
    pub contract: ContractRef,
    pub operation: String,
    #[serde(default, skip_serializing_if = "empty")]
    pub surface: String,
    pub deadline: String,
    #[serde(default)]
    pub payload: Value,
}
impl Request {
    pub fn validate(&self, d: &Descriptor) -> Result<()> {
        d.validate()?;
        if self.api_version != VERSION || !identifier(&self.id, true) {
            return Err(Error::Invalid);
        }
        parse_deadline(&self.deadline)?;
        if self.plugin != d.identity
            || d.lookup(&self.contract, &self.operation)?.surface != self.surface
        {
            return Err(Error::Mismatch);
        }
        encode(&self.payload)?;
        Ok(())
    }
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RemoteError {
    pub code: String,
    pub message: String,
    #[serde(
        default,
        rename = "retryAfterMilliseconds",
        skip_serializing_if = "zero"
    )]
    pub retry_after_milliseconds: u64,
}
fn zero(v: &u64) -> bool {
    *v == 0
}
pub fn response(id: &str, result: Result<Value>) -> Result<Value> {
    let mut v = serde_json::json!({"apiVersion":VERSION,"id":id});
    match result {
        Ok(payload) => v["payload"] = payload,
        Err(e) => {
            let remote = match e {
                Error::Remote(r) => r,
                _ => RemoteError {
                    code: "operation_failed".into(),
                    message: "plugin operation failed".into(),
                    retry_after_milliseconds: 0,
                },
            };
            v["error"] = serde_json::to_value(remote).map_err(|_| Error::Invalid)?;
        }
    }
    validate_response(&v, id)?;
    Ok(v)
}
pub fn validate_response(v: &Value, id: &str) -> Result<()> {
    let m = v.as_object().ok_or(Error::Invalid)?;
    if m.keys()
        .any(|k| !["apiVersion", "id", "payload", "error"].contains(&k.as_str()))
    {
        return Err(Error::Invalid);
    }
    if v["apiVersion"] != VERSION || v["id"] != id {
        return Err(Error::Mismatch);
    }
    let has_error = m.get("error").is_some_and(|e| !e.is_null());
    if m.contains_key("payload") == has_error {
        return Err(Error::Invalid);
    }
    if has_error {
        let e: RemoteError = typed(v["error"].clone())?;
        if !identifier(&e.code, false)
            || e.message.len() > 4096
            || e.retry_after_milliseconds > i64::MAX as u64
        {
            return Err(Error::Invalid);
        }
    }
    Ok(())
}
pub fn parse_deadline(s: &str) -> Result<SystemTime> {
    let t = time::OffsetDateTime::parse(s, &time::format_description::well_known::Rfc3339)
        .map_err(|_| Error::Invalid)?;
    let utc = t.to_offset(time::UtcOffset::UTC);
    if utc.year() == 1
        && utc.ordinal() == 1
        && utc.hour() == 0
        && utc.minute() == 0
        && utc.second() == 0
        && utc.nanosecond() == 0
    {
        return Err(Error::Invalid);
    }
    let ns = t.unix_timestamp_nanos();
    let magnitude = ns.unsigned_abs();
    let delta = Duration::new(
        u64::try_from(magnitude / 1_000_000_000).map_err(|_| Error::Invalid)?,
        (magnitude % 1_000_000_000) as u32,
    );
    if ns >= 0 {
        UNIX_EPOCH.checked_add(delta)
    } else {
        UNIX_EPOCH.checked_sub(delta)
    }
    .ok_or(Error::Invalid)
}
pub fn timestamp(t: SystemTime) -> Result<String> {
    let dt: time::OffsetDateTime = t.into();
    dt.format(&time::format_description::well_known::Rfc3339)
        .map_err(|_| Error::Invalid)
}
