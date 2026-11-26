//! The download fixtures every binding runs: lib/plugin/testdata/v1/download-cases.json,
//! served from a local HTTPS origin with a certificate made for the test, beside a plain
//! HTTP server that must never receive a request. The fixtures are outside this crate,
//! so the test skips when the crate is used without the repository.
#![cfg(not(feature = "guest-only"))]
use ext_plugin_engine::{
    download::{DownloadError, Downloader, MAX_REDIRECTS},
    store::{Limits, Store},
    Error,
};
use rustls::pki_types::{CertificateDer, PrivateKeyDer, PrivatePkcs8KeyDer};
use serde_json::Value;
use std::{
    collections::HashMap,
    fs,
    io::{BufRead, BufReader, Write},
    net::{SocketAddr, TcpListener, TcpStream},
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicUsize, Ordering},
        Arc, Mutex,
    },
    thread,
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
            "ext-download-test-{}-{}",
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

/// One answer of the origin: a status and body, or a redirect.
#[derive(Clone)]
enum Answer {
    Body {
        status: u16,
        body: Vec<u8>,
        chunked: bool,
    },
    Redirect(String),
}

/// A local HTTPS origin and the plain HTTP server beside it.
struct Servers {
    origin: SocketAddr,
    plain: SocketAddr,
    plain_hits: Arc<AtomicUsize>,
    /// The headers of the last request the origin received, names in lower case.
    landed: Arc<Mutex<HashMap<String, String>>>,
    root: Vec<u8>,
}

fn serve(routes: HashMap<String, Answer>) -> Servers {
    let certified = rcgen::generate_simple_self_signed(vec!["127.0.0.1".to_owned()]).unwrap();
    let root = certified.cert.der().to_vec();
    let key = PrivateKeyDer::Pkcs8(PrivatePkcs8KeyDer::from(
        certified.signing_key.serialize_der(),
    ));
    let config = Arc::new(
        rustls::ServerConfig::builder_with_provider(Arc::new(
            rustls::crypto::ring::default_provider(),
        ))
        .with_safe_default_protocol_versions()
        .unwrap()
        .with_no_client_auth()
        .with_single_cert(vec![CertificateDer::from(root.clone())], key)
        .unwrap(),
    );

    let plain_listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let plain = plain_listener.local_addr().unwrap();
    let plain_hits = Arc::new(AtomicUsize::new(0));
    let hits = plain_hits.clone();
    thread::spawn(move || {
        for stream in plain_listener.incoming().flatten() {
            hits.fetch_add(1, Ordering::SeqCst);
            drop(stream);
        }
    });

    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let origin = listener.local_addr().unwrap();
    let landed = Arc::new(Mutex::new(HashMap::new()));
    let fill = move |value: &str| {
        value
            .replace("{origin}", &origin.to_string())
            .replace("{plain}", &plain.to_string())
    };
    let routes: Arc<HashMap<String, Answer>> = Arc::new(
        routes
            .into_iter()
            .map(|(path, answer)| {
                let answer = match answer {
                    Answer::Redirect(to) => Answer::Redirect(fill(&to)),
                    other => other,
                };
                (path, answer)
            })
            .collect(),
    );
    let seen = landed.clone();
    thread::spawn(move || {
        for stream in listener.incoming().flatten() {
            let (config, routes, seen) = (config.clone(), routes.clone(), seen.clone());
            thread::spawn(move || {
                let _ = answer(stream, config, &routes, &seen);
            });
        }
    });
    Servers {
        origin,
        plain,
        plain_hits,
        landed,
        root,
    }
}

