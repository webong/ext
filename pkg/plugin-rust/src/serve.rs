//! Serving a guest over JSON lines, with progress events and reverse calls.
//!
//! One connection has one `Channel`: a lock around writes, a table of the reverse
//! requests waiting for an answer, and a router that classifies every frame the host
//! sends (a request for the call loop, an answer for its waiter, anything else a
//! violation that ends the session). Two modes carry the same API:
//!
//! - `Mode::Threaded` reads on a thread of its own, so a handler can wait for the
//!   host's answer to several requests at once, and background work can make a
//!   session-scoped call after the call that started it has returned.
//! - `Mode::Inline` reads on the thread that is waiting: the call loop between calls,
//!   a handler waiting for an answer. The host sends nothing but answers during a
//!   call, so that is enough, and it works where there are no threads (WebAssembly).
//!   It has no session-scoped caller, and one request outstanding at a time.
use crate::{
    frames::{self, Answer, Limiter, Progress, ProgressOutcome},
    guest::{self, read_frame, Guest},
    wire::{self, ContractRef, Descriptor, ProgressLimits, RemoteError, Request},
    Error, Result, DEFAULT_TIMEOUT,
};
use serde_json::Value;
use std::{
    collections::{HashMap, VecDeque},
    io::{BufRead, Write},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Condvar, Mutex, MutexGuard,
    },
    time::{Duration, Instant, SystemTime},
};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Mode {
    Threaded,
    Inline,
}
impl Default for Mode {
    /// Threaded, except where there are no threads.
    fn default() -> Self {
        if cfg!(target_os = "wasi") {
            Mode::Inline
        } else {
            Mode::Threaded
        }
    }
}

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}

#[derive(Clone, Copy)]
enum Failure {
    Protocol,
    Transport,
}
impl Failure {
    fn error(self) -> Error {
        match self {
            Failure::Protocol => Error::Protocol,
            Failure::Transport => Error::Transport,
        }
    }
}
enum Slot {
    Pending,
    Done(Value),
}
#[derive(Default)]
struct State {
    issued: u64,
    waiting: HashMap<String, Slot>,
    busy: bool, // a request is being handled
    queue: VecDeque<Value>,
    gone: bool,
    failure: Option<Failure>,
}

pub(crate) struct Channel {
    out: Mutex<Box<dyn Write + Send>>,
    input: Mutex<Option<Box<dyn BufRead + Send>>>, // inline mode only
    state: Mutex<State>,
    changed: Condvar,
    closed: Arc<AtomicBool>, // set when the connection ends: the handler's cancellation
    threaded: bool,
    started: Instant,
}

const POLL: Duration = Duration::from_millis(10);

