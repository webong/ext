//! The router behind `serve`: progress events, reverse calls and the rules a host
//! that breaks them meets, in threaded and in inline mode. The host is a test
//! thread on in-memory pipes.
use ext_plugin::{
    frames::{check_progress_frame, check_reverse_request, Progress, ProgressOutcome},
    guest::{serve_with, Registry},
    ContractRef, Error, Guest, Identity, Method, Mode, Operation, RemoteError,
};
use serde_json::{json, Value};
use std::{
    io::{self, BufRead, Read, Write},
    sync::{atomic::AtomicBool, mpsc, Arc},
    thread::{self, JoinHandle},
    time::{Duration, SystemTime},
};

struct PipeIn {
    rx: mpsc::Receiver<Vec<u8>>,
    buf: Vec<u8>,
    pos: usize,
}
impl Read for PipeIn {
    fn read(&mut self, out: &mut [u8]) -> io::Result<usize> {
        let n = {
            let b = self.fill_buf()?;
            let n = b.len().min(out.len());
            out[..n].copy_from_slice(&b[..n]);
            n
        };
        self.consume(n);
        Ok(n)
    }
}
impl BufRead for PipeIn {
    fn fill_buf(&mut self) -> io::Result<&[u8]> {
        if self.pos >= self.buf.len() {
            self.buf = self.rx.recv().unwrap_or_default(); // empty at the end of input
            self.pos = 0;
        }
        Ok(&self.buf[self.pos..])
    }
    fn consume(&mut self, n: usize) {
        self.pos += n;
    }
}
struct PipeOut(mpsc::Sender<Vec<u8>>);
impl Write for PipeOut {
    fn write(&mut self, b: &[u8]) -> io::Result<usize> {
        let _ = self.0.send(b.to_vec());
        Ok(b.len())
    }
    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

fn contract() -> ContractRef {
    ContractRef {
        name: "test.frames".into(),
        version: "v1".into(),
    }
}
fn host_ref() -> ContractRef {
    ContractRef {
        name: "host.svc".into(),
        version: "v1".into(),
    }
}
fn guest() -> Guest {
    let mut r = Registry::new(Identity {
        id: "test/frames".into(),
        revision: "r1".into(),
        version: String::new(),
    })
    .unwrap();
    for (name, progress) in [
        ("events", true),
        ("flood", true),
        ("quiet", false),
        ("ask", false),
        ("late", false),
        ("hold", false),
        ("session", false),
    ] {
        let m = Method::<Value, Value>::new(
            contract(),
            Operation {
                progress,
                ..Operation::new(name)
            },
        );
        r.register(m, |ctx, req, input| match req.operation.as_str() {
            "events" => {
                let first = ctx.progress(Progress::log("a"))?;
                let second = ctx.progress(Progress::fraction(1, 2))?;
                Ok(json!({"sent": [first == ProgressOutcome::Sent, second == ProgressOutcome::Sent]}))
            }
            "flood" => {
                let mut dropped = 0;
                for _ in 0..300 {
                    if ctx.progress(Progress::log("x"))? == ProgressOutcome::Dropped {
                        dropped += 1;
                    }
                }
                Ok(json!({"dropped": dropped}))
            }
            "quiet" => Ok(json!({"unsupported": matches!(ctx.progress(Progress::log("x")), Err(Error::Unsupported))})),
            "ask" => {
                let reverse = ctx.reverse().ok_or(Error::Unsupported)?;
                let op = input["op"].as_str().unwrap_or("sign");
                match reverse.call(&host_ref(), op, &input["payload"]) {
                    Ok(v) => Ok(json!({"result": v})),
                    Err(Error::Remote(r)) => Ok(json!({"code": r.code})),
                    Err(Error::Closed) => Ok(json!({"closed": true})),
                    Err(e) => Ok(json!({"failed": format!("{e:?}")})),
                }
            }
            "late" => {
                let reverse = ctx.reverse().ok_or(Error::Unsupported)?;
                let r = reverse.call_by(
                    &host_ref(),
                    "sign",
                    &Value::Null,
                    SystemTime::now() + Duration::from_millis(150),
                );
                Ok(json!({"deadline": matches!(r, Err(Error::Deadline))}))
            }
            "hold" => loop {
                ctx.check()?;
                thread::sleep(Duration::from_millis(2));
            },
            "session" => Ok(json!({"session": ctx.session_reverse().is_some(), "concurrent": ctx.reverse().is_some_and(|r| r.concurrent())})),
            _ => Err(Error::Unsupported),
        })
        .unwrap();
    }
    r.declare_reverse(host_ref(), vec![Operation::new("sign")])
        .unwrap();
    let d = r.descriptor();
    r.guest(Arc::new(move |req| req.validate(&d))).unwrap()
}

struct Host {
    tx: Option<mpsc::Sender<Vec<u8>>>,
    rx: mpsc::Receiver<Vec<u8>>,
    line: Vec<u8>,
    served: Option<JoinHandle<Result<(), Error>>>,
    next: u32,
}
impl Host {
    fn start(mode: Mode) -> Self {
        let (to_guest, input) = mpsc::channel();
        let (output, from_guest) = mpsc::channel();
        let g = guest();
        let served = thread::spawn(move || {
            serve_with(
                &g,
                PipeIn {
                    rx: input,
                    buf: Vec::new(),
                    pos: 0,
                },
                PipeOut(output),
                mode,
            )
        });
        let host = Host {
            tx: Some(to_guest),
            rx: from_guest,
            line: Vec::new(),
            served: Some(served),
            next: 0,
        };
        let deadline = SystemTime::now() + Duration::from_secs(30);
        host.send(
            json!({"apiVersion":"ext.plugin/v1","id":"hello","operation":"plugin.hello",
            "deadline": ext_plugin::wire::timestamp(deadline).unwrap()}),
        );
        host
    }
    fn send(&self, v: Value) {
        let mut bytes = serde_json::to_vec(&v).unwrap();
        bytes.push(b'\n');
        self.tx.as_ref().unwrap().send(bytes).unwrap();
    }
    /// The next frame the guest wrote, if one comes within `wait`.
    fn try_recv(&mut self, wait: Duration) -> Option<Value> {
        let end = std::time::Instant::now() + wait;
        loop {
            if let Some(n) = self.line.iter().position(|b| *b == b'\n') {
                let frame: Vec<u8> = self.line.drain(..=n).collect();
                return Some(serde_json::from_slice(&frame[..n]).unwrap());
            }
            let left = end.saturating_duration_since(std::time::Instant::now());
            match self.rx.recv_timeout(left) {
                Ok(bytes) => self.line.extend(bytes),
                Err(_) => return None,
            }
        }
    }
    fn recv(&mut self) -> Value {
        self.try_recv(Duration::from_secs(5))
            .expect("the guest wrote nothing")
    }
    /// A request for `operation`; a call whose operation reports progress asks for events.
    fn request(&mut self, operation: &str, payload: Value, progress: bool) -> String {
        self.next += 1;
        let id = self.next.to_string();
        let mut r = json!({
            "apiVersion":"ext.plugin/v1","id":id,
            "plugin":{"id":"test/frames","revision":"r1"},
            "contract":{"name":"test.frames","version":"v1"},
            "operation":operation,"payload":payload,
            "deadline": ext_plugin::wire::timestamp(SystemTime::now() + Duration::from_secs(30)).unwrap(),
        });
        if progress {
            r["progress"] = json!({"eventsPerSecond": 5});
        }
        self.send(r);
        id
    }
    /// Reads frames up to the response to `id`, returning those before it and it.
    fn until_response(&mut self, id: &str) -> (Vec<Value>, Value) {
        let mut before = Vec::new();
        loop {
            let frame = self.recv();
            if frame.get("frame").is_none() && frame["id"] == id {
                return (before, frame);
            }
            before.push(frame);
        }
    }
    fn finish(&mut self) -> Result<(), Error> {
        self.tx = None; // end of input
        self.served.take().unwrap().join().unwrap()
    }
}

const MODES: [Mode; 2] = [Mode::Threaded, Mode::Inline];

fn started(mode: Mode) -> Host {
    let mut host = Host::start(mode);
    let hello = host.recv();
    assert_eq!(hello["id"], "hello");
    host
}

#[test]
fn the_descriptor_declares_progress_and_reverse() {
    for mode in MODES {
        let mut host = Host::start(mode);
        let hello = host.recv();
        let ops = &hello["payload"]["contracts"][0]["operations"];
        assert!(ops
            .as_array()
            .unwrap()
            .iter()
            .any(|o| o["name"] == "events" && o["progress"] == true));
        assert!(ops
            .as_array()
            .unwrap()
            .iter()
            .all(|o| o["name"] != "quiet" || o.get("progress").is_none()));
        assert_eq!(hello["payload"]["reverse"][0]["name"], "host.svc");
        host.finish().unwrap();
    }
}

#[test]
fn events_come_in_order_before_the_response() {
    for mode in MODES {
        let mut host = started(mode);
        let id = host.request("events", Value::Null, true);
        let (events, response) = host.until_response(&id);
        assert_eq!(events.len(), 2, "{mode:?}: {events:?}");
        for e in &events {
            check_progress_frame(&serde_json::to_vec(e).unwrap()).unwrap();
            assert_eq!(e["parent"], id.as_str());
        }
        assert_eq!(
            (events[0]["kind"].clone(), events[0]["text"].clone()),
            (json!("log"), json!("a"))
        );
        assert_eq!(
            (
                events[1]["kind"].clone(),
                events[1]["completed"].clone(),
                events[1]["total"].clone()
            ),
            (json!("progress"), json!(1), json!(2))
        );
        assert_eq!(response["payload"]["sent"], json!([true, true]));
        assert!(
            host.try_recv(Duration::from_millis(100)).is_none(),
            "{mode:?}: a frame after the response"
        );
        host.finish().unwrap();
    }
}

#[test]
fn a_flood_is_limited_counted_and_closed_with_a_marker() {
    for mode in MODES {
        let mut host = started(mode);
        let id = host.request("flood", Value::Null, true);
        let (events, response) = host.until_response(&id);
        let dropped = response["payload"]["dropped"].as_u64().unwrap();
        let delivered = events.iter().filter(|e| e.get("text").is_some()).count() as u64;
        let reported: u64 = events
            .iter()
            .map(|e| e["dropped"].as_u64().unwrap_or(0))
            .sum();
        assert!(
            delivered >= 10 && delivered + dropped == 300 && reported == dropped,
            "{mode:?}: {delivered} delivered, {dropped} dropped, {reported} reported"
        );
        // The drops still unreported when the handler returned are in a marker with
        // only a kind and a count.
        let last = events.last().unwrap();
        assert!(
            last.get("text").is_none()
                && last["kind"] == "log"
                && last["dropped"].as_u64().unwrap() > 0,
            "{mode:?}: {last}"
        );
        host.finish().unwrap();
    }
}

#[test]
fn an_operation_that_did_not_declare_progress_cannot_report() {
    for mode in MODES {
        let mut host = started(mode);
        let id = host.request("quiet", Value::Null, false);
        let (events, response) = host.until_response(&id);
        assert!(events.is_empty());
        assert_eq!(response["payload"]["unsupported"], true);
        host.finish().unwrap();
    }
}

#[test]
fn a_reverse_call_names_the_call_and_returns_the_answer() {
    for mode in MODES {
        let mut host = started(mode);
        let id = host.request("ask", json!({"op": "sign", "payload": {"n": 1}}), false);
        let request = host.recv();
        check_reverse_request(&serde_json::to_vec(&request).unwrap()).unwrap();
        assert_eq!(request["frame"], "reverse_request");
        assert_eq!(
            (
                request["id"].clone(),
                request["parent"].clone(),
                request["operation"].clone()
            ),
            (json!("g1"), json!(id), json!("sign"))
        );
        assert_eq!(
            request["contract"],
            json!({"name":"host.svc","version":"v1"})
        );
        assert_eq!(request["payload"], json!({"n": 1}));
        host.send(json!({"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":"signed"}));
        let (_, response) = host.until_response(&id);
        assert_eq!(response["payload"]["result"], "signed");
        // The host's error comes back with its code, and ids go on counting.
        let id = host.request("ask", json!({"op": "sign"}), false);
        let request = host.recv();
        assert_eq!(request["id"], "g2");
        assert!(request.get("payload").is_none());
        host.send(json!({"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g2","error":{"code":"denied","message":"reverse call refused"}}));
        let (_, response) = host.until_response(&id);
        assert_eq!(response["payload"]["code"], "denied");
        // An operation the descriptor does not list is refused without a frame.
        let id = host.request("ask", json!({"op": "nothere"}), false);
        let (frames, response) = host.until_response(&id);
        assert!(frames.is_empty());
        assert_eq!(response["payload"]["code"], "reverse_undeclared");
        host.finish().unwrap();
    }
}

#[test]
fn an_answer_to_an_abandoned_request_is_dropped() {
    // Only a reader thread can give up on a request at its deadline.
    let mut host = started(Mode::Threaded);
    let id = host.request("late", Value::Null, false);
    let request = host.recv();
    assert_eq!(request["id"], "g1");
    let (_, response) = host.until_response(&id);
    assert_eq!(response["payload"]["deadline"], true);
    host.send(
        json!({"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":1}),
    );
    let id = host.request("events", Value::Null, true);
    let (_, response) = host.until_response(&id);
    assert_eq!(response["payload"]["sent"], json!([true, true]));
    host.finish().unwrap();
}

#[test]
fn what_the_host_may_not_do_ends_the_session() {
    let answer = |id: &str| json!({"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":id,"payload":1});
    for mode in MODES {
        let violations: Vec<(&str, Value)> = vec![
            ("a second request during a call", Value::Null),
            ("an unknown frame", json!({"frame":"mystery"})),
            ("an answer to a request never issued", answer("g99")),
            (
                "a progress frame from the host",
                json!({"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"log"}),
            ),
            (
                "a malformed answer to the request waiting",
                json!({"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":1,"extra":1}),
            ),
            (
                "an answer with both payload and error",
                json!({"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":1,"error":{"code":"x","message":"m"}}),
            ),
        ];
        for (what, frame) in violations {
            let mut host = started(mode);
            let id = host.request("ask", json!({"op": "sign"}), false);
            assert_eq!(host.recv()["id"], "g1");
            if frame.is_null() {
                host.request("events", Value::Null, true);
            } else {
                host.send(frame);
            }
            assert!(
                matches!(host.finish(), Err(Error::Protocol)),
                "{mode:?}: {what}"
            );
            // The call that was in flight is never answered.
            while let Some(frame) = host.try_recv(Duration::from_millis(50)) {
                assert!(
                    frame.get("frame").is_some() || frame["id"] != id.as_str(),
                    "{mode:?}: {what}: answered {frame}"
                );
            }
        }
    }
}

#[test]
fn a_connection_that_ends_releases_a_waiting_handler() {
    for mode in MODES {
        let mut host = started(mode);
        let id = host.request("ask", json!({"op": "sign"}), false);
        assert_eq!(host.recv()["id"], "g1");
        host.tx = None; // the host goes away
                        // The handler is released, and a call cut off by the end of the connection
                        // is answered as a failed one.
        let (_, response) = host.until_response(&id);
        assert_eq!(response["error"]["code"], "operation_failed", "{mode:?}");
        host.finish().unwrap();
    }
}

#[test]
fn a_handler_is_cancelled_when_the_connection_ends() {
    let mut host = started(Mode::Threaded);
    let id = host.request("hold", Value::Null, false);
    thread::sleep(Duration::from_millis(50));
    host.tx = None;
    let (_, response) = host.until_response(&id);
    assert_eq!(response["error"]["code"], "operation_failed");
    host.finish().unwrap();
}

#[test]
fn only_a_reader_thread_gives_a_session_scoped_caller() {
    for (mode, expected) in [(Mode::Threaded, true), (Mode::Inline, false)] {
        let mut host = started(mode);
        let id = host.request("session", Value::Null, false);
        let (_, response) = host.until_response(&id);
        assert_eq!(
            response["payload"],
            json!({"session": expected, "concurrent": expected}),
            "{mode:?}"
        );
        host.finish().unwrap();
    }
}

#[test]
fn a_transport_without_frames_gives_no_reverse_caller_and_no_reporter() {
    let g = guest();
    let request = |op: &str| -> ext_plugin::Request {
        serde_json::from_value(json!({
            "apiVersion":"ext.plugin/v1","id":"1",
            "plugin":{"id":"test/frames","revision":"r1"},
            "contract":{"name":"test.frames","version":"v1"},
            "operation":op,"payload":{},
            "deadline": ext_plugin::wire::timestamp(SystemTime::now() + Duration::from_secs(5)).unwrap(),
            "progress":{}
        }))
        .unwrap()
    };
    let v = g
        .invoke(request("events"), Arc::new(AtomicBool::new(false)))
        .unwrap();
    assert_eq!(v["error"]["code"], "operation_failed"); // events needs a reporter
    let v = g
        .invoke(request("ask"), Arc::new(AtomicBool::new(false)))
        .unwrap();
    assert_eq!(v["error"]["code"], "operation_failed"); // ask needs a reverse caller
    let v = g
        .invoke(request("session"), Arc::new(AtomicBool::new(false)))
        .unwrap();
    assert_eq!(v["payload"], json!({"session": false, "concurrent": false}));
    let _ = RemoteError {
        code: String::new(),
        message: String::new(),
        retry_after_milliseconds: 0,
    };
}
