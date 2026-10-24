//! Resource ownership bindings. Factories and disposers must not reenter their
//! manager. Callbacks are cooperative: check Context and unblock reads in close.
//! Dropping a manager joins cleanup; use explicit close to report cleanup errors.
use crate::{check, cstring, ffi, output, CallOptions, Error};
use std::{
    ffi::c_void,
    marker::PhantomData,
    mem::size_of,
    panic::{catch_unwind, AssertUnwindSafe},
    ptr, slice,
    time::Duration,
};

/// Borrowed for one callback. Neither signal may outlive that callback. A
/// background resource should use its close method to stop its own work.
pub struct Context<'a> {
    pub remaining: Duration,
    call: &'a ffi::Call,
    life: *const c_void,
}
impl Context<'_> {
    pub fn is_canceled(&self) -> bool {
        unsafe {
            ffi::ext_cancel_is_signaled(self.call.cancel) != 0
                || ffi::ext_cancel_is_signaled(self.life) != 0
        }
    }
}
unsafe fn context<'a>(call: *const ffi::Call, life: *const c_void) -> Context<'a> {
    Context {
        remaining: Duration::from_millis((*call).timeout_ms as u64),
        call: &*call,
        life,
    }
}
fn callback(f: impl FnOnce() -> Result<(), Error>) -> i32 {
    match catch_unwind(AssertUnwindSafe(f)) {
        Ok(Ok(())) => 0,
        Ok(Err(e)) => e.0,
        Err(_) => 7,
    }
}
pub type Validate = Box<dyn Fn(&[u8]) -> Result<(), Error> + Send + Sync>;
pub type Factory<T> =
    Box<dyn for<'a> Fn(Context<'a>, &[u8], &[u8]) -> Result<T, Error> + Send + Sync>;
