//! The archive fixtures every store runs, in every language:
//! lib/plugin/testdata/v1/archive-cases.json with the archives beside it. They are
//! outside this crate, so the test skips when the crate is used without the repository.
#![cfg(not(feature = "guest-only"))]
use ext_plugin_engine::{
    store::{Limits, Store},
    Error,
};
use serde_json::Value;
use std::{
    fs,
    path::{Path, PathBuf},
    sync::atomic::{AtomicUsize, Ordering},
};

fn fixtures() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../lib/plugin/testdata/v1")
}

/// A scratch directory removed when it goes out of scope.
struct Scratch(PathBuf);
impl Scratch {
    fn new() -> Self {
        static NEXT: AtomicUsize = AtomicUsize::new(0);
        let path = std::env::temp_dir().join(format!(
            "ext-archive-test-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::SeqCst)
        ));
        fs::create_dir_all(&path).unwrap();
        Self(path)
    }
}
impl Drop for Scratch {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn status(result: Result<(), Error>) -> &'static str {
    match result {
        Ok(()) => "ok",
        Err(Error::EXISTS) => "exists",
        Err(Error::NOT_FOUND) => "not_found",
        Err(Error::MISMATCH) => "mismatch",
        Err(Error::UNSUPPORTED) => "unsupported",
        Err(Error::INVALID) => "invalid",
        Err(_) => "failed",
    }
}

fn text<'a>(value: &'a Value, key: &str) -> Option<&'a str> {
    value.get(key).and_then(Value::as_str)
}

fn revision(json: &[u8], pointer: &str) -> String {
    let value: Value = serde_json::from_slice(json).expect("engine JSON");
    value
        .pointer(pointer)
        .and_then(Value::as_str)
        .expect("a revision")
        .to_owned()
}

#[test]
fn archive_cases() {
    let dir = fixtures();
    let Ok(data) = fs::read(dir.join("archive-cases.json")) else {
        eprintln!("skipped: needs the repository's lib/plugin/testdata");
        return;
    };
    let fixture: Value = serde_json::from_slice(&data).unwrap();
    let cases = fixture["cases"].as_array().unwrap();
    assert!(!cases.is_empty(), "no cases");
    for case in cases {
        let name = text(case, "name").unwrap();
        let limit = |key: &str| {
            case.pointer(&format!("/limits/{key}"))
                .and_then(Value::as_u64)
        };
        let limits = Limits {
            max_archive_bytes: limit("maxArchiveBytes").unwrap_or(0),
            max_tree_files: limit("maxTreeFiles").unwrap_or(0) as u32,
            max_tree_bytes: limit("maxTreeBytes").unwrap_or(0),
            max_artifact_bytes: limit("maxArtifactBytes").unwrap_or(0),
        };
        let scratch = Scratch::new();
        let root = scratch.0.join("plugins");
        let store = Store::open(root.to_str().unwrap(), limits).unwrap();
        for (i, step) in case["steps"].as_array().unwrap().iter().enumerate() {
            let sha256 = text(step, "sha256");
            let mut got_revision = String::new();
            let got = if let Some(archive) = text(step, "install") {
                let path = dir.join(archive);
                let result = store.install_archive(
                    path.to_str().unwrap(),
                    sha256.unwrap_or(""),
                    text(step, "id"),
                    false,
                );
                status(result.map(|manifest| {
                    got_revision = revision(&manifest, "/identity/revision");
                }))
            } else if let Some(id) = text(step, "receipt") {
                status(store.receipt(id).map(|receipt| {
                    let value: Value = serde_json::from_slice(&receipt).unwrap();
                    assert_eq!(text(&value, "id"), Some(id), "{name} step {i}");
                    assert_eq!(
                        text(&value, "artifact_sha256"),
                        sha256,
                        "{name} step {i}: receipt names another archive"
                    );
                    got_revision = revision(&receipt, "/revision");
                }))
            } else if let Some(id) = text(step, "remove") {
                status(store.remove(id))
            } else if let Some(id) = text(step, "damage") {
                let path = root
                    .join(id.replace('/', "%2f"))
                    .join(text(step, "path").unwrap());
                fs::write(path, "damaged").unwrap();
                "ok"
            } else {
                panic!("{name} step {i}: nothing to do");
            };
            let want = text(step, "status").unwrap_or("ok");
            let want_revision = text(step, "revision").unwrap_or("");
            assert_eq!(
                (got, got_revision.as_str()),
                (want, want_revision),
                "{name} step {i}"
            );
        }
        // Whatever happened, nothing is left under the root but packages and receipts.
        if let Ok(entries) = fs::read_dir(&root) {
            for entry in entries {
                let entry = entry.unwrap().file_name();
                let entry = entry.to_string_lossy();
                assert!(
                    !entry.starts_with('.') || entry == ".receipts",
                    "{name}: left {entry} in the store"
                );
            }
        }
        assert!(
            fs::symlink_metadata("/tmp/ext-archive-escape").is_err(),
            "{name}: an archive wrote outside the store"
        );
    }
}
