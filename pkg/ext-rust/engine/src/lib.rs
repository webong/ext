//! Rust ownership and callback bindings for the CTX C engine. Wire values remain
//! JSON bytes; consumers choose their own serializer and domain types. Requires
//! ABI v2, currently Linux/macOS. No Go runtime or Rust protocol engine is linked.
pub mod endpoint;
pub mod ffi;
#[cfg(not(feature = "guest-only"))]
pub mod resources;
#[cfg(not(feature = "guest-only"))]
pub mod store;
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
impl Error {
    pub const INVALID: Error = Error(1);
    pub const DENIED: Error = Error(2);
    /// A digest, identity or contract differs from what was expected.
    pub const MISMATCH: Error = Error(3);
    /// What was asked for is not there: a package that is not installed.
    pub const NOT_FOUND: Error = Error(10);
    /// The peer broke the framing rules (a frame that is not valid, or that the
    /// call did not allow); the session is closed.
    pub const PROTOCOL: Error = Error(16);
    /// A time has run out: an endpoint whose expiry is already past.
    pub const EXPIRED: Error = Error(17);
    /// What was to be created is already there: a package already installed.
    pub const EXISTS: Error = Error(18);
}
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
/// Receives the JSON of one progress event.
#[cfg(not(feature = "guest-only"))]
pub type EventHook<'a> = dyn Fn(&[u8]) + Sync + 'a;
/// Progress events of one call. A zero rate uses the default (50 events and 65536
/// bytes of text per second, with a burst of twice that); the rate travels to the
/// plugin in the request, so both ends apply the same one.
#[cfg(not(feature = "guest-only"))]
#[derive(Default)]
pub struct Progress<'a> {
    pub events_per_second: u32,
    pub bytes_per_second: u32,
    /// Receives the JSON of one event. It runs on the calling thread, or on an
    /// engine thread when the plugin declares reverse operations, and never after
    /// the call returns. It must be quick and must not reenter the host.
    pub on_event: Option<&'a EventHook<'a>>,
}
#[cfg(not(feature = "guest-only"))]
unsafe extern "C" fn progress_event(user: *mut c_void, p: *const u8, n: usize) {
    let _ = catch_unwind(AssertUnwindSafe(|| {
        if let Some(f) = (*user.cast::<Progress<'_>>()).on_event {
            f(slice::from_raw_parts(p, n));
        }
    }));
}
/// One reverse request, borrowed for the hook that serves it. `request` is the
/// JSON of the request (apiVersion, id, plugin, contract, operation, surface,
/// deadline, payload and, for a call-scoped request, parent).
#[cfg(not(feature = "guest-only"))]
pub struct ReverseCall<'a> {
    pub request: &'a [u8],
    /// The time left to this reverse call.
    pub remaining: Duration,
    cancel: *const c_void,
    _life: std::marker::PhantomData<&'a ()>,
}
#[cfg(not(feature = "guest-only"))]
impl ReverseCall<'_> {
    /// Set when the call it belongs to ends, when the caller cancels it and when
    /// the host closes. A hook polls it and stops early.
    pub fn is_canceled(&self) -> bool {
        unsafe { ffi::ext_cancel_is_signaled(self.cancel) != 0 }
    }
}
/// A hook's answer to a reverse request: a payload (empty means null) or a public
/// error, the JSON of a RemoteError. Any other failure is private and reaches
/// the plugin as `operation_failed`.
#[cfg(not(feature = "guest-only"))]
pub enum ReverseReply {
    Payload(Vec<u8>),
    PublicError(Vec<u8>),
}
#[cfg(not(feature = "guest-only"))]
pub type ReverseAuthorize = Box<dyn for<'a> Fn(ReverseCall<'a>) -> Result<(), Error> + Send + Sync>;
#[cfg(not(feature = "guest-only"))]
pub type ReverseHandle =
    Box<dyn for<'a> Fn(ReverseCall<'a>) -> Result<ReverseReply, Error> + Send + Sync>;