pub type Dispose<T> = Box<dyn Fn(T) -> Result<(), Error> + Send + Sync>;
struct InstanceCallbacks<T> {
    validate: Validate,
    create: Factory<T>,
    dispose: Dispose<T>,
}
unsafe extern "C" fn validate<T>(user: *mut c_void, p: *const u8, n: usize) -> i32 {
    callback(|| ((*user.cast::<InstanceCallbacks<T>>()).validate)(slice::from_raw_parts(p, n)))
}
unsafe extern "C" fn create<T>(
    user: *mut c_void,
    call: *const ffi::Call,
    life: *const c_void,
    key: *const u8,
    kn: usize,
    config: *const u8,
    cn: usize,
    out: *mut *mut c_void,
) -> i32 {
    callback(|| {
        let value = ((*user.cast::<InstanceCallbacks<T>>()).create)(
            context(call, life),
            slice::from_raw_parts(key, kn),
            slice::from_raw_parts(config, cn),
        )?;
        *out = Box::into_raw(Box::new(value)).cast();
        Ok(())
    })
}
unsafe extern "C" fn dispose<T>(user: *mut c_void, value: *mut c_void) -> i32 {
    callback(|| {
        let value = *Box::from_raw(value.cast::<T>());
        ((*user.cast::<InstanceCallbacks<T>>()).dispose)(value)
    })
}
pub struct Instances<T: Send + Sync + 'static> {
    raw: *mut c_void,
    _callbacks: Box<InstanceCallbacks<T>>,
}
unsafe impl<T: Send + Sync> Send for Instances<T> {}
unsafe impl<T: Send + Sync> Sync for Instances<T> {}
impl<T: Send + Sync> Instances<T> {
    pub fn new(
        capacity: u32,
        validate_config: Validate,
        factory: Factory<T>,
        disposer: Dispose<T>,
    ) -> Result<Self, Error> {
        let mut callbacks = Box::new(InstanceCallbacks {
            validate: validate_config,
            create: factory,
            dispose: disposer,
        });
        let options = ffi::InstanceOptions {
            struct_size: size_of::<ffi::InstanceOptions>() as u32,
            capacity,
            user: (&mut *callbacks as *mut InstanceCallbacks<T>).cast(),
            validate: validate::<T>,
            create: create::<T>,
            dispose: dispose::<T>,
            observe: None,
        };
        let mut raw = ptr::null_mut();
        check(unsafe { ffi::ext_instances_create(&options, &mut raw) })?;
        Ok(Self {
            raw,
            _callbacks: callbacks,
        })
    }
    pub fn configure(
        &self,
        key: &[u8],
        revision: &[u8],
        config: &[u8],
        options: CallOptions<'_>,
    ) -> Result<(), Error> {
        check(unsafe {
            ffi::ext_instances_configure(
                self.raw,
                key.as_ptr(),
                key.len(),
                revision.as_ptr(),
                revision.len(),
                config.as_ptr(),
                config.len(),
                &options.raw()?,
            )
        })
    }
    pub fn acquire(&self, key: &[u8]) -> Result<Lease<'_, T>, Error> {
        let mut raw = ptr::null_mut();
        check(unsafe { ffi::ext_instances_acquire(self.raw, key.as_ptr(), key.len(), &mut raw) })?;
        Ok(Lease {
            raw,
            _manager: PhantomData,
        })
    }
    pub fn remove(&self, key: &[u8]) -> Result<(), Error> {
        check(unsafe { ffi::ext_instances_remove(self.raw, key.as_ptr(), key.len()) })
    }
    /// Permanently stops admission. Outstanding leases keep their values alive;
    /// release them and retry after a timeout. Cleanup errors are retained by C.
    pub fn close(&self, options: CallOptions<'_>) -> Result<(), Error> {
        check(unsafe { ffi::ext_instances_close(self.raw, &options.raw()?) })
    }
}
impl<T: Send + Sync> Drop for Instances<T> {
    fn drop(&mut self) {
        let options = CallOptions {
            timeout: Duration::from_millis(u32::MAX as u64),
            cancel: None,
        };
        loop {
            let _ = self.close(options);
            if unsafe { ffi::ext_instances_destroy(self.raw) } == 0 {
                break;
            }
        }
    }
}
/// A lease borrows its manager, so the manager cannot be destroyed before the
/// value is released. Values stay immutable through replacement and close.
pub struct Lease<'a, T: Send + Sync + 'static> {
    raw: *mut c_void,
    _manager: PhantomData<&'a Instances<T>>,
}
unsafe impl<T: Send + Sync> Send for Lease<'_, T> {}
unsafe impl<T: Send + Sync> Sync for Lease<'_, T> {}
impl<T: Send + Sync> Lease<'_, T> {
    pub fn value(&self) -> &T {
        unsafe { &*ffi::ext_lease_value(self.raw).cast::<T>() }
    }
    pub fn revision(&self) -> &[u8] {
        let mut n = 0;
        unsafe {
            let p = ffi::ext_lease_revision(self.raw, &mut n);
            slice::from_raw_parts(p, n)
        }
    }
    pub fn release(mut self) -> Result<(), Error> {
        let raw = std::mem::replace(&mut self.raw, ptr::null_mut());
        check(unsafe { ffi::ext_lease_release(raw) })
    }
}
impl<T: Send + Sync> Drop for Lease<'_, T> {
    fn drop(&mut self) {
        if !self.raw.is_null() {
            unsafe {
                ffi::ext_lease_release(self.raw);
            }
        }
    }
}