impl Channel {
    fn now_ns(&self) -> i64 {
        i64::try_from(self.started.elapsed().as_nanos()).unwrap_or(i64::MAX)
    }
    fn write(&self, v: &Value) -> Result<()> {
        let bytes = wire::encode(v)?;
        let mut out = lock(&self.out);
        out.write_all(&bytes)
            .and_then(|_| out.write_all(b"\n"))
            .and_then(|_| out.flush())
            .map_err(|_| Error::Transport)
    }
    /// The connection is over, cleanly or not; every waiter is released.
    fn end(&self, failure: Option<Failure>) {
        let mut s = lock(&self.state);
        s.gone = true;
        if s.failure.is_none() {
            s.failure = failure;
        }
        self.closed.store(true, Ordering::Release);
        self.changed.notify_all();
    }
    /// Classifies one frame from the host.
    fn route(&self, bytes: &[u8]) -> Result<()> {
        let value = wire::decode(bytes).map_err(|_| Error::Protocol)?;
        let tag = match value.as_object().ok_or(Error::Protocol)?.get("frame") {
            None => None,
            Some(Value::String(t)) => Some(t.as_str()),
            Some(_) => return Err(Error::Protocol),
        };
        match tag {
            None => {
                let mut s = lock(&self.state);
                if s.busy {
                    return Err(Error::Protocol); // a request while a call is in flight
                }
                s.busy = true;
                s.queue.push_back(value);
                self.changed.notify_all();
                Ok(())
            }
            Some(frames::FRAME_REVERSE_RESPONSE) => self.deliver(value),
            Some(_) => Err(Error::Protocol),
        }
    }
    /// Hands an answer to the request that waits for it. An answer to a request that
    /// was abandoned is dropped; one to an id never issued is a violation.
    fn deliver(&self, frame: Value) -> Result<()> {
        let id = frame
            .get("id")
            .and_then(Value::as_str)
            .unwrap_or("")
            .to_string();
        let mut s = lock(&self.state);
        if matches!(s.waiting.get(&id), Some(Slot::Pending)) {
            frames::validate_reverse_response(&frame, &id).map_err(|_| Error::Protocol)?;
            s.waiting.insert(id, Slot::Done(frame));
            self.changed.notify_all();
            return Ok(());
        }
        let issued = s.issued;
        let number = id
            .strip_prefix('g')
            .filter(|n| !n.is_empty() && n.bytes().all(|b| b.is_ascii_digit()))
            .and_then(|n| n.parse::<u64>().ok());
        match number {
            Some(n) if n >= 1 && n <= issued => Ok(()),
            _ => Err(Error::Protocol),
        }
    }
    /// Reads and routes one frame on the calling thread (inline mode). With
    /// `block` false it gives up at once if another thread is reading.
    fn pump(&self, block: bool) -> bool {
        let mut input = if block {
            lock(&self.input)
        } else {
            match self.input.try_lock() {
                Ok(g) => g,
                Err(_) => return false,
            }
        };
        let Some(reader) = input.as_mut() else {
            self.end(None);
            return true;
        };
        let frame = read_frame(reader);
        drop(input);
        match frame {
            Ok(Some(bytes)) => {
                if let Err(e) = self.route(&bytes) {
                    self.end(Some(match e {
                        Error::Protocol | Error::Invalid => Failure::Protocol,
                        _ => Failure::Transport,
                    }));
                }
            }
            Ok(None) => self.end(None),
            Err(_) => self.end(Some(Failure::Transport)),
        }
        true
    }
    /// The next request, or None when the host closed the connection.
    fn next_request(&self) -> Result<Option<Value>> {
        loop {
            {
                let mut s = lock(&self.state);
                if let Some(v) = s.queue.pop_front() {
                    return Ok(Some(v));
                }
                if s.gone {
                    return match s.failure {
                        None => Ok(None),
                        Some(f) => Err(f.error()),
                    };
                }
                if self.threaded {
                    drop(self.changed.wait(s).unwrap_or_else(|e| e.into_inner()));
                    continue;
                }
            }
            self.pump(true);
        }
    }
    /// Waits for the answer to request `id`.
    fn await_answer(&self, id: &str, deadline: SystemTime) -> Result<Value> {
        loop {
            {
                let mut s = lock(&self.state);
                if matches!(s.waiting.get(id), Some(Slot::Done(_))) {
                    if let Some(Slot::Done(frame)) = s.waiting.remove(id) {
                        return Ok(frame);
                    }
                }
                let failure = s.failure;
                if s.gone || SystemTime::now() >= deadline {
                    s.waiting.remove(id);
                    return Err(match (s.gone, failure) {
                        (true, Some(f)) => f.error(),
                        (true, None) => Error::Closed,
                        _ => Error::Deadline,
                    });
                }
                if self.threaded {
                    drop(
                        self.changed
                            .wait_timeout(s, POLL)
                            .unwrap_or_else(|e| e.into_inner()),
                    );
                    continue;
                }
            }
            // Inline: read frames until the answer arrives. If another thread is
            // reading, it will deliver ours.
            if !self.pump(false) {
                let s = lock(&self.state);
                drop(
                    self.changed
                        .wait_timeout(s, POLL)
                        .unwrap_or_else(|e| e.into_inner()),
                );
            }
        }
    }
    fn abandon(&self, id: &str) {
        lock(&self.state).waiting.remove(id);
    }
    fn reverse_call(
        &self,
        descriptor: &Descriptor,
        parent: &str,
        contract: &ContractRef,
        operation: &str,
        payload: &Value,
        deadline: SystemTime,
    ) -> Result<Value> {
        // Only what the descriptor declares is asked of the host; no frame is sent
        // for anything else.
        if descriptor.lookup_reverse(contract, operation).is_err() {
            return Err(refused(frames::REVERSE_UNDECLARED));
        }
        let id = {
            let mut s = lock(&self.state);
            s.issued += 1;
            let id = format!("g{}", s.issued);
            s.waiting.insert(id.clone(), Slot::Pending);
            id
        };
        let frame = frames::reverse_request_frame(
            &id,
            parent,
            contract,
            operation,
            &wire::timestamp(deadline).inspect_err(|_| self.abandon(&id))?,
            payload,
        );
        if frames::validate_reverse_request(&frame).is_err() {
            self.abandon(&id);
            return Err(Error::Invalid);
        }
        if let Err(e) = self.write(&frame) {
            self.abandon(&id);
            return Err(e);
        }
        let response = self.await_answer(&id, deadline)?;
        match frames::validate_reverse_response(&response, &id)? {
            Answer::Error(e) => Err(Error::Remote(e)),
            Answer::Payload(p) => Ok(p),
        }
    }
}

