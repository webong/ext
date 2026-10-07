//! Rust ownership and callback bindings for the CTX C engine. Wire values remain
//! JSON bytes; consumers choose their own serializer and domain types. Requires
//! ABI v2, currently Linux/macOS. No Go runtime or Rust protocol engine is linked.
pub mod ffi;
#[cfg(not(feature = "guest-only"))]
pub mod resources;
use std::{
    ffi::{c_void, CStr, CString},
    fmt,
    mem::size_of,
    panic::{catch_unwind, AssertUnwindSafe},
    ptr, slice,
    time::Duration,
};
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Error(pub i32);
impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        unsafe {
            write!(
                f,
                "{}",
                CStr::from_ptr(ffi::ext_host_status_string(self.0)).to_string_lossy()
            )
        }
    }
}
impl std::error::Error for Error {}
fn check(s: i32) -> Result<(), Error> {
    if s == 0 {
        Ok(())
    } else {
        Err(Error(s))
    }
}
fn cstring(s: &str) -> Result<CString, Error> {
    CString::new(s).map_err(|_| Error(1))
}
fn milliseconds(d: Duration) -> Result<u32, Error> {
    if d.is_zero() || d.as_millis() > u32::MAX as u128 {
        return Err(Error(1));
    }
    Ok(d.as_millis().max(1) as u32)
}
fn output(f: impl FnOnce(*mut ffi::Buffer) -> i32) -> Result<Vec<u8>, Error> {
    let mut b = ffi::Buffer {
        data: ptr::null_mut(),
        len: 0,
    };
    let status = f(&mut b);
    let result = if status != 0 {
        Err(Error(status))
    } else if b.len == 0 {
        Ok(Vec::new())
    } else {
        Ok(unsafe { slice::from_raw_parts(b.data, b.len) }.to_vec())
    };
    unsafe { ffi::ext_buffer_free(&mut b) };
    result
}
pub struct Cancellation(*mut c_void);
// The C signal is atomic. Its lifetime is pinned by borrowed CallOptions.
unsafe impl Send for Cancellation {}
unsafe impl Sync for Cancellation {}
impl Cancellation {
    pub fn new() -> Result<Self, Error> {
        let mut p = ptr::null_mut();
        check(unsafe { ffi::ext_cancel_create(&mut p) })?;
        Ok(Self(p))
    }
    pub fn cancel(&self) {
        unsafe { ffi::ext_cancel_signal(self.0) }
    }
    pub fn is_canceled(&self) -> bool {
        unsafe { ffi::ext_cancel_is_signaled(self.0) != 0 }
    }
}
impl Drop for Cancellation {
    fn drop(&mut self) {
        unsafe { ffi::ext_cancel_destroy(self.0) }
    }
}
#[derive(Clone, Copy)]
pub struct CallOptions<'a> {
    pub timeout: Duration,
    pub cancel: Option<&'a Cancellation>,
}
impl Default for CallOptions<'_> {
    fn default() -> Self {
        Self {
            timeout: Duration::from_secs(30),
            cancel: None,
        }
    }
}
#[cfg(not(feature = "guest-only"))]
impl CallOptions<'_> {
    fn raw(&self) -> Result<ffi::Call, Error> {
        Ok(ffi::Call {
            struct_size: size_of::<ffi::Call>() as u32,
            timeout_ms: milliseconds(self.timeout)?,
            cancel: self.cancel.map_or(ptr::null(), |s| s.0),
            value: ptr::null_mut(),
        })
    }
}
#[cfg(not(feature = "guest-only"))]
pub type Policy = Box<dyn Fn(&[u8]) -> Result<(), Error> + Send + Sync>;
#[cfg(not(feature = "guest-only"))]
pub type Observer = Box<dyn Fn(&[u8]) + Send + Sync>;
#[cfg(not(feature = "guest-only"))]
struct Policies {
    verify: Policy,
    authorize: Policy,
    observe: Option<Observer>,
}
#[cfg(not(feature = "guest-only"))]
unsafe extern "C" fn observe(user: *mut c_void, _: *const ffi::Call, p: *const u8, n: usize) {
    let _ = catch_unwind(AssertUnwindSafe(|| {
        if let Some(f) = &(*user.cast::<Policies>()).observe {
            f(slice::from_raw_parts(p, n));
        }
    }));
}
#[cfg(not(feature = "guest-only"))]
unsafe extern "C" fn verify(p: *mut c_void, data: *const u8, n: usize) -> i32 {
    policy(p, data, n, true)
}
#[cfg(not(feature = "guest-only"))]
unsafe extern "C" fn authorize(p: *mut c_void, data: *const u8, n: usize) -> i32 {
    policy(p, data, n, false)
}
#[cfg(not(feature = "guest-only"))]
unsafe fn policy(p: *mut c_void, data: *const u8, n: usize, verify: bool) -> i32 {
    catch_unwind(AssertUnwindSafe(|| {
        let p = &*p.cast::<Policies>();
        let f = if verify { &p.verify } else { &p.authorize };
        if f(slice::from_raw_parts(data, n)).is_ok() {
            0
        } else {
            1
        }
    }))
    .unwrap_or(1)
}
/// A shared host session. Policy callbacks must not reenter this same host.
/// Rust borrows ensure Drop cannot race a call. Raw FFI users own that guarantee.
#[cfg(not(feature = "guest-only"))]
pub struct Host {
    raw: *mut c_void,
    _policies: Box<Policies>,
}
#[cfg(not(feature = "guest-only"))]
unsafe impl Send for Host {}
#[cfg(not(feature = "guest-only"))]
unsafe impl Sync for Host {}
#[derive(Default)]
#[cfg(not(feature = "guest-only"))]
pub struct Process<'a> {
    pub executable: &'a str,
    pub arguments: &'a [&'a str],
    pub environment: &'a [&'a str],
}
#[cfg(not(feature = "guest-only"))]
impl Host {
    pub fn process(
        process: Process<'_>,
        descriptor: &[u8],
        verify: Policy,
        authorize: Policy,
    ) -> Result<Self, Error> {
        if unsafe { ffi::ext_host_abi_version() } != 2 {
            return Err(Error(3));
        }
        let path = cstring(process.executable)?;
        let args: Vec<CString> = process
            .arguments
            .iter()
            .map(|s| cstring(s))
            .collect::<Result<_, _>>()?;
        let env: Vec<CString> = process
            .environment
            .iter()
            .map(|s| cstring(s))
            .collect::<Result<_, _>>()?;
        let argv: Vec<_> = args.iter().map(|s| s.as_ptr()).collect();
        let envp: Vec<_> = env.iter().map(|s| s.as_ptr()).collect();
        let process = ffi::Process {
            struct_size: size_of::<ffi::Process>() as u32,
            executable: path.as_ptr(),
            arguments: argv.as_ptr(),
            argument_count: argv.len(),
            environment: envp.as_ptr(),
            environment_count: envp.len(),
        };
        let backend = ffi::Backend {
            struct_size: size_of::<ffi::Backend>() as u32,
            kind: 4,
            config: (&process as *const ffi::Process).cast(),
            config_size: size_of::<ffi::Process>(),
        };
        let mut policies = Box::new(Policies {
            verify,
            authorize,
            observe: None,
        });
        let options = ffi::HostOptions {
            abi_version: 2,
            struct_size: size_of::<ffi::HostOptions>() as u32,
            descriptor: descriptor.as_ptr(),
            descriptor_len: descriptor.len(),
            verify: crate::verify,
            authorize: crate::authorize,
            user: (&mut *policies as *mut Policies).cast(),
        };
        let mut raw = ptr::null_mut();
        check(unsafe { ffi::ext_host_create(&options, &backend, &mut raw) })?;
        Ok(Self {
            raw,
            _policies: policies,
        })
    }
    pub fn start(&self, options: CallOptions<'_>) -> Result<(), Error> {
        check(unsafe { ffi::ext_host_start_with_options(self.raw, &options.raw()?) })
    }
    /// Install before start. Receives metadata-only JSON synchronously; must be
    /// fast, nonblocking, and must not reenter this host. Panics are contained.
    pub fn with_observer(mut self, observer: Observer) -> Result<Self, Error> {
        self._policies.observe = Some(observer);
        let hooks = ffi::Hooks {
            struct_size: size_of::<ffi::Hooks>() as u32,
            user: (&mut *self._policies as *mut Policies).cast(),
            verify: None,
            authorize: None,
            observe: Some(observe),
        };
        check(unsafe { ffi::ext_host_set_hooks(self.raw, &hooks) })?;
        Ok(self)
    }
    pub fn invoke(&self, request: &[u8], options: CallOptions<'_>) -> Result<Vec<u8>, Error> {
        let o = options.raw()?;
        output(|out| unsafe {
            ffi::ext_host_invoke_with_options(self.raw, request.as_ptr(), request.len(), &o, out)
        })
    }
    /// Generate envelope identity, surface, ID and deadline from
    /// {"contract":{"name":...,"version":...},"operation":...,"payload":...}.
    pub fn call(&self, input: &[u8], options: CallOptions<'_>) -> Result<Vec<u8>, Error> {
        let o = options.raw()?;
        output(|out| unsafe { ffi::ext_host_call(self.raw, input.as_ptr(), input.len(), &o, out) })
    }
    pub fn drain(&self, options: CallOptions<'_>) -> Result<(), Error> {
        check(unsafe { ffi::ext_host_drain_with_options(self.raw, &options.raw()?) })
    }
    pub fn close(&self) {
        unsafe { ffi::ext_host_close(self.raw) }
    }
    pub fn state(&self) -> i32 {
        unsafe { ffi::ext_host_get_state(self.raw) }
    }
}
#[cfg(not(feature = "guest-only"))]
impl Drop for Host {
    fn drop(&mut self) {
        unsafe { ffi::ext_host_destroy(self.raw) }
    }
}
pub struct GuestCall<'a> {
    pub request: &'a [u8],
    pub remaining: Duration,
    pub cancel: Option<&'a Cancellation>,
}
pub enum GuestReply {
    Payload(Vec<u8>),
    PublicError(Vec<u8>),
}
pub type Handler = Box<dyn for<'a> Fn(GuestCall<'a>) -> Result<GuestReply, Error> + Send + Sync>;
struct Dispatch {
    handler: Handler,
}
pub struct Guest {
    raw: *mut c_void,
    _handler: Box<Dispatch>,
}
unsafe impl Send for Guest {}
unsafe impl Sync for Guest {}
unsafe extern "C" fn handle(
    user: *mut c_void,
    call_user: *mut c_void,
    p: *const u8,
    n: usize,
    ms: u32,
    emit: ffi::GuestEmit,
    sink: *mut c_void,
) -> i32 {
    catch_unwind(AssertUnwindSafe(|| {
        let handler = &*user.cast::<Dispatch>();
        let options = &*call_user.cast::<CallOptions<'_>>();
        if options.cancel.is_some_and(Cancellation::is_canceled) {
            return 12;
        }
        let result = (handler.handler)(GuestCall {
            request: slice::from_raw_parts(p, n),
            remaining: Duration::from_millis(ms as u64),
            cancel: options.cancel,
        });
        if options.cancel.is_some_and(Cancellation::is_canceled) {
            return 12;
        }
        match result {
            Ok(GuestReply::Payload(v)) => emit(sink, 0, v.as_ptr(), v.len()),
            Ok(GuestReply::PublicError(v)) => emit(sink, 1, v.as_ptr(), v.len()),
            Err(e) => e.0,
        }
    }))
    .unwrap_or(7)
}
impl Guest {
    pub fn new(descriptor: &[u8], max_call: Duration, handler: Handler) -> Result<Self, Error> {
        let mut handler = Box::new(Dispatch { handler });
        let o = ffi::GuestOptions {
            abi_version: 2,
            struct_size: size_of::<ffi::GuestOptions>() as u32,
            descriptor: descriptor.as_ptr(),
            descriptor_len: descriptor.len(),
            max_call_ms: milliseconds(max_call)?,
            user: (&mut *handler as *mut Dispatch).cast(),
            handle,
        };
        let mut raw = ptr::null_mut();
        check(unsafe { ffi::ext_guest_create(&o, &mut raw) })?;
        Ok(Self {
            raw,
            _handler: handler,
        })
    }
    pub fn descriptor(&self) -> Result<Vec<u8>, Error> {
        output(|out| unsafe { ffi::ext_guest_descriptor(self.raw, out) })
    }
    pub fn invoke(&self, request: &[u8], options: CallOptions<'_>) -> Result<Vec<u8>, Error> {
        let ms = milliseconds(options.timeout)?;
        output(|out| unsafe {
            ffi::ext_guest_invoke(
                self.raw,
                request.as_ptr(),
                request.len(),
                ms,
                (&options as *const CallOptions<'_>).cast_mut().cast(),
                out,
            )
        })
    }
}
impl Drop for Guest {
    fn drop(&mut self) {
        unsafe { ffi::ext_guest_destroy(self.raw) }
    }
}
/// Run a pure bounded JSON engine service; see pkg/plugin-engine/services.md.
pub fn service(operation: &str, input: &[u8]) -> Result<Vec<u8>, Error> {
    let name = cstring(operation)?;
    output(|out| unsafe { ffi::ext_engine_call(name.as_ptr(), input.as_ptr(), input.len(), out) })
}
pub fn sha256(input: &[u8]) -> [u8; 32] {
    let mut out = [0; 32];
    unsafe {
        ffi::ext_engine_sha256(input.as_ptr(), input.len(), out.as_mut_ptr());
    }
    out
}
#[cfg(not(feature = "guest-only"))]
pub fn verify_artifacts(manifest: &[u8], root: &str) -> Result<(), Error> {
    let root = cstring(root)?;
    check(unsafe { ffi::ext_package_verify(manifest.as_ptr(), manifest.len(), root.as_ptr()) })
}
#[cfg(not(feature = "guest-only"))]
pub fn directory_digest(root: &str) -> Result<[u8; 32], Error> {
    let root = cstring(root)?;
    let mut out = [0; 32];
    check(unsafe { ffi::ext_directory_digest(root.as_ptr(), out.as_mut_ptr()) })?;
    Ok(out)
}
#[cfg(test)]
mod tests {
    use super::*;
    const DESCRIPTOR: &[u8] = include_bytes!("../../../../pkg/plugin/testdata/v1/descriptor.json");
    #[test]
    fn services() {
        assert!(service("descriptor.validate", DESCRIPTOR).is_ok());
        assert_eq!(
            sha256(b"abc"),
            [
                0xba, 0x78, 0x16, 0xbf, 0x8f, 0x01, 0xcf, 0xea, 0x41, 0x41, 0x40, 0xde, 0x5d, 0xae,
                0x22, 0x23, 0xb0, 0x03, 0x61, 0xa3, 0x96, 0x17, 0x7a, 0x9c, 0xb4, 0x10, 0xff, 0x61,
                0xf2, 0x00, 0x15, 0xad
            ]
        );
        assert_eq!(service("unknown", b"{}"), Err(Error(4)));
    }
    #[test]
    fn cancellation() {
        let c = Cancellation::new().unwrap();
        assert!(!c.is_canceled());
        c.cancel();
        assert!(c.is_canceled());
    }
    #[test]
    fn guest() {
        let g = Guest::new(
            DESCRIPTOR,
            Duration::from_secs(1),
            Box::new(|_| Ok(GuestReply::Payload(b"7".to_vec()))),
        )
        .unwrap();
        assert!(service(
            "descriptor.match",
            format!(
                "{{\"selected\":{},\"actual\":{}}}",
                String::from_utf8_lossy(DESCRIPTOR),
                String::from_utf8_lossy(&g.descriptor().unwrap())
            )
            .as_bytes()
        )
        .is_ok());
    }
}