/// Reads are serialized per stream by C. Close may run concurrently with a read
/// and must unblock it. The value is dropped only after close and reads finish.
pub trait Stream: Send + Sync + 'static {
    /// Return one JSON object: {"items":[...],"done":bool}.
    fn read(&self, context: Context<'_>, limit: u32) -> Result<Vec<u8>, Error>;
    fn close(&self) -> Result<(), Error>;
}
pub type Open<T> = Box<dyn for<'a> Fn(Context<'a>, &[u8]) -> Result<T, Error> + Send + Sync>;
unsafe extern "C" fn open<T: Stream>(
    user: *mut c_void,
    call: *const ffi::Call,
    life: *const c_void,
    p: *const u8,
    n: usize,
    out: *mut *mut c_void,
) -> i32 {
    callback(|| {
        let value = (&*user.cast::<Open<T>>())(context(call, life), slice::from_raw_parts(p, n))?;
        *out = Box::into_raw(Box::new(value)).cast();
        Ok(())
    })
}
unsafe extern "C" fn read<T: Stream>(
    _: *mut c_void,
    value: *mut c_void,
    call: *const ffi::Call,
    life: *const c_void,
    limit: u32,
    emit: ffi::Emit,
    sink: *mut c_void,
) -> i32 {
    callback(|| {
        let result = (&*value.cast::<T>()).read(context(call, life), limit)?;
        check(emit(sink, result.as_ptr(), result.len()))
    })
}
unsafe extern "C" fn close<T: Stream>(_: *mut c_void, value: *mut c_void) -> i32 {
    callback(|| (&*value.cast::<T>()).close())
}
unsafe extern "C" fn release<T: Stream>(_: *mut c_void, value: *mut c_void) {
    let _ = catch_unwind(AssertUnwindSafe(|| drop(Box::from_raw(value.cast::<T>()))));
}
pub struct Streams<T: Stream> {
    raw: *mut c_void,
    _open: Box<Open<T>>,
}
unsafe impl<T: Stream> Send for Streams<T> {}
unsafe impl<T: Stream> Sync for Streams<T> {}
impl<T: Stream> Streams<T> {
    pub fn new(capacity: u32, max_age: Duration, factory: Open<T>) -> Result<Self, Error> {
        let mut factory = Box::new(factory);
        let options = ffi::StreamOptions {
            struct_size: size_of::<ffi::StreamOptions>() as u32,
            capacity,
            max_age_ms: crate::milliseconds(max_age)?,
            user: (&mut *factory as *mut Open<T>).cast(),
            open: open::<T>,
            read: read::<T>,
            close: close::<T>,
            release: release::<T>,
        };
        let mut raw = ptr::null_mut();
        check(unsafe { ffi::ext_streams_create(&options, &mut raw) })?;
        Ok(Self {
            raw,
            _open: factory,
        })
    }
    pub fn open(
        &self,
        scope: &[u8],
        parameters: &[u8],
        options: CallOptions<'_>,
    ) -> Result<String, Error> {
        let o = options.raw()?;
        let id = output(|out| unsafe {
            ffi::ext_streams_open(
                self.raw,
                scope.as_ptr(),
                scope.len(),
                parameters.as_ptr(),
                parameters.len(),
                &o,
                out,
            )
        })?;
        // C emits a quoted, fixed-width lowercase hex ID. Do not need a JSON dependency.
        if id.len() != 50 || id[0] != b'"' || id[49] != b'"' {
            return Err(Error(3));
        }
        String::from_utf8(id[1..49].to_vec()).map_err(|_| Error(3))
    }
    pub fn read(
        &self,
        scope: &[u8],
        id: &str,
        sequence: u64,
        limit: u32,
        options: CallOptions<'_>,
    ) -> Result<Vec<u8>, Error> {
        let id = cstring(id)?;
        let o = options.raw()?;
        output(|out| unsafe {
            ffi::ext_streams_read(
                self.raw,
                scope.as_ptr(),
                scope.len(),
                id.as_ptr(),
                sequence,
                limit,
                &o,
                out,
            )
        })
    }
    pub fn remove(&self, scope: &[u8], id: &str) -> Result<(), Error> {
        let id = cstring(id)?;
        check(unsafe {
            ffi::ext_streams_remove(self.raw, scope.as_ptr(), scope.len(), id.as_ptr())
        })
    }
    pub fn close(&self) -> Result<(), Error> {
        check(unsafe { ffi::ext_streams_close(self.raw) })
    }
}
impl<T: Stream> Drop for Streams<T> {
    fn drop(&mut self) {
        let _ = self.close();
        // Borrowed calls have joined before Drop. C expiry cleanup may still be releasing.
        while unsafe { ffi::ext_streams_destroy(self.raw) } == 9 {
            std::thread::yield_now();
        }
    }
}
