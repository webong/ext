//! The package store of the C engine through the Rust binding: the lifecycle, the
//! failures each call has, permissions, limits and the manifest copied as it was.
//! The Go package store is the baseline; pkg/ext-go/cgoengine holds the C store to
//! it, and this checks that the binding reaches the same behavior.
#![cfg(not(feature = "guest-only"))]
use ext_plugin_engine::{
    directory_digest, sha256,
    store::{Limits, Store},
    Error,
};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::atomic::{AtomicUsize, Ordering},
};

/// A scratch directory removed when it goes out of scope.
struct Scratch(PathBuf);
impl Scratch {
    fn new() -> Self {
        static NEXT: AtomicUsize = AtomicUsize::new(0);
        let path = std::env::temp_dir().join(format!(
            "ext-store-test-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::SeqCst)
        ));
        fs::create_dir_all(&path).unwrap();
        Self(path)
    }
    fn join(&self, name: &str) -> String {
        self.0.join(name).to_str().unwrap().to_owned()
    }
}
impl Drop for Scratch {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}
fn write(dir: &Path, rel: &str, body: &str, executable: bool) {
    let path = dir.join(rel);
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(&path, body).unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(
            &path,
            fs::Permissions::from_mode(if executable { 0o700 } else { 0o600 }),
        )
        .unwrap();
    }
    let _ = executable;
}

/// A source package at `dir`: `src/main.py` holding `body`, and with `tree` the
/// tree artifact `data`. `contract` is what an upgrade may not change.
fn package(dir: &Path, id: &str, revision: &str, contract: &str, body: &str, tree: bool) -> String {
    let _ = fs::remove_dir_all(dir);
    write(dir, "src/main.py", body, false);
    let mut artifacts = format!(
        r#"{{"name":"main","path":"src/main.py","sha256":"{}"}}"#,
        hex(&sha256(body.as_bytes()))
    );
    if tree {
        write(dir, "data/x.txt", "ex", false);
        write(dir, "data/sub/y.txt", "why", false);
        write(dir, "data/run.sh", "#!/bin/sh\n", true);
        let digest = directory_digest(dir.join("data").to_str().unwrap()).unwrap();
        artifacts.push_str(&format!(
            r#",{{"name":"data","path":"data","sha256":"{}","kind":"tree"}}"#,
            hex(&digest)
        ));
    }
    let manifest = format!(
        r#"{{"apiVersion":"ext.package/v1","descriptor":{{"apiVersion":"ext.plugin/v1","identity":{{"id":"{id}","revision":"{revision}"}},"contracts":[{{"name":"{contract}","version":"v1","operations":[{{"name":"read"}}]}}]}},"artifacts":[{artifacts}]}}"#
    );
    write(dir, "plugin.json", &manifest, false);
    dir.to_str().unwrap().to_owned()
}
fn entries(root: &Path) -> Vec<String> {
    let mut names: Vec<String> = fs::read_dir(root)
        .map(|d| {
            d.map(|e| e.unwrap().file_name().into_string().unwrap())
                .collect()
        })
        .unwrap_or_default();
    names.sort();
    names
}
#[cfg(unix)]
fn mode(path: &Path) -> u32 {
    use std::os::unix::fs::PermissionsExt;
    fs::metadata(path).unwrap().permissions().mode() & 0o777
}

#[test]
fn an_empty_store_has_nothing_to_find() {
    let scratch = Scratch::new();
    let store = Store::open(&scratch.join("store"), Limits::default()).unwrap();
    assert_eq!(store.ids().unwrap(), Vec::<String>::new());
    assert_eq!(store.load("example/a"), Err(Error::NOT_FOUND));
    assert_eq!(store.remove("example/a"), Err(Error::NOT_FOUND));
    let source = package(
        &scratch.0.join("s"),
        "example/a",
        "r1",
        "example.read",
        "one",
        false,
    );
    assert_eq!(store.upgrade(&source, false), Err(Error::NOT_FOUND));
    assert_eq!(store.load("../bad"), Err(Error::INVALID));
    assert_eq!(store.remove("Bad"), Err(Error::INVALID));
    assert_eq!(store.install("", false), Err(Error::INVALID));
    assert!(
        !scratch.0.join("store").exists(),
        "a failed call made the root"
    );
    assert_eq!(
        Store::open("", Limits::default()).err(),
        Some(Error::INVALID)
    );
}