/// What a host offers its plugin: the host operations the plugin's descriptor
/// lists under `reverse`. The engine decides admission (declared, within bounds,
/// before the plugin's deadline, fewer than `max_in_flight`) and refuses with a
/// stable code; `authorize` and `handle` are the host's policy. Zero bounds use
/// the defaults (30 s, 1 MiB, 4).
#[cfg(not(feature = "guest-only"))]
pub struct Services {
    pub authorize: ReverseAuthorize,
    pub handle: ReverseHandle,
    pub timeout: Duration,
    pub max_payload_bytes: u32,
    pub max_in_flight: u32,
}
#[cfg(not(feature = "guest-only"))]
struct ServiceHooks {
    authorize: ReverseAuthorize,
    handle: ReverseHandle,
}
#[cfg(not(feature = "guest-only"))]
unsafe fn reverse_call<'a>(call: *const ffi::Call, p: *const u8, n: usize) -> ReverseCall<'a> {
    ReverseCall {
        request: slice::from_raw_parts(p, n),
        remaining: Duration::from_millis((*call).timeout_ms as u64),
        cancel: (*call).cancel,
        _life: std::marker::PhantomData,
    }
}
#[cfg(not(feature = "guest-only"))]
unsafe extern "C" fn reverse_authorize(
    user: *mut c_void,
    call: *const ffi::Call,
    p: *const u8,
    n: usize,
) -> i32 {
    // A policy that fails denies.
    catch_unwind(AssertUnwindSafe(|| {
        let hooks = &*user.cast::<ServiceHooks>();
        if (hooks.authorize)(reverse_call(call, p, n)).is_ok() {
            0
        } else {
            1
        }
    }))
    .unwrap_or(1)
}
#[cfg(not(feature = "guest-only"))]
unsafe extern "C" fn reverse_handle(
    user: *mut c_void,
    call: *const ffi::Call,
    p: *const u8,
    n: usize,
    emit: ffi::ReverseEmit,
    sink: *mut c_void,
) -> i32 {
    // A hook that fails or panics is a failure the plugin sees as operation_failed.
    catch_unwind(AssertUnwindSafe(|| {
        let hooks = &*user.cast::<ServiceHooks>();
        match (hooks.handle)(reverse_call(call, p, n)) {
            Ok(ReverseReply::Payload(v)) => emit(sink, 0, v.as_ptr(), v.len()),
            Ok(ReverseReply::PublicError(v)) => emit(sink, 1, v.as_ptr(), v.len()),
            Err(e) => e.0,
        }
    }))
    .unwrap_or(7)
}
/// A shared host session. Policy callbacks must not reenter this same host.
/// Rust borrows ensure Drop cannot race a call. Raw FFI users own that guarantee.
#[cfg(not(feature = "guest-only"))]
pub struct Host {
    raw: *mut c_void,
    _policies: Box<Policies>,
    _services: Option<Box<ServiceHooks>>,
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
            _services: None,
        })
    }
    /// Offer reverse-call services to the plugin. Install before start. The hooks
    /// run on engine threads, several at a time, and must not reenter this host.
    pub fn with_services(mut self, services: Services) -> Result<Self, Error> {
        let timeout_ms = if services.timeout.is_zero() {
            0
        } else {
            milliseconds(services.timeout)?
        };
        let mut hooks = Box::new(ServiceHooks {
            authorize: services.authorize,
            handle: services.handle,
        });
        let raw = ffi::Services {
            struct_size: size_of::<ffi::Services>() as u32,
            user: (&mut *hooks as *mut ServiceHooks).cast(),
            authorize: reverse_authorize,
            handle: reverse_handle,
            timeout_ms,
            max_payload_bytes: services.max_payload_bytes,
            max_in_flight: services.max_in_flight,
            session_value: ptr::null_mut(),
        };
        check(unsafe { ffi::ext_host_set_services(self.raw, &raw) })?;
        self._services = Some(hooks);
        Ok(self)
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
    /// [`Host::call`] for an operation that declares progress: `progress.on_event`
    /// receives the JSON of each event while the call runs. The engine validates
    /// the events, applies the rate limit and delivers what passes before this
    /// returns, ending with a marker for any it dropped.
    pub fn call_progress(
        &self,
        input: &[u8],
        options: CallOptions<'_>,
        progress: &Progress<'_>,
    ) -> Result<Vec<u8>, Error> {
        let o = options.raw()?;
        let raw = ffi::ProgressOptions {
            struct_size: size_of::<ffi::ProgressOptions>() as u32,
            deliver: progress.on_event.map(|_| progress_event as _),
            user: (progress as *const Progress<'_>).cast_mut().cast(),
            events_per_second: progress.events_per_second,
            bytes_per_second: progress.bytes_per_second,
        };
        output(|out| unsafe {
            ffi::ext_host_call_progress(self.raw, input.as_ptr(), input.len(), &o, &raw, out)
        })
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
/// Run a pure bounded JSON engine service; see lib/plugin/services.md.
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
    const DESCRIPTOR: &[u8] = include_bytes!("../../../../lib/plugin/testdata/v1/descriptor.json");
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
    // A request that does not match the descriptor is answered by the engine, for every binding, as a
    // public invalid_request response that echoes its id; one with no usable id, or that is not a
    // request, is a failed call (EXT_INVALID, status 1). The handler is never called for either.
    #[test]
    fn malformed_requests() {
        use std::sync::atomic::{AtomicUsize, Ordering};
        use std::sync::Arc;
        let called = Arc::new(AtomicUsize::new(0));
        let counter = called.clone();
        let g = Guest::new(
            DESCRIPTOR,
            Duration::from_secs(1),
            Box::new(move |_| {
                counter.fetch_add(1, Ordering::SeqCst);
                Ok(GuestReply::Payload(b"7".to_vec()))
            }),
        )
        .unwrap();
        let request = |id: &str, operation: &str, deadline: &str| {
            format!(
                "{{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"{id}\",\"plugin\":{{\"id\":\"ctx/conformance\",\"revision\":\"fixture-1\"}},\"contract\":{{\"name\":\"ext.conformance\",\"version\":\"v1\"}},\"operation\":\"{operation}\"{deadline}}}"
            )
        };
        let deadline = ",\"deadline\":\"2099-01-01T00:00:00Z\"";
        let answered = g
            .invoke(
                request("1", "echo", deadline).as_bytes(),
                CallOptions::default(),
            )
            .unwrap();
        assert!(String::from_utf8_lossy(&answered).contains("\"payload\":7"));
        for bad in [
            request("1", "nope", deadline),
            request("1", "echo", ""),
            request("1", "echo", deadline).replace("ext.plugin/v1", "ext.plugin/v2"),
        ] {
            called.store(0, Ordering::SeqCst);
            let reply = g.invoke(bad.as_bytes(), CallOptions::default()).unwrap();
            let text = String::from_utf8_lossy(&reply);
            assert!(
                text.contains("\"id\":\"1\"")
                    && text.contains("\"code\":\"invalid_request\"")
                    && !text.contains("\"payload\"")
                    && called.load(Ordering::SeqCst) == 0,
                "{bad}: {text}"
            );
        }
        for bad in [
            String::new(),
            "{".to_string(),
            "[]".to_string(),
            "{}".to_string(),
            request("", "echo", deadline),
            request("-bad", "echo", deadline),
        ] {
            called.store(0, Ordering::SeqCst);
            assert_eq!(
                g.invoke(bad.as_bytes(), CallOptions::default()),
                Err(Error(1)),
                "{bad}"
            );
            assert_eq!(called.load(Ordering::SeqCst), 0);
        }
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
