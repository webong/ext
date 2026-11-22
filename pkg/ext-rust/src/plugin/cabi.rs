//! Guest C ABI v1. Buffers remain owned by the foreign caller. Use export_guest!
//! from a cdylib crate. No allocator-owned pointer is returned to the caller.
use crate::plugin::{wire, Error, Guest, Result, MAX_FRAME};
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
                let request: crate::plugin::Request = wire::typed(v)?;
                // A reactor's guest may call its host while this invocation runs.
                #[cfg(target_arch = "wasm32")]
                if !e.guest.descriptor().reverse.is_empty() {
                    let reverse = crate::plugin::serve::Reverse::reactor(
                        Arc::new(e.guest.descriptor()),
                        &request.id,
                    );
                    let frames = crate::plugin::serve::CallFrames {
                        reporter: None,
                        limits: None,
                        reverse: Some(reverse),
                        session: None,
                    };
                    return wire::encode(&e.guest.invoke_in(
                        request,
                        e.closed.clone(),
                        Some(frames),
                    )?);
                }
                e.guest.invoke(request, e.closed.clone())?
            }
            _ => return Err(Error::Unsupported),
        };
        wire::encode(&out)
    }
    /// # Safety
    /// Non-null pointers must refer to valid non-overlapping buffers of the
    /// declared lengths for the duration of this call. No pointer is retained.
    #[allow(clippy::too_many_arguments)] // Mirrors the fixed ext C ABI buffer signature.
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
/// Sends one reverse_request frame through the reactor's ext_plugin.reverse import
/// and returns the reverse_response frame (docs/plugin-reactor-abi.md). A host that
/// serves no reverse calls here answers status 2, reported as reverse_unavailable.
#[cfg(target_arch = "wasm32")]
pub(crate) fn reactor_reverse(frame: &[u8]) -> Result<Vec<u8>> {
    #[link(wasm_import_module = "ext_plugin")]
    extern "C" {
        fn reverse(
            request: *const u8,
            request_len: u32,
            response: *mut u8,
            capacity: u32,
            written: *mut u32,
        ) -> u32;
    }
    // One response buffer of the maximum frame size, kept for the instance, which
    // runs one call at a time.
    static BUFFER: Mutex<Vec<u8>> = Mutex::new(Vec::new());
    if frame.is_empty() || frame.len() > MAX_FRAME {
        return Err(Error::Invalid);
    }
    let mut buffer = BUFFER.lock().map_err(|_| Error::Closed)?;
    if buffer.len() != MAX_FRAME {
        *buffer = vec![0; MAX_FRAME];
    }
    let mut written = 0u32;
    let status = unsafe {
        reverse(
            frame.as_ptr(),
            frame.len() as u32,
            buffer.as_mut_ptr(),
            MAX_FRAME as u32,
            &mut written,
        )
    };
    match status {
        0 if (written as usize) <= MAX_FRAME => Ok(buffer[..written as usize].to_vec()),
        2 => Err(crate::plugin::serve::refused(
            crate::plugin::frames::REVERSE_UNAVAILABLE,
        )),
        1 => Err(Error::Invalid),
        _ => Err(Error::Closed),
    }
}
/// Allocate `size` bytes for a WebAssembly host to fill. The pointer is 16-byte
/// aligned and null on failure or when `size` is zero. The host releases it with
/// [`reactor_free`] and the same size.
pub fn reactor_alloc(size: u32) -> *mut u8 {
    match std::alloc::Layout::from_size_align(size as usize, 16) {
        Ok(layout) if size != 0 => unsafe { std::alloc::alloc(layout) },
        _ => std::ptr::null_mut(),
    }
}

/// Release memory from [`reactor_alloc`].
///
/// # Safety
/// `ptr` must come from `reactor_alloc(size)` and not have been freed.
pub unsafe fn reactor_free(ptr: *mut u8, size: u32) {
    if ptr.is_null() {
        return;
    }
    if let Ok(layout) = std::alloc::Layout::from_size_align(size as usize, 16) {
        unsafe { std::alloc::dealloc(ptr, layout) }
    }
}

/// Export the four ext C ABI symbols. The factory returns an independent Guest.
/// For wasm32 targets it also exports `ext_plugin_guest_alloc` and `ext_plugin_guest_free`,
/// which make the module a WebAssembly reactor; see docs/plugin-reactor-abi.md.
#[doc(hidden)]
#[macro_export]
macro_rules! __plugin_export_guest {
    ($factory:path) => {
        #[cfg(target_arch = "wasm32")]
        #[unsafe(no_mangle)]
        pub extern "C" fn ext_plugin_guest_alloc(size: u32) -> u32 {
            $crate::plugin::cabi::reactor_alloc(size) as usize as u32
        }
        #[cfg(target_arch = "wasm32")]
        #[unsafe(no_mangle)]
        pub unsafe extern "C" fn ext_plugin_guest_free(ptr: u32, size: u32) {
            unsafe { $crate::plugin::cabi::reactor_free(ptr as usize as *mut u8, size) }
        }
        fn ext_server() -> &'static $crate::plugin::cabi::Server {
            static SERVER: std::sync::OnceLock<$crate::plugin::cabi::Server> =
                std::sync::OnceLock::new();
            SERVER.get_or_init(|| $crate::plugin::cabi::Server::new($factory, 64))
        }
        #[unsafe(no_mangle)]
        pub extern "C" fn ext_plugin_guest_abi_version() -> u32 {
            1
        }
        #[unsafe(no_mangle)]
        pub extern "C" fn ext_plugin_guest_open() -> u64 {
            std::panic::catch_unwind(|| ext_server().open()).unwrap_or(0)
        }
        #[unsafe(no_mangle)]
        pub extern "C" fn ext_plugin_guest_close(id: u64) {
            let _ = std::panic::catch_unwind(|| ext_server().close(id));
        }
        #[unsafe(no_mangle)]
        pub unsafe extern "C" fn ext_plugin_guest_call(
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
#[doc(inline)]
pub use __plugin_export_guest as export_guest;
