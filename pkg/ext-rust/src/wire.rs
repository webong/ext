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
    T::deserialize(Objects(value)).map_err(|_| Error::Invalid)
}

// A deserializer over a JSON value that reads a struct only from an object. serde's
// derive would also read a list as the struct's fields in order, so an operation
// given as ["echo"] would pass where Go refuses it.
struct Objects(Value);
struct Items(std::vec::IntoIter<Value>);
struct Members {
    iter: serde_json::map::IntoIter,
    value: Option<Value>,
}
impl<'de> de::Deserializer<'de> for Objects {
    type Error = serde_json::Error;
    fn deserialize_any<V: Visitor<'de>>(
        self,
        visitor: V,
    ) -> std::result::Result<V::Value, Self::Error> {
        match self.0 {
            Value::Null => visitor.visit_unit(),
            Value::Bool(b) => visitor.visit_bool(b),
            Value::Number(n) => {
                if let Some(u) = n.as_u64() {
                    visitor.visit_u64(u)
                } else if let Some(i) = n.as_i64() {
                    visitor.visit_i64(i)
                } else {
                    visitor.visit_f64(n.as_f64().unwrap_or(f64::NAN))
                }
            }
            Value::String(s) => visitor.visit_string(s),
            Value::Array(a) => visitor.visit_seq(Items(a.into_iter())),
            Value::Object(m) => visitor.visit_map(Members {
                iter: m.into_iter(),
                value: None,
            }),
        }
    }
    fn deserialize_struct<V: Visitor<'de>>(
        self,
        _name: &'static str,
        _fields: &'static [&'static str],
        visitor: V,
    ) -> std::result::Result<V::Value, Self::Error> {
        if self.0.is_array() {
            return Err(de::Error::custom("a struct is a JSON object"));
        }
        self.deserialize_any(visitor)
    }
    fn deserialize_option<V: Visitor<'de>>(
        self,
        visitor: V,
    ) -> std::result::Result<V::Value, Self::Error> {
        if self.0.is_null() {
            visitor.visit_none()
        } else {
            visitor.visit_some(self)
        }
    }
    fn deserialize_newtype_struct<V: Visitor<'de>>(
        self,
        _name: &'static str,
        visitor: V,
    ) -> std::result::Result<V::Value, Self::Error> {
        visitor.visit_newtype_struct(self)
    }
    fn deserialize_enum<V: Visitor<'de>>(
        self,
        name: &'static str,
        variants: &'static [&'static str],
        visitor: V,
    ) -> std::result::Result<V::Value, Self::Error> {
        self.0.deserialize_enum(name, variants, visitor)
    }
    serde::forward_to_deserialize_any! {
        bool i8 i16 i32 i64 i128 u8 u16 u32 u64 u128 f32 f64 char str string bytes byte_buf
        unit unit_struct seq tuple tuple_struct map identifier ignored_any
    }
}
impl<'de> SeqAccess<'de> for Items {
    type Error = serde_json::Error;
    fn next_element_seed<T: DeserializeSeed<'de>>(
        &mut self,
        seed: T,
    ) -> std::result::Result<Option<T::Value>, Self::Error> {
        match self.0.next() {
            Some(v) => seed.deserialize(Objects(v)).map(Some),
            None => Ok(None),
        }
    }
    fn size_hint(&self) -> Option<usize> {
        Some(self.0.len())
    }
}
impl<'de> MapAccess<'de> for Members {
    type Error = serde_json::Error;
    fn next_key_seed<K: DeserializeSeed<'de>>(
        &mut self,
        seed: K,
    ) -> std::result::Result<Option<K::Value>, Self::Error> {
        match self.iter.next() {
            Some((key, value)) => {
                self.value = Some(value);
                seed.deserialize(de::value::StringDeserializer::<serde_json::Error>::new(key))
                    .map(Some)
            }
            None => Ok(None),
        }
    }
    fn next_value_seed<V: DeserializeSeed<'de>>(
        &mut self,
        seed: V,
    ) -> std::result::Result<V::Value, Self::Error> {
        let value = self
            .value
            .take()
            .ok_or_else(|| de::Error::custom("a member without a value"))?;
        seed.deserialize(Objects(value))
    }
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
fn is_false(v: &bool) -> bool {
    !*v
}
// Go leaves a member alone when its JSON is null, so a null is the default value.
fn null_default<'de, D: de::Deserializer<'de>, T: Deserialize<'de> + Default>(
    d: D,
) -> std::result::Result<T, D::Error> {
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Identity {
    pub id: String,
    pub revision: String,
    #[serde(
        default,
        skip_serializing_if = "empty",
        deserialize_with = "null_default"
    )]
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
    #[serde(
        default,
        skip_serializing_if = "empty",
        deserialize_with = "null_default"
    )]
    pub surface: String,
    /// A call to this operation may emit progress and log frames before its
    /// response. Absent means it never does.
    #[serde(
        default,
        skip_serializing_if = "is_false",
        deserialize_with = "null_default"
    )]
    pub progress: bool,
}
impl Operation {
    pub fn new(name: &str) -> Self {
        Self {
            name: name.into(),
            surface: String::new(),
            progress: false,
        }
    }
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
    /// The host operations the plugin may call (reverse calls).
    #[serde(
        default,
        skip_serializing_if = "Vec::is_empty",
        deserialize_with = "null_default"
    )]
    pub reverse: Vec<Contract>,
}
fn validate_contracts(contracts: &[Contract]) -> Result<()> {
    let mut refs = HashSet::new();
    for c in contracts {
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
// The same contracts with the same operations, in any order.
fn contracts_match(selected: &[Contract], actual: &[Contract]) -> Result<()> {
    if selected.len() != actual.len() {
        return Err(Error::Mismatch);
    }
    for c in selected {
        let a = actual
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
impl Descriptor {
    pub fn validate(&self) -> Result<()> {
        self.identity.validate()?;
        if self.api_version != VERSION || self.contracts.is_empty() || self.contracts.len() > 64 {
            return Err(Error::Invalid);
        }
        validate_contracts(&self.contracts)?;
        if self.reverse.len() > 64
            || self
                .reverse
                .iter()
                .any(|c| c.operations.iter().any(|op| op.progress))
        {
            return Err(Error::Invalid);
        }
        validate_contracts(&self.reverse)
    }
    pub fn lookup(&self, c: &ContractRef, name: &str) -> Result<&Operation> {
        Self::find(&self.contracts, c, name)
    }
    /// An operation the plugin declared it may call on the host.
    pub fn lookup_reverse(&self, c: &ContractRef, name: &str) -> Result<&Operation> {
        Self::find(&self.reverse, c, name)
    }
    fn find<'a>(contracts: &'a [Contract], c: &ContractRef, name: &str) -> Result<&'a Operation> {
        contracts
            .iter()
            .find(|v| v.name == c.name && v.version == c.version)
            .and_then(|v| v.operations.iter().find(|o| o.name == name))
            .ok_or(Error::Unsupported)
    }
    pub fn matches(&self, actual: &Self) -> Result<()> {
        self.validate()?;
        actual.validate()?;
        if self.identity != actual.identity {
            return Err(Error::Mismatch);
        }
        contracts_match(&self.contracts, &actual.contracts)?;
        contracts_match(&self.reverse, &actual.reverse)
    }
}
/// The event rate a host sets for a call: events and bytes of text per second.
/// Zero or negative values use the defaults.
#[derive(Clone, Copy, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ProgressLimits {
    #[serde(
        default,
        rename = "eventsPerSecond",
        skip_serializing_if = "is_zero_i64",
        deserialize_with = "null_default"
    )]
    pub events_per_second: i64,
    #[serde(
        default,
        rename = "bytesPerSecond",
        skip_serializing_if = "is_zero_i64",
        deserialize_with = "null_default"
    )]
    pub bytes_per_second: i64,
}
fn is_zero_i64(v: &i64) -> bool {
    *v == 0
}
// The rate is a JSON object or null: serde would also read a list as a struct.
fn limits_object<'de, D: de::Deserializer<'de>>(
    d: D,
) -> std::result::Result<Option<ProgressLimits>, D::Error> {
    match Option::<Value>::deserialize(d)? {
        None | Some(Value::Null) => Ok(None),
        Some(v @ Value::Object(_)) => typed(v)
            .map(Some)
            .map_err(|_| de::Error::custom("progress limits")),
        Some(_) => Err(de::Error::custom("progress must be an object")),
    }
}
pub const DEFAULT_PROGRESS_EVENTS: i64 = 50;
pub const DEFAULT_PROGRESS_BYTES: i64 = 64 << 10;
impl ProgressLimits {
    /// The limits with the defaults filled in.
    pub fn resolved(self) -> Self {
        Self {
            events_per_second: if self.events_per_second <= 0 {
                DEFAULT_PROGRESS_EVENTS
            } else {
                self.events_per_second
            },
            bytes_per_second: if self.bytes_per_second <= 0 {
                DEFAULT_PROGRESS_BYTES
            } else {
                self.bytes_per_second
            },
        }
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
    #[serde(
        default,
        skip_serializing_if = "empty",
        deserialize_with = "null_default"
    )]
    pub surface: String,
    pub deadline: String,
    #[serde(default)]
    pub payload: Value,
    /// Present on a call whose operation declared progress: the rate at which it
    /// may report.
    #[serde(
        default,
        skip_serializing_if = "Option::is_none",
        deserialize_with = "limits_object"
    )]
    pub progress: Option<ProgressLimits>,
    /// Names the forward call a reverse call belongs to; set only on the request
    /// a host's reverse handler receives.
    #[serde(
        default,
        skip_serializing_if = "empty",
        deserialize_with = "null_default"
    )]
    pub parent: String,
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
