//! Progress events and reverse calls on the JSON-line wire: the frames a guest
//! sends and receives, the rules that judge them (the Go contract's, pinned by
//! lib/plugin/testdata/v1/frame-cases.json) and the sender's rate limiter (pinned
//! by progress-limiter-cases.json). The machinery that carries them is in `serve`.
use crate::{
    wire::{self, identifier, ProgressLimits, RemoteError},
    Error, Result, VERSION,
};
use serde_json::{json, Map, Value};

/// The most text one event may carry, in bytes of UTF-8.
pub const MAX_PROGRESS_TEXT: usize = 2048;
/// A retry hint is whole milliseconds, at most one year.
pub const MAX_RETRY_AFTER_MS: i64 = 365 * 24 * 60 * 60 * 1000;
pub const FRAME_PROGRESS: &str = "progress";
pub const FRAME_REVERSE_REQUEST: &str = "reverse_request";
pub const FRAME_REVERSE_RESPONSE: &str = "reverse_response";
/// The code of a reverse call the guest refused itself, because the descriptor does
/// not list the operation.
pub const REVERSE_UNDECLARED: &str = "reverse_undeclared";

/// What came of reporting an event. A drop by the rate limit is routine: the call is
/// fine, and the count is reported with the next event.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ProgressOutcome {
    Sent,
    Dropped,
}

/// One progress or log event of a call.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Progress {
    /// `"progress"` or `"log"`.
    pub kind: String,
    /// `""`, `"out"` or `"err"`; only a log event has one.
    pub stream: String,
    pub text: String,
    pub completed: u64,
    pub total: u64,
    /// Events the rate limit dropped since the previous one that passed. The
    /// sender fills it in; whatever a handler sets is ignored.
    pub dropped: u64,
}
impl Progress {
    /// A log event.
    pub fn log(text: &str) -> Self {
        Self {
            kind: "log".into(),
            text: text.into(),
            ..Self::default()
        }
    }
    /// A log event for a program's standard output.
    pub fn out(text: &str) -> Self {
        Self {
            stream: "out".into(),
            ..Self::log(text)
        }
    }
    /// A log event for a program's standard error.
    pub fn err(text: &str) -> Self {
        Self {
            stream: "err".into(),
            ..Self::log(text)
        }
    }
    /// A progress event: `completed` of `total` (zero when not known).
    pub fn fraction(completed: u64, total: u64) -> Self {
        Self {
            kind: "progress".into(),
            completed,
            total,
            ..Self::default()
        }
    }
    /// Checks the event's shape.
    pub fn validate(&self) -> Result<()> {
        if (self.kind != "progress" && self.kind != "log")
            || (!self.stream.is_empty() && self.stream != "out" && self.stream != "err")
            || (!self.stream.is_empty() && self.kind != "log")
            || self.text.len() > MAX_PROGRESS_TEXT
            || (self.total != 0 && self.completed > self.total)
        {
            return Err(Error::Protocol);
        }
        Ok(())
    }
    pub(crate) fn frame(&self, parent: &str) -> Value {
        let mut m = Map::new();
        m.insert("frame".into(), FRAME_PROGRESS.into());
        m.insert("apiVersion".into(), VERSION.into());
        m.insert("parent".into(), parent.into());
        m.insert("kind".into(), self.kind.clone().into());
        if !self.stream.is_empty() {
            m.insert("stream".into(), self.stream.clone().into());
        }
        if !self.text.is_empty() {
            m.insert("text".into(), self.text.clone().into());
        }
        for (key, value) in [
            ("completed", self.completed),
            ("total", self.total),
            ("dropped", self.dropped),
        ] {
            if value != 0 {
                m.insert(key.into(), value.into());
            }
        }
        Value::Object(m)
    }
}

/// Cuts text into pieces that each fit one event: at most `MAX_PROGRESS_TEXT` bytes
/// of UTF-8, never ending inside a character. A `&str` is already valid UTF-8, so
/// nothing is replaced; callers with bytes use `String::from_utf8_lossy` first.
pub fn split_progress_text(text: &str) -> Vec<String> {
    let mut pieces = Vec::new();
    let mut rest = text;
    while rest.len() > MAX_PROGRESS_TEXT {
        let mut cut = MAX_PROGRESS_TEXT;
        while !rest.is_char_boundary(cut) {
            cut -= 1;
        }
        pieces.push(rest[..cut].to_string());
        rest = &rest[cut..];
    }
    if !rest.is_empty() {
        pieces.push(rest.to_string());
    }
    pieces
}