fn refused(code: &str) -> Error {
    Error::Remote(RemoteError {
        code: code.into(),
        message: "reverse call refused".into(),
        retry_after_milliseconds: 0,
    })
}

/// A way to call the host: the operations the descriptor lists under `reverse`.
/// A call-scoped caller belongs to the forward call that made it; a session-scoped
/// one (threaded mode only) can be kept and used from other threads for as long as
/// the connection lasts.
#[derive(Clone)]
pub struct Reverse {
    channel: Arc<Channel>,
    descriptor: Arc<Descriptor>,
    parent: String, // empty for session scope
    deadline: Option<SystemTime>,
}
impl Reverse {
    /// Calls a host operation and waits for the answer. The call's own deadline
    /// bounds the wait (30 s on, for a session-scoped caller). A host that refuses or
    /// fails gives `Error::Remote` with its code: `denied`, `busy`, `too_large`,
    /// `expired`, `reverse_unavailable`, `operation_failed` or its policy's own; an
    /// operation the descriptor does not list is refused here as `reverse_undeclared`
    /// without a frame. `Error::Closed` is a connection that ended.
    pub fn call(&self, contract: &ContractRef, operation: &str, payload: &Value) -> Result<Value> {
        let deadline = self
            .deadline
            .unwrap_or_else(|| SystemTime::now() + DEFAULT_TIMEOUT);
        self.call_by(contract, operation, payload, deadline)
    }
    pub fn call_by(
        &self,
        contract: &ContractRef,
        operation: &str,
        payload: &Value,
        deadline: SystemTime,
    ) -> Result<Value> {
        self.channel.reverse_call(
            &self.descriptor,
            &self.parent,
            contract,
            operation,
            payload,
            deadline,
        )
    }
    /// Whether requests may be outstanding at once and made from other threads: true
    /// when a thread of its own reads the connection.
    pub fn concurrent(&self) -> bool {
        self.channel.threaded
    }
}

/// Sends the progress events of one call.
pub(crate) struct Reporter {
    channel: Arc<Channel>,
    parent: String,
    limiter: Mutex<Limiter>,
    finished: Mutex<bool>,
}
impl Reporter {
    pub(crate) fn report(&self, mut event: Progress) -> Result<ProgressOutcome> {
        event.dropped = 0;
        event.validate().map_err(|_| Error::Invalid)?;
        let dropped = lock(&self.limiter).admit(event.text.len(), self.channel.now_ns());
        let Some(dropped) = dropped else {
            return Ok(ProgressOutcome::Dropped);
        };
        event.dropped = dropped;
        let finished = lock(&self.finished);
        if *finished {
            return Err(Error::Closed);
        }
        self.channel.write(&event.frame(&self.parent))?;
        Ok(ProgressOutcome::Sent)
    }
    /// Ends reporting after a marker for events never reported: nothing may follow
    /// the response.
    pub(crate) fn finish(&self) {
        let mut finished = lock(&self.finished);
        if *finished {
            return;
        }
        *finished = true;
        let pending = lock(&self.limiter).pending();
        if pending > 0 {
            let marker = Progress {
                kind: "log".into(),
                dropped: pending,
                ..Progress::default()
            };
            let _ = self.channel.write(&marker.frame(&self.parent));
        }
    }
}