fn answer(
    tcp: TcpStream,
    config: Arc<rustls::ServerConfig>,
    routes: &HashMap<String, Answer>,
    seen: &Mutex<HashMap<String, String>>,
) -> std::io::Result<()> {
    let connection = rustls::ServerConnection::new(config).map_err(std::io::Error::other)?;
    let mut tls = BufReader::new(rustls::StreamOwned::new(connection, tcp));
    let mut line = String::new();
    tls.read_line(&mut line)?;
    let path = line.split(' ').nth(1).unwrap_or("").to_owned();
    let mut headers = HashMap::new();
    loop {
        let mut header = String::new();
        tls.read_line(&mut header)?;
        let header = header.trim_end();
        if header.is_empty() {
            break;
        }
        if let Some((name, value)) = header.split_once(':') {
            headers.insert(name.trim().to_ascii_lowercase(), value.trim().to_owned());
        }
    }
    *seen.lock().unwrap() = headers;
    let mut out = Vec::new();
    match routes.get(&path).cloned() {
        Some(Answer::Redirect(to)) => {
            write!(
                out,
                "HTTP/1.1 302 Found\r\nLocation: {to}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
            )?;
        }
        Some(Answer::Body {
            status,
            body,
            chunked,
        }) => {
            write!(out, "HTTP/1.1 {status} Answer\r\nConnection: close\r\n")?;
            if chunked {
                write!(out, "Transfer-Encoding: chunked\r\n\r\n")?;
                for chunk in body.chunks(512) {
                    write!(out, "{:x}\r\n", chunk.len())?;
                    out.extend_from_slice(chunk);
                    out.extend_from_slice(b"\r\n");
                }
                out.extend_from_slice(b"0\r\n\r\n");
            } else {
                write!(out, "Content-Length: {}\r\n\r\n", body.len())?;
                out.extend_from_slice(&body);
            }
        }
        None => {
            eprintln!("no route {path}");
            write!(
                out,
                "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
            )?;
        }
    }
    let stream = tls.get_mut();
    stream.write_all(&out)?;
    stream.conn.send_close_notify();
    stream.flush()
}

fn routes(fixture: &Value) -> HashMap<String, Answer> {
    fixture["routes"]
        .as_object()
        .unwrap()
        .iter()
        .map(|(path, route)| {
            let answer = if let Some(to) = route["redirect"].as_str() {
                Answer::Redirect(to.to_owned())
            } else {
                let body = match route["bodyBytes"].as_u64() {
                    Some(n) => vec![b'x'; n as usize],
                    None => route["body"].as_str().unwrap_or("").as_bytes().to_vec(),
                };
                Answer::Body {
                    status: route["status"].as_u64().unwrap_or(200) as u16,
                    body,
                    chunked: route["chunked"].as_bool().unwrap_or(false),
                }
            };
            (path.clone(), answer)
        })
        .collect()
}

fn outcome(result: &Result<(), DownloadError>) -> &'static str {
    match result {
        Ok(()) => "ok",
        Err(DownloadError::Invalid(_)) => "invalid",
        Err(_) => "failed",
    }
}

