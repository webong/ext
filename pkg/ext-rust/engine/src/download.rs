//! Downloading a package archive over HTTPS, for [`Store::install_archive`] to
//! install (feature `download`). The engine has no HTTPS, so the binding fetches
//! the file and the engine checks it against the pinned digest.
//!
//! The rules are those of every binding (lib/plugin/testdata/v1/download-cases.json):
//! only `https` URLs with a host and without user information or a fragment, for the
//! first request and for every redirect; at most [`MAX_REDIRECTS`] redirects;
//! `Authorization`, `Cookie` and `Referer` are not sent after a redirect; the body is
//! no larger than the limit, whatever `Content-Length` says; and a download that
//! fails leaves no file and never changes one that existed. The content is not
//! verified here: [`Store::install_archive`] checks it against the pinned digest
//! before anything is extracted.
use crate::{store::Store, Error};
use std::{
    fmt, fs,
    io::{self, Read, Write},
    path::{Path, PathBuf},
    sync::atomic::{AtomicUsize, Ordering},
    time::Duration,
};
use ureq::tls::{Certificate, RootCerts, TlsConfig};

/// How many redirects a download follows.
pub const MAX_REDIRECTS: usize = 5;
/// The body limit when none is given: 512 MiB, the store's default archive limit.
pub const DEFAULT_MAX_BYTES: u64 = 512 << 20;

/// Headers that never follow a redirect.
const PRIVATE_HEADERS: [&str; 3] = ["authorization", "cookie", "referer"];

/// Why a download failed.
#[derive(Debug)]
pub enum DownloadError {
    /// Refused by the download rules: a URL that is not allowed, too many
    /// redirects, or a body over the limit. [`Error::INVALID`] as a status.
    Invalid(&'static str),
    /// The origin answered with a status other than 200.
    Status(u16),
    /// The transfer or the file failed, including a destination that exists.
    Io(io::Error),
    /// The engine refused the downloaded archive.
    Store(Error),
}
impl fmt::Display for DownloadError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Invalid(why) => write!(f, "download refused: {why}"),
            Self::Status(code) => write!(f, "download: origin returned HTTP {code}"),
            Self::Io(err) => write!(f, "download: {err}"),
            Self::Store(err) => write!(f, "install: {err}"),
        }
    }
}
impl std::error::Error for DownloadError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Io(err) => Some(err),
            Self::Store(err) => Some(err),
            _ => None,
        }
    }
}
impl From<io::Error> for DownloadError {
    fn from(err: io::Error) -> Self {
        Self::Io(err)
    }
}
impl From<ureq::Error> for DownloadError {
    fn from(err: ureq::Error) -> Self {
        match err {
            ureq::Error::Io(err) => Self::Io(err),
            other => Self::Io(io::Error::other(other)),
        }
    }
}
impl DownloadError {
    /// The engine status this failure is: [`Error::INVALID`] for a refusal by the
    /// rules, the engine's own status for [`DownloadError::Store`], and `None` for
    /// a failure of the origin or the transfer.
    pub fn status(&self) -> Option<Error> {
        match self {
            Self::Invalid(_) => Some(Error::INVALID),
            Self::Store(err) => Some(*err),
            _ => None,
        }
    }
}

/// An HTTPS client that follows the download rules. [`Downloader::new`] trusts
/// the Web PKI roots bundled with the crate (webpki-roots); a private origin's
/// roots are given with [`Downloader::with_root_certificates`]. It has no timeout
/// unless [`Downloader::with_timeout`] sets one. A proxy named by the
/// environment (`HTTPS_PROXY`, `ALL_PROXY`, `NO_PROXY`) is used.
#[derive(Clone)]
pub struct Downloader {
    agent: ureq::Agent,
    roots: Option<Vec<Vec<u8>>>,
    timeout: Option<Duration>,
}
impl Default for Downloader {
    fn default() -> Self {
        Self::new()
    }
}
impl fmt::Debug for Downloader {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Downloader")
            .field("private_roots", &self.roots.as_ref().map(Vec::len))
            .field("timeout", &self.timeout)
            .finish()
    }
}
impl Downloader {
    /// A client that trusts the Web PKI roots.
    pub fn new() -> Self {
        Self::build(None, None)
    }
    /// The same client trusting only these DER-encoded root certificates.
    pub fn with_root_certificates(&self, roots: &[&[u8]]) -> Self {
        let roots = roots.iter().map(|der| der.to_vec()).collect();
        Self::build(Some(roots), self.timeout)
    }
    /// The same client with a bound on each whole download, redirects included.
    pub fn with_timeout(&self, timeout: Duration) -> Self {
        Self::build(self.roots.clone(), Some(timeout))
    }
    fn build(roots: Option<Vec<Vec<u8>>>, timeout: Option<Duration>) -> Self {
        let mut tls = TlsConfig::builder();
        if let Some(roots) = &roots {
            let certs: Vec<Certificate<'static>> = roots
                .iter()
                .map(|der| Certificate::from_der(der).to_owned())
                .collect();
            tls = tls.root_certs(RootCerts::new_with_certs(&certs));
        }
        let agent = ureq::Agent::config_builder()
            .https_only(true)
            // Redirects are followed here, so every hop is checked.
            .max_redirects(0)
            .max_redirects_will_error(false)
            .http_status_as_error(false)
            .timeout_global(timeout)
            .tls_config(tls.build())
            .build()
            .new_agent();
        Self {
            agent,
            roots,
            timeout,
        }
    }

