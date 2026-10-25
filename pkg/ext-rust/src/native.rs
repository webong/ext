//! Native host backends. Each worker serializes calls. Deadlines stop waiting;
//! process close kills the owned child, while C cleanup waits for native return.
use crate::{guest::read_frame, host::Backend, wire, Error, Result, MAX_FRAME, VERSION};
use std::{
    collections::HashMap,
    io::{BufReader, Write},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::{mpsc, Arc, Mutex, OnceLock},
    time::{Duration, SystemTime},
};

type Task = (u32, Vec<u8>, mpsc::SyncSender<Result<Vec<u8>>>);
fn pinned_library(path: &Path) -> Result<Arc<libloading::Library>> {
    static IMAGES: OnceLock<Mutex<HashMap<PathBuf, Arc<libloading::Library>>>> = OnceLock::new();
    let canonical = std::fs::canonicalize(path).map_err(|_| Error::Transport)?;
    let mut images = IMAGES
        .get_or_init(|| Mutex::new(HashMap::new()))
        .lock()
        .map_err(|_| Error::Closed)?;
    if let Some(image) = images.get(&canonical) {
        return Ok(image.clone());
    }
    let image =
        Arc::new(unsafe { libloading::Library::new(&canonical) }.map_err(|_| Error::Transport)?);
    images.insert(canonical, image.clone());
    Ok(image)
}
pub struct Worker {
    send: Option<mpsc::SyncSender<Task>>,
    stop: Box<dyn FnMut() + Send>,
    done: mpsc::Receiver<()>,
    completed: bool,
}
impl Worker {
    fn new(
        mut call: impl FnMut(u32, Vec<u8>) -> Result<Vec<u8>> + Send + 'static,
        mut cleanup: impl FnMut() + Send + 'static,
        stop: impl FnMut() + Send + 'static,
    ) -> Self {
        let (send, receive) = mpsc::sync_channel::<Task>(1);
        let (finished, done) = mpsc::sync_channel(1);
        std::thread::spawn(move || {
            for (op, data, result) in receive {
                let answer = call(op, data);
                let _ = result.send(answer);
            }
            cleanup();
            let _ = finished.send(());
        });
        Self {
            send: Some(send),
            stop: Box::new(stop),
            done,
            completed: false,
        }
    }
    pub fn process(program: &Path, args: &[String]) -> Result<Self> {
        let mut child = Command::new(program)
            .args(args)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit())
            .spawn()
            .map_err(|_| Error::Transport)?;
        let mut input = child.stdin.take().ok_or(Error::Transport)?;
        let mut output = BufReader::new(child.stdout.take().ok_or(Error::Transport)?);
        let child = Arc::new(Mutex::new(child));
        let stop = child.clone();
        Ok(Self::new(
            move |op, data| {
                let request = if op == 1 {
                    let v = wire::decode(&data)?;
                    wire::encode(
                        &serde_json::json!({"apiVersion":VERSION,"id":"hello","operation":"plugin.hello","deadline":v["deadline"]}),
                    )?
                } else {
                    data
                };
                input
                    .write_all(&request)
                    .and_then(|_| input.write_all(b"\n"))
                    .and_then(|_| input.flush())
                    .map_err(|_| Error::Transport)?;
                let bytes = read_frame(&mut output)?.ok_or(Error::Transport)?;
                if op == 1 {
                    let v = wire::decode(&bytes)?;
                    wire::validate_response(&v, "hello")?;
                    if v.get("error").is_some_and(|e| !e.is_null()) {
                        return Err(Error::Transport);
                    }
                    wire::encode(&v["payload"])
                } else {
                    Ok(bytes)
                }
            },
            move || {
                if let Ok(mut c) = child.lock() {
                    let _ = c.kill();
                    let _ = c.wait();
                }
            },
            move || {
                if let Ok(mut c) = stop.lock() {
                    let _ = c.kill();
                }
            },
        ))
    }
    /// Load only inside Session::open's verified connect callback. Loading runs
    /// constructors synchronously. Libraries stay resident for process lifetime.
    pub fn cshared(path: &Path) -> Result<Self> {
        if !path.is_absolute() {
            return Err(Error::Invalid);
        }
        type Version = unsafe extern "C" fn() -> u32;
        type Open = unsafe extern "C" fn() -> u64;
        type Call = unsafe extern "C" fn(u64, u32, *const u8, u32, *mut u8, u32, *mut u32) -> u32;
        type Close = unsafe extern "C" fn(u64);
        // Pin before inspecting exports: constructors may create live runtimes.
        let library = pinned_library(path)?;
        unsafe {
            let version = *library
                .get::<Version>(b"ext_plugin_abi_version\0")
                .map_err(|_| Error::Unsupported)?;
            let open = *library
                .get::<Open>(b"ext_plugin_open\0")
                .map_err(|_| Error::Unsupported)?;
            let call = *library
                .get::<Call>(b"ext_plugin_call\0")
                .map_err(|_| Error::Unsupported)?;
            let close = *library
                .get::<Close>(b"ext_plugin_close\0")
                .map_err(|_| Error::Unsupported)?;
            if version() != 1 {
                return Err(Error::Unsupported);
            }
            let id = open();
            if id == 0 {
                return Err(Error::Denied);
            }
            Ok(Self::new(
                move |op, input| {
                    let mut out = vec![0u8; MAX_FRAME];
                    let mut len = 0u32;
                    let status = call(
                        id,
                        op,
                        input.as_ptr(),
                        input.len() as u32,
                        out.as_mut_ptr(),
                        MAX_FRAME as u32,
                        &mut len,
                    );
                    if status != 0 || len == 0 || len as usize > MAX_FRAME {
                        return Err(Error::Transport);
                    }
                    out.truncate(len as usize);
                    Ok(out)
                },
                move || close(id),
                || {},
            ))
        }
    }
}
impl Backend for Worker {
    fn exchange(&mut self, op: u32, input: Vec<u8>, deadline: SystemTime) -> Result<Vec<u8>> {
        if input.len() > MAX_FRAME {
            return Err(Error::Invalid);
        }
        let wait = deadline
            .duration_since(SystemTime::now())
            .map_err(|_| Error::Deadline)?;
        let (send, recv) = mpsc::sync_channel(1);
        self.send
            .as_ref()
            .ok_or(Error::Closed)?
            .try_send((op, input, send))
            .map_err(|_| Error::Closed)?;
        match recv.recv_timeout(wait) {
            Ok(result) => result,
            Err(_) => {
                self.close();
                Err(Error::Deadline)
            }
        }
    }
    fn close(&mut self) {
        if self.send.take().is_some() {
            (self.stop)();
        }
    }
    fn wait_closed(&mut self, timeout: Duration) -> Result<()> {
        if self.completed {
            return Ok(());
        }
        match self.done.recv_timeout(timeout) {
            Ok(()) => {
                self.completed = true;
                Ok(())
            }
            Err(mpsc::RecvTimeoutError::Timeout) => Err(Error::Deadline),
            Err(mpsc::RecvTimeoutError::Disconnected) => Err(Error::Transport),
        }
    }
}
impl Drop for Worker {
    fn drop(&mut self) {
        self.close();
    }
}
