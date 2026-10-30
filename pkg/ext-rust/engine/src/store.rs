//! The package store of the C engine: install, upgrade, replace, remove and load
//! reviewed `ext.package/v1` packages under a root the application supplies (see
//! `ext_store_*` in lib/plugin/services.md). A package is its `plugin.json`
//! manifest and the artifacts it lists, in a directory named by the package id.
//! The store copies and re-verifies content; it never runs it or grants
//! authority. A missing root is an empty store, made by the first install.
//! Calls on one [`Store`] are serialized by the engine; several processes
//! mutating one root are the application's to serialize.
//!
//! Failures are statuses: [`Error::INVALID`] for a package, id or directory that
//! is not valid or an artifact over a limit, [`Error::MISMATCH`] for an artifact
//! whose digest differs or an upgrade that changes the id or contracts,
//! [`Error::EXISTS`] for an install over an installed id and
//! [`Error::NOT_FOUND`] for an id that is not installed.
//!
//! The whole-root reports ([`Store::list`], [`Store::verify`],
//! [`Store::list_unverified`]) and the read-only [`Layered`] view return the
//! engine's JSON as it is, as `load` returns `plugin.json`, so a caller parses
//! it with its own JSON library; the shapes are in `ext_store_list` of
//! lib/plugin/include/ext_host.h.
use crate::{check, cstring, ffi, output, Error};
use std::{
    ffi::{c_char, c_void, CString},
    ptr,
};

/// Bounds on one artifact file (a file inside a tree counts too), on the files
/// in one tree and on the bytes of one tree. Zero takes the default: 4 GiB,
/// 250000 files and 16 GiB.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Limits {
    pub max_artifact_bytes: u64,
    pub max_tree_files: u32,
    pub max_tree_bytes: u64,
}

/// An open package store. Dropping it releases the engine's handle.
pub struct Store(*mut c_void);
// The engine serializes calls on one store.
unsafe impl Send for Store {}
unsafe impl Sync for Store {}

type Put = unsafe extern "C" fn(*mut c_void, *const std::ffi::c_char, u32, *mut ffi::Buffer) -> i32;

impl Store {
    /// Returns a store rooted at `root`, which need not exist yet.
    pub fn open(root: &str, limits: Limits) -> Result<Self, Error> {
        let root = cstring(root)?;
        let raw = ffi::StoreLimits {
            max_artifact_bytes: limits.max_artifact_bytes,
            max_tree_files: limits.max_tree_files,
            max_tree_bytes: limits.max_tree_bytes,
        };
        let mut handle = ptr::null_mut();
        check(unsafe { ffi::ext_store_open(root.as_ptr(), &raw, &mut handle) })?;
        Ok(Self(handle))
    }
    fn put(&self, call: Put, source: &str, preserve_executable: bool) -> Result<Vec<u8>, Error> {
        let source = cstring(source)?;
        let flags = if preserve_executable {
            ffi::STORE_PRESERVE_EXECUTABLE
        } else {
            0
        };
        output(|manifest| unsafe { call(self.0, source.as_ptr(), flags, manifest) })
    }
    /// Copies the package in the directory `source` into the store, never over an
    /// installed id, and returns the installed `plugin.json`, byte for byte as the
    /// source had it. With `preserve_executable` a file artifact that is
    /// executable in the source stays so; entrypoints and executable files in
    /// trees always do.
    pub fn install(&self, source: &str, preserve_executable: bool) -> Result<Vec<u8>, Error> {
        self.put(ffi::ext_store_install, source, preserve_executable)
    }
    /// Replaces an installed package with a new revision of the same id and
    /// contracts. The previous installation survives any failure.
    pub fn upgrade(&self, source: &str, preserve_executable: bool) -> Result<Vec<u8>, Error> {
        self.put(ffi::ext_store_upgrade, source, preserve_executable)
    }
    /// Installs or replaces the package of the source's id, whatever its contracts.
    pub fn replace(&self, source: &str, preserve_executable: bool) -> Result<Vec<u8>, Error> {
        self.put(ffi::ext_store_replace, source, preserve_executable)
    }
    /// Deletes one installed package.
    pub fn remove(&self, id: &str) -> Result<(), Error> {
        let id = cstring(id)?;
        check(unsafe { ffi::ext_store_remove(self.0, id.as_ptr()) })
    }
    /// Reads and verifies one installed package and returns its `plugin.json`.
    pub fn load(&self, id: &str) -> Result<Vec<u8>, Error> {
        let id = cstring(id)?;
        output(|manifest| unsafe { ffi::ext_store_load(self.0, id.as_ptr(), manifest) })
    }
    /// Lists the installed package ids in byte order without reading or verifying
    /// any package. Staging directories are skipped; any other entry that is not a
    /// package directory is [`Error::INVALID`].
    pub fn ids(&self) -> Result<Vec<String>, Error> {
        let json = output(|ids| unsafe { ffi::ext_store_ids(self.0, ids) })?;
        // A JSON array of strings; ids hold only [a-z0-9._/-], so nothing is escaped.
        let text = std::str::from_utf8(&json).map_err(|_| Error::INVALID)?;
        let inner = text
            .strip_prefix('[')
            .and_then(|t| t.strip_suffix(']'))
            .ok_or(Error::INVALID)?;
        if inner.is_empty() {
            return Ok(Vec::new());
        }
        inner
            .split(',')
            .map(|item| {
                item.strip_prefix('"')
                    .and_then(|t| t.strip_suffix('"'))
                    .map(str::to_owned)
                    .ok_or(Error::INVALID)
            })
            .collect()
    }
    /// Reads and verifies every package and returns a JSON array of `{id,
    /// directory, layer, manifest}` sorted by id, the manifest being `plugin.json`
    /// as an embedded value. It fails with the status of the first entry that is
    /// not a package directory and of the first package that does not verify.
    pub fn list(&self) -> Result<Vec<u8>, Error> {
        output(|packages| unsafe { ffi::ext_store_list(self.0, packages) })
    }
    /// Checks every entry without stopping at a bad one and returns a JSON array
    /// of `{id, directory, layer, status}`, where `status` is the engine status
    /// verifying it gave (0 for verified). It runs nothing and changes nothing.
    pub fn verify(&self) -> Result<Vec<u8>, Error> {
        output(|checks| unsafe { ffi::ext_store_verify(self.0, checks) })
    }
    /// Reads only the manifests and checks them against their directory names;
    /// no artifact is read or hashed, so THE RESULT IS UNVERIFIED and must not be
    /// launched or trusted as it stands. Returns `{"packages": [...], "problems":
    /// [...]}` as JSON; a package that cannot be read does not hide the others.
    pub fn list_unverified(&self) -> Result<Vec<u8>, Error> {
        output(|listing| unsafe { ffi::ext_store_list_unverified(self.0, listing) })
    }
}
impl Drop for Store {
    fn drop(&mut self) {
        unsafe { ffi::ext_store_close(self.0) }
    }
}

