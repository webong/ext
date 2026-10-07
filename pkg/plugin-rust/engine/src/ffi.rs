//! Raw embedding ABI v2. Pointers, callbacks, threads and ownership follow the
//! installed ext_host.h, ext_guest.h, ext_instance.h and ext_stream.h headers.
#![allow(non_camel_case_types)]
use std::ffi::{c_char, c_void};
pub type Status = i32;
#[repr(C)]
pub struct Buffer {
    pub data: *mut u8,
    pub len: usize,
}
#[repr(C)]
pub struct Call {
    pub struct_size: u32,
    pub timeout_ms: u32,
    pub cancel: *const c_void,
    pub value: *mut c_void,
}
pub type Policy = unsafe extern "C" fn(*mut c_void, *const u8, usize) -> i32;
pub type Emit = unsafe extern "C" fn(*mut c_void, *const u8, usize) -> Status;
#[repr(C)]
pub struct HostOptions {
    pub abi_version: u32,
    pub struct_size: u32,
    pub descriptor: *const u8,
    pub descriptor_len: usize,
    pub verify: Policy,
    pub authorize: Policy,
    pub user: *mut c_void,
}
#[repr(C)]
pub struct Backend {
    pub struct_size: u32,
    pub kind: u32,
    pub config: *const c_void,
    pub config_size: usize,
}
#[repr(C)]
pub struct Process {
    pub struct_size: u32,
    pub executable: *const c_char,
    pub arguments: *const *const c_char,
    pub argument_count: usize,
    pub environment: *const *const c_char,
    pub environment_count: usize,
}
pub type ContextPolicy = unsafe extern "C" fn(*mut c_void, *const Call, *const u8, usize) -> i32;
pub type Observer = unsafe extern "C" fn(*mut c_void, *const Call, *const u8, usize);
#[repr(C)]
pub struct Hooks {
    pub struct_size: u32,
    pub user: *mut c_void,
    pub verify: Option<ContextPolicy>,
    pub authorize: Option<ContextPolicy>,
    pub observe: Option<Observer>,
}
#[repr(C)]
pub struct Extension {
    pub struct_size: u32,
    pub flags: u32,
    pub user: *mut c_void,
    pub connect: unsafe extern "C" fn(*mut c_void, *const Call, Emit, *mut c_void) -> Status,
    pub invoke: unsafe extern "C" fn(
        *mut c_void,
        *const u8,
        usize,
        *const Call,
        Emit,
        *mut c_void,
    ) -> Status,
    pub close: unsafe extern "C" fn(*mut c_void),
    pub release: unsafe extern "C" fn(*mut c_void),
}
pub type GuestEmit = unsafe extern "C" fn(*mut c_void, u32, *const u8, usize) -> Status;
#[repr(C)]
pub struct GuestOptions {
    pub abi_version: u32,
    pub struct_size: u32,
    pub descriptor: *const u8,
    pub descriptor_len: usize,
    pub max_call_ms: u32,
    pub user: *mut c_void,
    pub handle: unsafe extern "C" fn(
        *mut c_void,
        *mut c_void,
        *const u8,
        usize,
        u32,
        GuestEmit,
        *mut c_void,
    ) -> Status,
}
#[repr(C)]
pub struct InstanceOptions {
    pub struct_size: u32,
    pub capacity: u32,
    pub user: *mut c_void,
    pub validate: unsafe extern "C" fn(*mut c_void, *const u8, usize) -> Status,
    pub create: unsafe extern "C" fn(
        *mut c_void,
        *const Call,
        *const c_void,
        *const u8,
        usize,
        *const u8,
        usize,
        *mut *mut c_void,
    ) -> Status,
    pub dispose: unsafe extern "C" fn(*mut c_void, *mut c_void) -> Status,
    pub observe: Option<
        unsafe extern "C" fn(*mut c_void, *const u8, usize, *const u8, usize, *const c_char),
    >,
}
#[repr(C)]
pub struct StreamOptions {
    pub struct_size: u32,
    pub capacity: u32,
    pub max_age_ms: u32,
    pub user: *mut c_void,
    pub open: unsafe extern "C" fn(
        *mut c_void,
        *const Call,
        *const c_void,
        *const u8,
        usize,
        *mut *mut c_void,
    ) -> Status,
    pub read: unsafe extern "C" fn(
        *mut c_void,
        *mut c_void,
        *const Call,
        *const c_void,
        u32,
        Emit,
        *mut c_void,
    ) -> Status,
    pub close: unsafe extern "C" fn(*mut c_void, *mut c_void) -> Status,
    pub release: unsafe extern "C" fn(*mut c_void, *mut c_void),
}
extern "C" {
    pub fn ext_host_abi_version() -> u32;
    pub fn ext_host_create(
        o: *const HostOptions,
        b: *const Backend,
        out: *mut *mut c_void,
    ) -> Status;
    pub fn ext_host_set_hooks(h: *mut c_void, o: *const Hooks) -> Status;
    pub fn ext_host_start_with_options(h: *mut c_void, o: *const Call) -> Status;
    pub fn ext_host_invoke_with_options(
        h: *mut c_void,
        p: *const u8,
        n: usize,
        o: *const Call,
        out: *mut Buffer,
    ) -> Status;
    pub fn ext_host_call(
        h: *mut c_void,
        p: *const u8,
        n: usize,
        o: *const Call,
        out: *mut Buffer,
    ) -> Status;
    pub fn ext_host_drain_with_options(h: *mut c_void, o: *const Call) -> Status;
    pub fn ext_host_get_state(h: *mut c_void) -> i32;
    pub fn ext_host_close(h: *mut c_void);
    pub fn ext_host_destroy(h: *mut c_void);
    pub fn ext_host_status_string(s: Status) -> *const c_char;
    pub fn ext_host_validate_json(p: *const u8, n: usize) -> Status;
    pub fn ext_buffer_free(b: *mut Buffer);
    pub fn ext_cancel_create(out: *mut *mut c_void) -> Status;
    pub fn ext_cancel_signal(c: *mut c_void);
    pub fn ext_cancel_is_signaled(c: *const c_void) -> i32;
    pub fn ext_cancel_destroy(c: *mut c_void);
    pub fn ext_engine_call(op: *const c_char, p: *const u8, n: usize, out: *mut Buffer) -> Status;
    pub fn ext_engine_sha256(p: *const u8, n: usize, out: *mut u8) -> Status;
    pub fn ext_package_verify(p: *const u8, n: usize, root: *const c_char) -> Status;
    pub fn ext_directory_digest(root: *const c_char, out: *mut u8) -> Status;
    pub fn ext_guest_create(o: *const GuestOptions, out: *mut *mut c_void) -> Status;
    pub fn ext_guest_descriptor(g: *mut c_void, out: *mut Buffer) -> Status;
    pub fn ext_guest_invoke(
        g: *mut c_void,
        p: *const u8,
        n: usize,
        ms: u32,
        user: *mut c_void,
        out: *mut Buffer,
    ) -> Status;
    pub fn ext_guest_destroy(g: *mut c_void);
    pub fn ext_instances_create(o: *const InstanceOptions, out: *mut *mut c_void) -> Status;
    pub fn ext_instances_configure(
        m: *mut c_void,
        key: *const u8,
        kn: usize,
        revision: *const u8,
        rn: usize,
        config: *const u8,
        cn: usize,
        o: *const Call,
    ) -> Status;
    pub fn ext_instances_acquire(
        m: *mut c_void,
        key: *const u8,
        n: usize,
        out: *mut *mut c_void,
    ) -> Status;
    pub fn ext_lease_value(l: *const c_void) -> *mut c_void;
    pub fn ext_lease_revision(l: *const c_void, n: *mut usize) -> *const u8;
    pub fn ext_lease_release(l: *mut c_void) -> Status;
    pub fn ext_instances_remove(m: *mut c_void, key: *const u8, n: usize) -> Status;
    pub fn ext_instances_close(m: *mut c_void, o: *const Call) -> Status;
    pub fn ext_instances_destroy(m: *mut c_void) -> Status;
    pub fn ext_streams_create(o: *const StreamOptions, out: *mut *mut c_void) -> Status;
    pub fn ext_streams_open(
        s: *mut c_void,
        scope: *const u8,
        sn: usize,
        p: *const u8,
        n: usize,
        o: *const Call,
        out: *mut Buffer,
    ) -> Status;
    pub fn ext_streams_read(
        s: *mut c_void,
        scope: *const u8,
        sn: usize,
        id: *const c_char,
        sequence: u64,
        limit: u32,
        o: *const Call,
        out: *mut Buffer,
    ) -> Status;
    pub fn ext_streams_remove(
        s: *mut c_void,
        scope: *const u8,
        sn: usize,
        id: *const c_char,
    ) -> Status;
    pub fn ext_streams_close(s: *mut c_void) -> Status;
    pub fn ext_streams_destroy(s: *mut c_void) -> Status;
}
