#![cfg(not(feature = "guest-only"))]
use ext_plugin_engine::{resources::*, CallOptions, Error};
use std::{
    sync::{
        atomic::{AtomicUsize, Ordering},
        Arc,
    },
    time::Duration,
};

#[test]
fn leases_pin_replaced_values_and_close_is_retryable() {
    let disposed = Arc::new(AtomicUsize::new(0));
    let counter = disposed.clone();
    let instances = Instances::new(
        3,
        Box::new(|_| Ok(())),
        Box::new(|ctx, key, config| {
            assert!(!ctx.is_canceled());
            assert_eq!(key, b"one");
            Ok(config.to_vec())
        }),
        Box::new(move |_| {
            counter.fetch_add(1, Ordering::SeqCst);
            Ok(())
        }),
    )
    .unwrap();
    let options = CallOptions::default();
    instances.configure(b"one", b"r1", b"1", options).unwrap();
    let lease = instances.acquire(b"one").unwrap();
    instances.configure(b"one", b"r2", b"2", options).unwrap();
    assert_eq!(lease.revision(), b"r1");
    assert_eq!(lease.value(), b"1");
    assert_eq!(disposed.load(Ordering::SeqCst), 0);
    let short = CallOptions {
        timeout: Duration::from_millis(10),
        cancel: None,
    };
    assert_eq!(instances.close(short), Err(Error(6)));
    assert!(matches!(instances.acquire(b"one"), Err(Error(5))));
    lease.release().unwrap();
    instances.close(options).unwrap();
    assert_eq!(disposed.load(Ordering::SeqCst), 2);
}

struct Reader {
    closed: Arc<AtomicUsize>,
    dropped: Arc<AtomicUsize>,
}
impl Stream for Reader {
    fn read(&self, ctx: Context<'_>, limit: u32) -> Result<Vec<u8>, Error> {
        assert!(!ctx.is_canceled());
        assert_eq!(limit, 1);
        Ok(br#"{"items":[7],"done":false}"#.to_vec())
    }
    fn close(&self) -> Result<(), Error> {
        self.closed.fetch_add(1, Ordering::SeqCst);
        Ok(())
    }
}
impl Drop for Reader {
    fn drop(&mut self) {
        self.dropped.fetch_add(1, Ordering::SeqCst);
    }
}
#[test]
fn scoped_streams_enforce_sequences_and_release_once() {
    let closed = Arc::new(AtomicUsize::new(0));
    let dropped = Arc::new(AtomicUsize::new(0));
    let c = closed.clone();
    let d = dropped.clone();
    let streams = Streams::new(
        2,
        Duration::from_secs(5),
        Box::new(move |_, _| {
            Ok(Reader {
                closed: c.clone(),
                dropped: d.clone(),
            })
        }),
    )
    .unwrap();
    let options = CallOptions::default();
    let id = streams.open(b"alice", b"null", options).unwrap();
    assert_eq!(streams.read(b"bob", &id, 1, 1, options), Err(Error(2)));
    let first = streams.read(b"alice", &id, 1, 1, options).unwrap();
    assert!(std::str::from_utf8(&first).unwrap().contains("[7]"));
    assert_eq!(streams.read(b"alice", &id, 1, 1, options), Err(Error(15)));
    streams.remove(b"alice", &id).unwrap();
    streams.close().unwrap();
    drop(streams);
    assert_eq!(closed.load(Ordering::SeqCst), 1);
    assert_eq!(dropped.load(Ordering::SeqCst), 1);
}

#[test]
fn factory_panic_is_contained_at_ffi_boundary() {
    let instances: Instances<()> = Instances::new(
        1,
        Box::new(|_| Ok(())),
        Box::new(|_, _, _| panic!("private")),
        Box::new(|_| Ok(())),
    )
    .unwrap();
    assert_eq!(
        instances.configure(b"one", b"r1", b"null", CallOptions::default()),
        Err(Error(7))
    );
    instances.close(CallOptions::default()).unwrap();
}

#[test]
fn host_observer_gets_bounded_metadata() {
    use ext_plugin_engine::{Host, Process};
    let count = Arc::new(AtomicUsize::new(0));
    let observed = count.clone();
    let host = Host::process(
        Process {
            executable: "/not-launched",
            ..Default::default()
        },
        include_bytes!("../../../../pkg/plugin/testdata/v1/descriptor.json"),
        Box::new(|_| Err(Error(2))),
        Box::new(|_| Ok(())),
    )
    .unwrap()
    .with_observer(Box::new(move |event| {
        assert!(std::str::from_utf8(event)
            .unwrap()
            .contains("\"stage\":\"verify\""));
        observed.fetch_add(1, Ordering::SeqCst);
    }))
    .unwrap();
    assert_eq!(host.start(CallOptions::default()), Err(Error(2)));
    assert_eq!(count.load(Ordering::SeqCst), 1);
}