/// A read-only view over several roots in priority order, highest first, such as
/// a user root before a system root. An id is served from the first root that
/// holds it; a copy in a lower root is shadowed and never read. If the winning
/// copy does not verify, [`Layered::load`] and the reports fail instead of
/// falling back to a lower one. The view never writes, so a system root is never
/// created or modified through it. Roots are compared as cleaned paths: a repeated
/// root, an empty one, or no root at all is [`Error::INVALID`].
pub struct Layered(*mut c_void);
// Each root is a store whose calls the engine serializes.
unsafe impl Send for Layered {}
unsafe impl Sync for Layered {}

impl Layered {
    /// Returns a view over `roots`, which need not exist.
    pub fn open(roots: &[&str], limits: Limits) -> Result<Self, Error> {
        let owned = roots
            .iter()
            .map(|root| cstring(root))
            .collect::<Result<Vec<CString>, Error>>()?;
        let pointers: Vec<*const c_char> = owned.iter().map(|root| root.as_ptr()).collect();
        let raw = ffi::StoreLimits {
            max_artifact_bytes: limits.max_artifact_bytes,
            max_tree_files: limits.max_tree_files,
            max_tree_bytes: limits.max_tree_bytes,
        };
        let mut handle = ptr::null_mut();
        check(unsafe {
            ffi::ext_layered_open(pointers.as_ptr(), pointers.len(), &raw, &mut handle)
        })?;
        Ok(Self(handle))
    }
    /// Reads and verifies the package from the first root that holds `id` and
    /// returns its `plugin.json` and the position of that root.
    /// [`Error::NOT_FOUND`] means no root holds it.
    pub fn load(&self, id: &str) -> Result<(Vec<u8>, usize), Error> {
        let id = cstring(id)?;
        let mut layer = 0usize;
        let manifest =
            output(|m| unsafe { ffi::ext_layered_load(self.0, id.as_ptr(), m, &mut layer) })?;
        Ok((manifest, layer))
    }
    /// As [`Store::list`] over every root, one line per id from the root that serves it.
    pub fn list(&self) -> Result<Vec<u8>, Error> {
        output(|packages| unsafe { ffi::ext_layered_list(self.0, packages) })
    }
    /// As [`Store::verify`] over every root.
    pub fn verify(&self) -> Result<Vec<u8>, Error> {
        output(|checks| unsafe { ffi::ext_layered_verify(self.0, checks) })
    }
    /// As [`Store::list_unverified`] over every root; the result is unverified.
    pub fn list_unverified(&self) -> Result<Vec<u8>, Error> {
        output(|listing| unsafe { ffi::ext_layered_list_unverified(self.0, listing) })
    }
}
impl Drop for Layered {
    fn drop(&mut self) {
        unsafe { ffi::ext_layered_close(self.0) }
    }
}