#[test]
fn install_upgrade_replace_and_remove() {
    let scratch = Scratch::new();
    let root = scratch.0.join("store");
    let store = Store::open(&scratch.join("store"), Limits::default()).unwrap();

    // install returns plugin.json as the source had it, and never replaces
    let first = package(
        &scratch.0.join("s1"),
        "example/a",
        "r1",
        "example.read",
        "one",
        true,
    );
    let source = fs::read(scratch.0.join("s1/plugin.json")).unwrap();
    assert_eq!(store.install(&first, false).unwrap(), source);
    assert_eq!(
        fs::read(root.join("example%2fa/plugin.json")).unwrap(),
        source
    );
    assert_eq!(store.install(&first, false), Err(Error::EXISTS));
    assert_eq!(store.ids().unwrap(), ["example/a"]);
    assert_eq!(store.load("example/a").unwrap(), source);
    #[cfg(unix)]
    {
        assert_eq!(mode(&root.join("example%2fa")), 0o700);
        assert_eq!(mode(&root.join("example%2fa/src/main.py")), 0o600);
        assert_eq!(mode(&root.join("example%2fa/data/x.txt")), 0o600);
        assert_eq!(mode(&root.join("example%2fa/data/run.sh")), 0o700);
    }

    // upgrade: a new revision of the same id and contracts, in place
    let second = package(
        &scratch.0.join("s2"),
        "example/a",
        "r2",
        "example.read",
        "two",
        true,
    );
    let renewed = fs::read(scratch.0.join("s2/plugin.json")).unwrap();
    assert_eq!(store.upgrade(&second, false).unwrap(), renewed);
    assert_eq!(
        fs::read_to_string(root.join("example%2fa/src/main.py")).unwrap(),
        "two"
    );
    assert_eq!(
        entries(&root),
        ["example%2fa"],
        "staging or backup left behind"
    );
    assert_eq!(
        store.upgrade(&second, false),
        Err(Error::INVALID),
        "same revision"
    );
    let other = package(
        &scratch.0.join("s3"),
        "example/a",
        "r3",
        "example.write",
        "three",
        false,
    );
    assert_eq!(
        store.upgrade(&other, false),
        Err(Error::MISMATCH),
        "contracts"
    );
    assert_eq!(
        fs::read_to_string(root.join("example%2fa/src/main.py")).unwrap(),
        "two"
    );

    // replace may change the contracts, and installs what is not there
    store.replace(&other, false).unwrap();
    assert_eq!(
        fs::read_to_string(root.join("example%2fa/src/main.py")).unwrap(),
        "three"
    );
    assert!(!root.join("example%2fa/data").exists(), "the tree is gone");
    let bee = package(
        &scratch.0.join("s4"),
        "example/b",
        "r1",
        "example.read",
        "bee",
        false,
    );
    store.replace(&bee, false).unwrap();
    assert_eq!(store.ids().unwrap(), ["example/a", "example/b"]);

    store.remove("example/b").unwrap();
    assert_eq!(store.remove("example/b"), Err(Error::NOT_FOUND));
    assert_eq!(store.load("example/b"), Err(Error::NOT_FOUND));
    store.remove("example/a").unwrap();
    assert_eq!(entries(&root), Vec::<String>::new());
}

#[test]
fn a_source_that_is_not_what_its_manifest_says_is_not_installed() {
    let scratch = Scratch::new();
    let store = Store::open(&scratch.join("store"), Limits::default()).unwrap();
    let dir = scratch.0.join("s");
    let source = package(&dir, "example/c", "r1", "example.read", "cee", true);
    write(&dir, "extra.txt", "unlisted", false);
    assert_eq!(store.install(&source, false), Err(Error::INVALID));
    fs::remove_file(dir.join("extra.txt")).unwrap();
    fs::write(dir.join("src/main.py"), "changed").unwrap();
    assert_eq!(store.install(&source, false), Err(Error::MISMATCH));
    fs::write(dir.join("src/main.py"), "cee").unwrap();
    fs::remove_file(dir.join("data/sub/y.txt")).unwrap();
    assert_eq!(store.install(&source, false), Err(Error::MISMATCH), "tree");
    assert!(store.install("/no/such/directory", false).is_err());
    // nothing was left below the root, whatever it was
    assert!(entries(&scratch.0.join("store")).is_empty());
}