    /// Fetches `url` into the new file `destination`, which must not exist, and
    /// fails when the body is larger than `max_bytes` ([`DEFAULT_MAX_BYTES`] when
    /// zero). `headers` go with the first request; `Authorization`, `Cookie` and
    /// `Referer` among them are dropped at the first redirect, the others are kept.
    /// Nothing is left at `destination` on failure.
    pub fn download(
        &self,
        url: &str,
        destination: &Path,
        max_bytes: u64,
        headers: &[(&str, &str)],
    ) -> Result<(), DownloadError> {
        let max_bytes = if max_bytes == 0 {
            DEFAULT_MAX_BYTES
        } else {
            max_bytes
        };
        check_url(url)?;
        let mut current = url.to_owned();
        let mut redirects = 0;
        let response = loop {
            let mut request = self.agent.get(&current);
            for (name, value) in headers {
                if redirects == 0 || !PRIVATE_HEADERS.contains(&name.to_ascii_lowercase().as_str())
                {
                    request = request.header(*name, *value);
                }
            }
            let response = request.call()?;
            let status = response.status().as_u16();
            if !matches!(status, 301 | 302 | 303 | 307 | 308) {
                break response;
            }
            let location = response
                .headers()
                .get("location")
                .and_then(|value| value.to_str().ok())
                .ok_or(DownloadError::Invalid("redirect without a location"))?;
            let next = resolve(&current, location)?;
            check_url(&next)?;
            redirects += 1;
            if redirects > MAX_REDIRECTS {
                return Err(DownloadError::Invalid("too many redirects"));
            }
            current = next;
        };
        if response.status().as_u16() != 200 {
            return Err(DownloadError::Status(response.status().as_u16()));
        }
        let declared = response
            .headers()
            .get("content-length")
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.trim().parse::<u64>().ok());
        if declared.is_some_and(|length| length > max_bytes) {
            return Err(DownloadError::Invalid("download exceeds size limit"));
        }
        let mut file = create_new(destination)?;
        let mut body = response.into_body().into_reader().take(max_bytes + 1);
        let copied = io::copy(&mut body, &mut file).and_then(|written| {
            file.flush()?;
            file.sync_all()?;
            Ok(written)
        });
        drop(file);
        let failure = match copied {
            Ok(written) if written > max_bytes => {
                DownloadError::Invalid("download exceeds size limit")
            }
            Ok(_) => return Ok(()),
            Err(err) => DownloadError::Io(err),
        };
        let _ = fs::remove_file(destination);
        Err(failure)
    }

    /// Downloads `url` under the store's archive limit into a new private
    /// directory of the system's temporary directory, installs it with
    /// [`Store::install_archive`] and removes the download, whatever happened.
    pub fn install(
        &self,
        store: &Store,
        url: &str,
        sha256: &str,
        id: Option<&str>,
        preserve_executable: bool,
    ) -> Result<Vec<u8>, DownloadError> {
        let scratch = Scratch::new()?;
        let archive = scratch.0.join("archive");
        self.download(url, &archive, store.max_archive_bytes(), &[])?;
        let path = archive
            .to_str()
            .ok_or(DownloadError::Store(Error::INVALID))?;
        store
            .install_archive(path, sha256, id, preserve_executable)
            .map_err(DownloadError::Store)
    }
}