#[test]
fn download_cases() {
    let Ok(data) = fs::read(fixtures().join("download-cases.json")) else {
        eprintln!("skipped: needs the repository's lib/plugin/testdata");
        return;
    };
    let fixture: Value = serde_json::from_slice(&data).unwrap();
    assert_eq!(fixture["maxRedirects"].as_u64(), Some(MAX_REDIRECTS as u64));
    let cases = fixture["cases"].as_array().unwrap();
    assert!(!cases.is_empty(), "no cases");
    let servers = serve(routes(&fixture));
    let downloader = Downloader::new().with_root_certificates(&[&servers.root]);
    let fill = |value: &str| {
        value
            .replace("{origin}", &servers.origin.to_string())
            .replace("{plain}", &servers.plain.to_string())
    };
    for case in cases {
        let name = case["name"].as_str().unwrap();
        let scratch = Scratch::new();
        let destination = scratch.0.join("out");
        let exists = case["destinationExists"].as_bool().unwrap_or(false);
        if exists {
            fs::write(&destination, "mine").unwrap();
        }
        let headers: Vec<(String, String)> = case["headers"]
            .as_object()
            .map(|headers| {
                headers
                    .iter()
                    .map(|(name, value)| (name.clone(), value.as_str().unwrap().to_owned()))
                    .collect()
            })
            .unwrap_or_default();
        let headers: Vec<(&str, &str)> = headers
            .iter()
            .map(|(name, value)| (name.as_str(), value.as_str()))
            .collect();
        let result = downloader.download(
            &fill(case["url"].as_str().unwrap()),
            &destination,
            case["maxBytes"].as_u64().unwrap_or(0),
            &headers,
        );
        let want = case["expect"].as_str().unwrap();
        assert_eq!(outcome(&result), want, "{name}: {result:?}");
        let data = fs::read(&destination);
        if exists {
            assert_eq!(
                data.unwrap(),
                b"mine",
                "{name}: the existing file was changed"
            );
        } else if want == "ok" {
            assert_eq!(
                data.unwrap(),
                case["body"].as_str().unwrap().as_bytes(),
                "{name}"
            );
        } else {
            assert!(data.is_err(), "{name}: a failed download left a file");
        }
        let landed = servers.landed.lock().unwrap();
        for header in case["absentAtLanding"].as_array().into_iter().flatten() {
            let header = header.as_str().unwrap().to_ascii_lowercase();
            assert!(
                !landed.contains_key(&header),
                "{name}: {header} followed the redirect"
            );
        }
        assert_eq!(
            servers.plain_hits.load(Ordering::SeqCst),
            0,
            "{name}: a request went to the plain HTTP server"
        );
    }
}

#[test]
fn install_downloads_then_installs() {
    let dir = fixtures();
    let Ok(archive) = fs::read(dir.join("archives/tar-plain.bin")) else {
        eprintln!("skipped: needs the repository's lib/plugin/testdata");
        return;
    };
    let cases: Value =
        serde_json::from_slice(&fs::read(dir.join("archive-cases.json")).unwrap()).unwrap();
    let sha256 = cases["cases"]
        .as_array()
        .unwrap()
        .iter()
        .flat_map(|case| case["steps"].as_array().unwrap())
        .find(|step| step["install"] == "archives/tar-plain.bin")
        .and_then(|step| step["sha256"].as_str())
        .unwrap()
        .to_owned();
    let servers = serve(HashMap::from([(
        "/archive".to_owned(),
        Answer::Body {
            status: 200,
            body: archive,
            chunked: false,
        },
    )]));
    let downloader = Downloader::new().with_root_certificates(&[&servers.root]);
    let url = format!("https://{}/archive", servers.origin);
    let scratch = Scratch::new();
    let root = scratch.0.join("plugins");
    let store = Store::open(root.to_str().unwrap(), Limits::default()).unwrap();

    let other = "0".repeat(64);
    match downloader.install(&store, &url, &other, None, false) {
        Err(DownloadError::Store(err)) => assert_eq!(err, Error::MISMATCH),
        other => panic!("a wrong digest installed: {other:?}"),
    }
    let manifest = downloader
        .install(&store, &url, &sha256, Some("example/archive"), false)
        .unwrap();
    let manifest: Value = serde_json::from_slice(&manifest).unwrap();
    assert_eq!(manifest["identity"]["revision"], "r1");
    let receipt: Value =
        serde_json::from_slice(&store.receipt("example/archive").unwrap()).unwrap();
    assert_eq!(receipt["artifact_sha256"], sha256.as_str());

    // The archive limit of the store bounds the download.
    let small = Store::open(
        scratch.0.join("small").to_str().unwrap(),
        Limits {
            max_archive_bytes: 64,
            ..Limits::default()
        },
    )
    .unwrap();
    match downloader.install(&small, &url, &sha256, None, false) {
        Err(err) => assert_eq!(err.status(), Some(Error::INVALID), "{err}"),
        Ok(_) => panic!("an archive over the limit installed"),
    }
}
