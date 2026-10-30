//! The package store of the C engine through the Rust binding: the lifecycle, the
//! failures each call has, permissions, limits and the manifest copied as it was.
//! The Go package store is the baseline; pkg/ext-go/cgoengine holds the C store to
//! it, and this checks that the binding reaches the same behavior.
#![cfg(not(feature = "guest-only"))]
use ext_plugin_engine::{
    directory_digest, sha256,
    store::{Layered, Limits, Store},
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

/// A path as the engine's JSON writes it.
fn quoted(path: &Path) -> String {
    format!(
        "\"{}\"",
        path.to_str()
            .unwrap()
            .replace('\\', "\\\\")
            .replace('"', "\\\"")
    )
}
fn text(bytes: Vec<u8>) -> String {
    String::from_utf8(bytes).unwrap()
}
/// The part of a report line that names an entry.
fn entry(id: &str, directory: &Path, layer: usize) -> String {
    format!(
        r#""id":"{id}","directory":{},"layer":{layer}"#,
        quoted(directory)
    )
}
fn install(root: &Path, scratch: &Scratch, name: &str, id: &str, body: &str) -> Vec<u8> {
    let source = package(&scratch.0.join(name), id, "r1", "example.read", body, false);
    Store::open(root.to_str().unwrap(), Limits::default())
        .unwrap()
        .install(&source, false)
        .unwrap()
}

#[test]
fn reports_cover_every_entry() {
    let scratch = Scratch::new();
    let root = scratch.0.join("store");
    let store = Store::open(&scratch.join("store"), Limits::default()).unwrap();

    // a missing root is empty, and asking does not make it
    assert_eq!(text(store.list().unwrap()), "[]");
    assert_eq!(text(store.verify().unwrap()), "[]");
    assert_eq!(
        text(store.list_unverified().unwrap()),
        r#"{"packages":[],"problems":[]}"#
    );
    assert!(!root.exists(), "a report made the root");

    let a = install(&root, &scratch, "s1", "example/a", "one");
    let b = install(&root, &scratch, "s2", "example/b", "two");
    let a_dir = root.join("example%2fa");
    let b_dir = root.join("example%2fb");
    let manifest = |m: &[u8]| format!(r#""manifest":{}"#, std::str::from_utf8(m).unwrap());

    let listed = text(store.list().unwrap());
    assert!(listed.starts_with(&format!("[{{{}", entry("example/a", &a_dir, 0))));
    assert!(listed.contains(&entry("example/b", &b_dir, 0)));
    assert!(listed.contains(&manifest(&a)) && listed.contains(&manifest(&b)));
    assert_eq!(
        text(store.verify().unwrap()),
        format!(
            "[{{{},\"status\":0}},{{{},\"status\":0}}]",
            entry("example/a", &a_dir, 0),
            entry("example/b", &b_dir, 0)
        )
    );

    // verify reports a bad entry and goes on; list fails closed
    fs::write(b_dir.join("src/main.py"), "tampered").unwrap();
    let checks = text(store.verify().unwrap());
    assert!(checks.contains(&format!("{},\"status\":0", entry("example/a", &a_dir, 0))));
    assert!(checks.contains(&format!(
        "{},\"status\":{}",
        entry("example/b", &b_dir, 0),
        Error::MISMATCH.0
    )));
    assert_eq!(store.list(), Err(Error::MISMATCH));
    // the unverified listing reads no artifact, so it still has both
    let unverified = text(store.list_unverified().unwrap());
    assert!(unverified.contains(&entry("example/a", &a_dir, 0)));
    assert!(unverified.contains(&entry("example/b", &b_dir, 0)));
    assert!(unverified.ends_with(r#""problems":[]}"#));

    // an entry that is not a package is a problem there, an error in list, and
    // a status in verify; staging directories are skipped
    fs::write(root.join("stray"), "x").unwrap();
    fs::create_dir(root.join(".install-left")).unwrap();
    assert_eq!(store.list(), Err(Error::INVALID));
    let unverified = text(store.list_unverified().unwrap());
    assert!(unverified.contains(r#""id":"stray""#) && !unverified.contains("install-left"));
    assert!(unverified.contains(&entry("example/a", &a_dir, 0)));
    assert!(text(store.verify().unwrap()).contains(&format!(
        "{},\"status\":{}",
        entry("stray", &root.join("stray"), 0),
        Error::INVALID.0
    )));
}

#[test]
fn a_layered_view_serves_the_highest_root_and_never_writes() {
    let scratch = Scratch::new();
    let high = scratch.0.join("user");
    let low = scratch.0.join("system");
    let kept = install(&high, &scratch, "s1", "example/a", "high");
    install(&low, &scratch, "s2", "example/a", "low");
    let only_low = install(&low, &scratch, "s3", "example/c", "cee");
    let absent = scratch.0.join("absent");
    let roots = [
        high.to_str().unwrap(),
        low.to_str().unwrap(),
        absent.to_str().unwrap(),
    ];
    let view = Layered::open(&roots, Limits::default()).unwrap();

    assert_eq!(view.load("example/a").unwrap(), (kept, 0));
    assert_eq!(view.load("example/c").unwrap(), (only_low, 1));
    assert_eq!(view.load("example/none"), Err(Error::NOT_FOUND));
    assert_eq!(view.load("../bad"), Err(Error::INVALID));
    let listed = text(view.list().unwrap());
    assert!(listed.contains(&entry("example/a", &high.join("example%2fa"), 0)));
    assert!(listed.contains(&entry("example/c", &low.join("example%2fc"), 1)));
    // the manifest repeats the id, so count the report lines, not the id
    assert_eq!(
        listed.matches(r#""id":"example/a","directory""#).count(),
        1,
        "shadowed"
    );
    assert!(!absent.exists(), "the view made a root");

    // a shadowed copy is never read
    fs::write(low.join("example%2fa/src/main.py"), "tampered").unwrap();
    assert!(view.load("example/a").is_ok());
    assert!(view.list().is_ok());

    // a damaged winner fails closed instead of falling back to the copy below
    fs::write(high.join("example%2fa/src/main.py"), "tampered").unwrap();
    assert_eq!(view.load("example/a"), Err(Error::MISMATCH));
    assert_eq!(view.list(), Err(Error::MISMATCH));
    assert!(text(view.verify().unwrap()).contains(&format!(
        "{},\"status\":{}",
        entry("example/a", &high.join("example%2fa"), 0),
        Error::MISMATCH.0
    )));
    let unverified = text(view.list_unverified().unwrap());
    assert!(unverified.contains(&entry("example/c", &low.join("example%2fc"), 1)));
    assert!(unverified.ends_with(r#""problems":[]}"#));
}

#[test]
fn a_layered_view_refuses_what_is_not_a_set_of_roots() {
    let scratch = Scratch::new();
    let one = scratch.join("one");
    let limits = Limits::default();
    assert_eq!(Layered::open(&[], limits).err(), Some(Error::INVALID));
    assert_eq!(Layered::open(&[""], limits).err(), Some(Error::INVALID));
    assert_eq!(
        Layered::open(&[&one, &one], limits).err(),
        Some(Error::INVALID)
    );
    let spelled = format!("{}/./", one);
    assert_eq!(
        Layered::open(&[&one, &spelled], limits).err(),
        Some(Error::INVALID),
        "the same root spelled differently"
    );
    assert!(Layered::open(&[&one, &scratch.join("two")], limits).is_ok());
}