// ---- reading frames, as Go's strict decoder does ----

// A member Go decodes into a string: absent and null are empty.
fn string_member(m: &Map<String, Value>, key: &str) -> Result<String> {
    match m.get(key) {
        None | Some(Value::Null) => Ok(String::new()),
        Some(Value::String(s)) => Ok(s.clone()),
        Some(_) => Err(Error::Protocol),
    }
}
// A member Go decodes into a uint64: absent and null are zero; a fraction, an
// exponent, a sign or a value past the range is an error.
fn uint_member(m: &Map<String, Value>, key: &str) -> Result<u64> {
    match m.get(key) {
        None | Some(Value::Null) => Ok(0),
        Some(Value::Number(n)) => n.as_u64().ok_or(Error::Protocol),
        Some(_) => Err(Error::Protocol),
    }
}
fn only(m: &Map<String, Value>, keys: &[&str]) -> Result<()> {
    if m.keys().any(|k| !keys.contains(&k.as_str())) {
        return Err(Error::Protocol);
    }
    Ok(())
}
fn object(v: &Value) -> Result<&Map<String, Value>> {
    v.as_object().ok_or(Error::Protocol)
}

/// Judges a progress frame: its parent (the call it belongs to) and the event.
pub fn validate_progress_frame(v: &Value) -> Result<(String, Progress)> {
    let m = object(v)?;
    only(
        m,
        &[
            "frame",
            "apiVersion",
            "parent",
            "kind",
            "stream",
            "text",
            "completed",
            "total",
            "dropped",
        ],
    )?;
    let parent = string_member(m, "parent")?;
    let event = Progress {
        kind: string_member(m, "kind")?,
        stream: string_member(m, "stream")?,
        text: string_member(m, "text")?,
        completed: uint_member(m, "completed")?,
        total: uint_member(m, "total")?,
        dropped: uint_member(m, "dropped")?,
    };
    if string_member(m, "frame")? != FRAME_PROGRESS
        || string_member(m, "apiVersion")? != VERSION
        || !identifier(&parent, true)
    {
        return Err(Error::Protocol);
    }
    event.validate()?;
    Ok((parent, event))
}

/// Judges a reverse request frame, as the host reads it.
pub fn validate_reverse_request(v: &Value) -> Result<()> {
    let m = object(v)?;
    only(
        m,
        &[
            "frame",
            "apiVersion",
            "id",
            "parent",
            "contract",
            "operation",
            "deadline",
            "payload",
        ],
    )?;
    let contract = match m.get("contract") {
        Some(Value::Object(c)) => c,
        _ => return Err(Error::Protocol),
    };
    only(contract, &["name", "version"])?;
    let parent = string_member(m, "parent")?;
    if string_member(m, "frame")? != FRAME_REVERSE_REQUEST
        || string_member(m, "apiVersion")? != VERSION
        || !identifier(&string_member(m, "id")?, true)
        || (!parent.is_empty() && !identifier(&parent, true))
        || !identifier(&string_member(contract, "name")?, false)
        || !identifier(&string_member(contract, "version")?, true)
        || !identifier(&string_member(m, "operation")?, false)
    {
        return Err(Error::Protocol);
    }
    // A deadline that is absent, not a time, or the zero time is refused.
    match m.get("deadline") {
        Some(Value::String(s)) => wire::parse_deadline(s)
            .map(|_| ())
            .map_err(|_| Error::Protocol),
        _ => Err(Error::Protocol),
    }
}

fn remote_error(v: &Value) -> Result<RemoteError> {
    let m = object(v)?;
    only(m, &["code", "message", "retryAfterMilliseconds"])?;
    let retry = match m.get("retryAfterMilliseconds") {
        None | Some(Value::Null) => 0,
        Some(Value::Number(n)) => n.as_i64().ok_or(Error::Protocol)?,
        Some(_) => return Err(Error::Protocol),
    };
    let e = RemoteError {
        code: string_member(m, "code")?,
        message: string_member(m, "message")?,
        retry_after_milliseconds: u64::try_from(retry).map_err(|_| Error::Protocol)?,
    };
    if !identifier(&e.code, false) || e.message.len() > 4096 || retry > MAX_RETRY_AFTER_MS {
        return Err(Error::Protocol);
    }
    Ok(e)
}