/// What a call can do beyond returning a result, when its transport carries frames.
#[derive(Clone)]
pub(crate) struct CallFrames {
    pub(crate) reporter: Option<Arc<Reporter>>,
    pub(crate) limits: Option<ProgressLimits>,
    pub(crate) reverse: Option<Reverse>,
    pub(crate) session: Option<Reverse>,
}
impl CallFrames {
    pub(crate) fn at(mut self, deadline: SystemTime) -> Self {
        if let Some(r) = self.reverse.as_mut() {
            r.deadline = Some(deadline);
        }
        self
    }
    pub(crate) fn finish(&self) {
        if let Some(r) = &self.reporter {
            r.finish();
        }
    }
}
impl Channel {
    fn call_frames(
        self: &Arc<Self>,
        descriptor: &Arc<Descriptor>,
        request: &Request,
    ) -> CallFrames {
        let declared = descriptor
            .lookup(&request.contract, &request.operation)
            .map(|op| op.progress)
            .unwrap_or(false);
        let limits = request.progress.filter(|_| declared).map(|l| l.resolved());
        let reporter = limits.map(|l| {
            Arc::new(Reporter {
                channel: self.clone(),
                parent: request.id.clone(),
                limiter: Mutex::new(Limiter::new(l, self.now_ns())),
                finished: Mutex::new(false),
            })
        });
        let reverse = |parent: &str| Reverse {
            channel: self.clone(),
            descriptor: descriptor.clone(),
            parent: parent.into(),
            deadline: None,
        };
        CallFrames {
            reporter,
            limits,
            reverse: Some(reverse(&request.id)),
            session: self.threaded.then(|| reverse("")),
        }
    }
    fn run(self: &Arc<Self>, guest: &Guest, descriptor: &Arc<Descriptor>) -> Result<()> {
        let mut hello = false;
        while let Some(value) = self.next_request()? {
            let response = if !hello {
                guest::validate_hello(&value)?;
                hello = true;
                wire::response(
                    "hello",
                    Ok(serde_json::to_value(descriptor.as_ref()).map_err(|_| Error::Invalid)?),
                )?
            } else {
                let request: Request = wire::typed(value)?;
                let frames = self.call_frames(descriptor, &request);
                let kept = frames.clone();
                let response = guest.invoke_in(request, self.closed.clone(), Some(frames))?;
                kept.finish(); // a marker for events never reported, before the response
                response
            };
            // A violation found while the handler ran ends the session unanswered.
            {
                let mut s = lock(&self.state);
                if let (true, Some(f)) = (s.gone, s.failure) {
                    return Err(f.error());
                }
                s.busy = false; // before the response, so the next request is never early
            }
            self.write(&response)?;
        }
        Ok(())
    }
}

/// Serves `guest` until the host closes the connection: threaded where there are
/// threads, inline where there are not. A frame that breaks the rules ends the session
/// with `Error::Protocol` and nothing is written for it. `input` and `output` are
/// kept for as long as the connection lasts (`serve_stdio` is the usual caller).
pub fn serve<R, W>(guest: &Guest, input: R, output: W) -> Result<()>
where
    R: BufRead + Send + 'static,
    W: Write + Send + 'static,
{
    serve_with(guest, input, output, Mode::default())
}

/// Serves `guest` on this process's standard input and output. Stdout belongs to the
/// protocol; log to stderr.
pub fn serve_stdio(guest: &Guest) -> Result<()> {
    serve(
        guest,
        std::io::BufReader::new(std::io::stdin()),
        std::io::stdout(),
    )
}

pub fn serve_with<R, W>(guest: &Guest, input: R, output: W, mode: Mode) -> Result<()>
where
    R: BufRead + Send + 'static,
    W: Write + Send + 'static,
{
    let descriptor = Arc::new(guest.descriptor());
    let channel = Arc::new(Channel {
        out: Mutex::new(Box::new(output)),
        input: Mutex::new(None),
        state: Mutex::new(State::default()),
        changed: Condvar::new(),
        closed: Arc::new(AtomicBool::new(false)),
        threaded: mode == Mode::Threaded,
        started: Instant::now(),
    });
    let mut reader = None;
    match mode {
        Mode::Inline => *lock(&channel.input) = Some(Box::new(input)),
        Mode::Threaded => {
            let c = channel.clone();
            let mut input = input;
            reader = Some(
                std::thread::Builder::new()
                    .name("ext-plugin-reader".into())
                    .spawn(move || loop {
                        match read_frame(&mut input) {
                            Ok(Some(bytes)) => {
                                if c.route(&bytes).is_err() {
                                    c.end(Some(Failure::Protocol));
                                    return;
                                }
                            }
                            Ok(None) => return c.end(None),
                            Err(_) => return c.end(Some(Failure::Transport)),
                        }
                    })
                    .map_err(|_| Error::Unsupported)?,
            );
        }
    }
    let result = channel.run(guest, &descriptor);
    channel.end(None); // whatever still holds the connection finds it closed
    if result.is_ok() {
        if let Some(r) = reader {
            let _ = r.join(); // the reader ended with the input
        }
    }
    result
}