#[test]
fn directories_that_are_not_what_their_name_says() {
    let scratch = Scratch::new();
    let root = scratch.0.join("store");
    let store = Store::open(&scratch.join("store"), Limits::default()).unwrap();
    let source = package(
        &scratch.0.join("s"),
        "example/b",
        "r1",
        "example.read",
        "b",
        false,
    );
    store.install(&source, false).unwrap();
    fs::rename(root.join("example%2fb"), root.join("example%2fz")).unwrap();
    assert_eq!(store.load("example/z"), Err(Error::MISMATCH));
    assert_eq!(store.remove("example/z"), Err(Error::MISMATCH));
    fs::rename(root.join("example%2fz"), root.join("example%2fb")).unwrap();
    // changed behind the store's back
    fs::write(root.join("example%2fb/src/main.py"), "tampered").unwrap();
    assert_eq!(store.load("example/b"), Err(Error::MISMATCH));
    // staging directories are skipped, anything else that is not a package is not
    fs::create_dir(root.join(".install-left")).unwrap();
    assert_eq!(store.ids().unwrap(), ["example/b"]);
    fs::write(root.join("stray"), "x").unwrap();
    assert_eq!(store.ids(), Err(Error::INVALID));
    fs::remove_file(root.join("stray")).unwrap();
    fs::create_dir(root.join("Not-A-Package")).unwrap();
    assert_eq!(store.ids(), Err(Error::INVALID));
}

#[test]
fn limits_bound_an_artifact_and_a_tree() {
    let scratch = Scratch::new();
    let root = scratch.join("store");
    let big = package(
        &scratch.0.join("big"),
        "example/f",
        "r1",
        "example.read",
        "three bytes",
        false,
    );
    let small = Limits {
        max_artifact_bytes: 2,
        ..Limits::default()
    };
    assert_eq!(
        Store::open(&root, small).unwrap().install(&big, false),
        Err(Error::INVALID)
    );
    let tree = package(
        &scratch.0.join("tree"),
        "example/g",
        "r1",
        "example.read",
        "ok",
        true,
    );
    let few = Limits {
        max_tree_files: 2,
        ..Limits::default()
    };
    assert_eq!(
        Store::open(&root, few).unwrap().install(&tree, false),
        Err(Error::INVALID)
    );
    let light = Limits {
        max_tree_bytes: 4,
        ..Limits::default()
    };
    assert_eq!(
        Store::open(&root, light).unwrap().install(&tree, false),
        Err(Error::INVALID)
    );
    assert!(entries(&scratch.0.join("store")).is_empty());
    let store = Store::open(&root, Limits::default()).unwrap();
    store.install(&tree, false).unwrap();
}

#[cfg(unix)]
#[test]
fn preserve_executable_keeps_the_execute_bit_of_a_file_artifact() {
    let scratch = Scratch::new();
    let root = scratch.0.join("store");
    let store = Store::open(&scratch.join("store"), Limits::default()).unwrap();
    let dir = scratch.0.join("s");
    let source = package(&dir, "example/e", "r1", "example.read", "run me", false);
    write(&dir, "src/main.py", "run me", true); // the same bytes, executable
    store.install(&source, false).unwrap();
    assert_eq!(mode(&root.join("example%2fe/src/main.py")), 0o600);
    store.replace(&source, true).unwrap();
    assert_eq!(mode(&root.join("example%2fe/src/main.py")), 0o700);
}

#[test]
fn a_store_is_shared_between_threads() {
    let scratch = Scratch::new();
    let store =
        std::sync::Arc::new(Store::open(&scratch.join("store"), Limits::default()).unwrap());
    let mut sources = Vec::new();
    for i in 0..4 {
        let id = format!("example/t{i}");
        sources.push((
            id.clone(),
            package(
                &scratch.0.join(format!("s{i}")),
                &id,
                "r1",
                "example.read",
                &id,
                false,
            ),
        ));
    }
    let threads: Vec<_> = sources
        .into_iter()
        .map(|(_, source)| {
            let store = store.clone();
            std::thread::spawn(move || store.install(&source, false).unwrap())
        })
        .collect();
    for t in threads {
        t.join().unwrap();
    }
    assert_eq!(
        store.ids().unwrap(),
        ["example/t0", "example/t1", "example/t2", "example/t3"]
    );
}