/// A private directory removed when it goes out of scope.
struct Scratch(PathBuf);
impl Scratch {
    fn new() -> io::Result<Self> {
        static NEXT: AtomicUsize = AtomicUsize::new(0);
        let path = std::env::temp_dir().join(format!(
            "ext-download-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        let mut builder = fs::DirBuilder::new();
        #[cfg(unix)]
        std::os::unix::fs::DirBuilderExt::mode(&mut builder, 0o700);
        builder.create(&path)?;
        Ok(Self(path))
    }
}
impl Drop for Scratch {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn create_new(path: &Path) -> io::Result<fs::File> {
    let mut options = fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    std::os::unix::fs::OpenOptionsExt::mode(&mut options, 0o600);
    options.open(path)
}

/// The scheme, authority and the rest of an absolute URL.
fn split(url: &str) -> Option<(&str, &str, &str)> {
    let (scheme, rest) = url.split_once("://")?;
    let end = rest.find(['/', '?', '#']).unwrap_or(rest.len());
    Some((scheme, &rest[..end], &rest[end..]))
}

/// Checks a URL against the rules: `https`, a host, no user information and no fragment.
fn check_url(url: &str) -> Result<(), DownloadError> {
    if url.bytes().any(|b| b <= b' ' || b == 0x7f) {
        return Err(DownloadError::Invalid(
            "URL holds a space or control character",
        ));
    }
    let (scheme, authority, _) = split(url).ok_or(DownloadError::Invalid("URL is not absolute"))?;
    if !scheme.eq_ignore_ascii_case("https") {
        return Err(DownloadError::Invalid("URL is not https"));
    }
    if authority.contains('@') {
        return Err(DownloadError::Invalid("URL holds user information"));
    }
    if url.contains('#') {
        return Err(DownloadError::Invalid("URL holds a fragment"));
    }
    let host = authority
        .rsplit_once(':')
        .filter(|(_, port)| !port.contains(']'))
        .map_or(authority, |(host, _)| host);
    if host.is_empty() {
        return Err(DownloadError::Invalid("URL has no host"));
    }
    url.parse::<ureq::http::Uri>()
        .map(|_| ())
        .map_err(|_| DownloadError::Invalid("URL does not parse"))
}

/// Resolves a redirect's `Location` against the URL that answered it. Dot
/// segments are left for the origin to interpret.
fn resolve(base: &str, location: &str) -> Result<String, DownloadError> {
    let has_scheme = location.split_once(':').is_some_and(|(scheme, _)| {
        !scheme.is_empty()
            && scheme.starts_with(|c: char| c.is_ascii_alphabetic())
            && scheme
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || matches!(c, '+' | '-' | '.'))
    });
    if has_scheme {
        return Ok(location.to_owned());
    }
    let (scheme, authority, rest) =
        split(base).ok_or(DownloadError::Invalid("URL is not absolute"))?;
    if location.starts_with("//") {
        return Ok(format!("{scheme}:{location}"));
    }
    if location.starts_with('/') {
        return Ok(format!("{scheme}://{authority}{location}"));
    }
    let path = rest.split(['?', '#']).next().unwrap_or("");
    if location.is_empty() || location.starts_with('#') {
        return Ok(format!("{scheme}://{authority}{rest}{location}"));
    }
    if location.starts_with('?') {
        return Ok(format!("{scheme}://{authority}{path}{location}"));
    }
    let directory = path.rfind('/').map_or("/", |end| &path[..=end]);
    Ok(format!("{scheme}://{authority}{directory}{location}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn resolves_locations() {
        let base = "https://a.example:8443/x/y?q";
        for (location, want) in [
            ("/p", "https://a.example:8443/p"),
            ("p", "https://a.example:8443/x/p"),
            ("?r", "https://a.example:8443/x/y?r"),
            ("//b.example/p", "https://b.example/p"),
            ("http://b.example/p", "http://b.example/p"),
            ("file:///etc/hosts", "file:///etc/hosts"),
        ] {
            assert_eq!(resolve(base, location).unwrap(), want, "{location}");
        }
    }

    #[test]
    fn checks_urls() {
        assert!(check_url("https://a.example/p").is_ok());
        assert!(check_url("https://[::1]:8443/p").is_ok());
        for url in [
            "http://a.example/p",
            "https:///p",
            "https://:443/p",
            "https://u@a.example/p",
            "https://a.example/p#f",
            "https://a.example/p q",
            "file:///etc/hosts",
            "a.example/p",
        ] {
            assert!(check_url(url).is_err(), "{url}");
        }
    }
}