/// What the host answered to a reverse request.
#[derive(Clone, Debug)]
pub enum Answer {
    Payload(Value),
    Error(RemoteError),
}

/// Judges a reverse response, which a guest accepts only for the request `id` it
/// is waiting on.
pub fn validate_reverse_response(v: &Value, id: &str) -> Result<Answer> {
    let m = object(v)?;
    only(m, &["frame", "apiVersion", "id", "payload", "error"])?;
    if string_member(m, "frame")? != FRAME_REVERSE_RESPONSE
        || string_member(m, "apiVersion")? != VERSION
        || string_member(m, "id")? != id
    {
        return Err(Error::Protocol);
    }
    let error = m.get("error").filter(|e| !e.is_null());
    // A response carries a payload (null counts) or an error, never both or neither.
    if error.is_some() == m.contains_key("payload") {
        return Err(Error::Protocol);
    }
    match error {
        Some(e) => Ok(Answer::Error(remote_error(e)?)),
        None => Ok(Answer::Payload(m["payload"].clone())),
    }
}

/// Judges a progress frame given as the bytes of one line.
pub fn check_progress_frame(line: &[u8]) -> Result<()> {
    validate_progress_frame(&wire::decode(line).map_err(|_| Error::Protocol)?).map(|_| ())
}
/// Judges a reverse request frame given as the bytes of one line.
pub fn check_reverse_request(line: &[u8]) -> Result<()> {
    validate_reverse_request(&wire::decode(line).map_err(|_| Error::Protocol)?)
}
/// Judges a reverse response given as the bytes of one line.
pub fn check_reverse_response(line: &[u8], id: &str) -> Result<()> {
    validate_reverse_response(&wire::decode(line).map_err(|_| Error::Protocol)?, id).map(|_| ())
}

pub(crate) fn reverse_request_frame(
    id: &str,
    parent: &str,
    contract: &wire::ContractRef,
    operation: &str,
    deadline: &str,
    payload: &Value,
) -> Value {
    let mut frame = json!({
        "frame": FRAME_REVERSE_REQUEST,
        "apiVersion": VERSION,
        "id": id,
        "contract": {"name": contract.name, "version": contract.version},
        "operation": operation,
        "deadline": deadline,
    });
    if !parent.is_empty() {
        frame["parent"] = parent.into();
    }
    if !payload.is_null() {
        frame["payload"] = payload.clone();
    }
    frame
}

// ---- the sender's rate limiter ----

/// A token bucket counted in events and bytes of text with a burst of twice the
/// rate, and a count of the events dropped since the last one that passed. Time is
/// a monotonic clock in nanoseconds, given by the caller.
pub struct Limiter {
    events: f64,
    bytes: f64,
    event_rate: f64,
    byte_rate: f64,
    last_ns: i64,
    dropped: u64,
}
impl Limiter {
    pub fn new(limits: ProgressLimits, now_ns: i64) -> Self {
        let l = limits.resolved();
        let (event_rate, byte_rate) = (l.events_per_second as f64, l.bytes_per_second as f64);
        Self {
            events: 2.0 * event_rate,
            bytes: 2.0 * byte_rate,
            event_rate,
            byte_rate,
            last_ns: now_ns,
            dropped: 0,
        }
    }
    /// Whether an event whose text is `text_bytes` long may pass. When it does the
    /// count is the number of events dropped since the previous passing one, which
    /// the caller attaches to the event; when it does not, the drop is counted.
    pub fn admit(&mut self, text_bytes: usize, now_ns: i64) -> Option<u64> {
        let delta = now_ns - self.last_ns;
        // Seconds as Go's Duration.Seconds computes them.
        let elapsed = (delta / 1_000_000_000) as f64 + (delta % 1_000_000_000) as f64 / 1e9;
        if elapsed > 0.0 {
            self.events = (self.events + elapsed * self.event_rate).min(2.0 * self.event_rate);
            self.bytes = (self.bytes + elapsed * self.byte_rate).min(2.0 * self.byte_rate);
            self.last_ns = now_ns;
        }
        let cost = text_bytes as f64;
        if self.events < 1.0 || self.bytes < cost {
            self.dropped += 1;
            return None;
        }
        self.events -= 1.0;
        self.bytes -= cost;
        Some(std::mem::take(&mut self.dropped))
    }
    /// The drops not yet reported, reset to zero.
    pub fn pending(&mut self) -> u64 {
        std::mem::take(&mut self.dropped)
    }
}
