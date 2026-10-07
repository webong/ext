//! Guest C ABI v1. Buffers remain owned by the foreign caller. Use export_guest!
//! from a cdylib crate. No allocator-owned pointer is returned to the caller.
use crate::{wire, Error, Guest, Result, MAX_FRAME};
use serde::Deserialize;
use std::{
    collections::HashMap,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
    time::SystemTime,
};

struct Entry {
    guest: Guest,
    hello: Mutex<bool>,
    closed: Arc<AtomicBool>,
}
struct State {
    next: u64,
    entries: HashMap<u64, Arc<Entry>>,
}
pub struct Server {
    state: Mutex<State>,
    factory: fn() -> Result<Guest>,
    capacity: usize,
}
impl Server {
    pub fn new(factory: fn() -> Result<Guest>, capacity: usize) -> Self {
        Self {
            state: Mutex::new(State {
                next: 0,
                entries: HashMap::new(),
            }),
            factory,
            capacity,
        }
    }
    pub fn open(&self) -> u64 {
        let Ok(mut state) = self.state.lock() else {
            return 0;
        };
        if state.entries.len() >= self.capacity || state.next == u64::MAX {
            return 0;
        }
        let Ok(guest) = (self.factory)() else {
            return 0;
        };
        state.next += 1;
        let id = state.next;
        state.entries.insert(
            id,
            Arc::new(Entry {
                guest,
                hello: Mutex::new(false),
                closed: Arc::new(AtomicBool::new(false)),
            }),
        );
        id
    }
    pub fn close(&self, id: u64) {
        if let Ok(mut state) = self.state.lock() {
            if let Some(e) = state.entries.remove(&id) {
                e.closed.store(true, Ordering::Release);
            }
        }
    }
    pub fn call(&self, id: u64, operation: u32, input: &[u8]) -> Result<Vec<u8>> {
        let e = self
            .state
            .lock()
            .map_err(|_| Error::Closed)?
            .entries
            .get(&id)
            .cloned()
            .ok_or(Error::Closed)?;
        let mut hello = e.hello.lock().map_err(|_| Error::Closed)?;
        if e.closed.load(Ordering::Acquire) {
            return Err(Error::Closed);
        }
        let v = wire::decode(input)?;
        let out = match operation {
            1 => {
                #[derive(Deserialize)]
                #[serde(deny_unknown_fields)]
                struct Hello {
                    deadline: String,
                }
                let h: Hello = wire::typed(v)?;
                if SystemTime::now() >= wire::parse_deadline(&h.deadline)? {
                    return Err(Error::Deadline);
                }
                *hello = true;
                serde_json::to_value(e.guest.descriptor()).map_err(|_| Error::Invalid)?
            }
            2 => {
                if !*hello {
                    return Err(Error::Invalid);
                }
                e.guest.invoke(wire::typed(v)?, e.closed.clone())?
            }
            _ => return Err(Error::Unsupported),
        };
        wire::encode(&out)
    }
    /// # Safety
    /// Non-null pointers must refer to valid non-overlapping buffers of the
    /// declared lengths for the duration of this call. No pointer is retained.
    #[allow(clippy::too_many_arguments)] // Mirrors the fixed CTX C ABI buffer signature.
    pub unsafe fn call_into(
        &self,
        id: u64,
        op: u32,
        input: *const u8,
        len: u32,
        out: *mut u8,
        cap: u32,
        written: *mut u32,
    ) -> u32 {
        if written.is_null() {
            return 1;
        }
        unsafe {
            *written = 0;
        }
        if input.is_null()
            || out.is_null()
            || len == 0
            || len as usize > MAX_FRAME
            || cap as usize != MAX_FRAME
        {
            return 1;
        }
        let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            self.call(id, op, unsafe {
                std::slice::from_raw_parts(input, len as usize)
            })
        }));
        match result {
            Ok(Ok(bytes)) => {
                unsafe {
                    std::ptr::copy_nonoverlapping(bytes.as_ptr(), out, bytes.len());
                    *written = bytes.len() as u32;
                }
                0
            }
            Ok(Err(Error::Closed)) => 2,
            Ok(Err(Error::Invalid | Error::Unsupported | Error::Mismatch)) => 1,
            _ => 3,
        }
    }
}
/// Export the four CTX C ABI symbols. The factory returns an independent Guest.
#[macro_export]
macro_rules! export_guest {
    ($factory:path) => {
        fn ext_server() -> &'static $crate::cabi::Server {
            static SERVER: std::sync::OnceLock<$crate::cabi::Server> = std::sync::OnceLock::new();
            SERVER.get_or_init(|| $crate::cabi::Server::new($factory, 64))
        }
        #[unsafe(no_mangle)]
        pub extern "C" fn ext_plugin_abi_version() -> u32 {
            1
        }
        #[unsafe(no_mangle)]
        pub extern "C" fn ext_plugin_open() -> u64 {
            std::panic::catch_unwind(|| ext_server().open()).unwrap_or(0)
        }
        #[unsafe(no_mangle)]
        pub extern "C" fn ext_plugin_close(id: u64) {
            let _ = std::panic::catch_unwind(|| ext_server().close(id));
        }
        #[unsafe(no_mangle)]
        pub unsafe extern "C" fn ext_plugin_call(
            id: u64,
            op: u32,
            input: *const u8,
            len: u32,
            out: *mut u8,
            cap: u32,
            written: *mut u32,
        ) -> u32 {
            unsafe { ext_server().call_into(id, op, input, len, out, cap, written) }
        }
    };
}
