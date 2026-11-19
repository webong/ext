import Foundation
#if canImport(Security)
import Security
#endif

/// Why `ArchiveDownloader` did not deliver an archive.
public enum ArchiveDownloadError: Error, Equatable {
    /// Refused by the download rules: a URL, first or redirected, that is not HTTPS, has no
    /// host, or carries user information or a fragment; more than
    /// `ArchiveDownloader.maxRedirects` redirects; a body, declared or received, over the
    /// limit; or a destination or limit that is not usable.
    case invalid
    /// The origin answered with this HTTP status instead of 200.
    case status(Int)
    /// The transfer failed: a connection, TLS or cancellation error.
    case transport(URLError.Code)
    /// The destination could not be created or written; the POSIX error number. A
    /// destination that already exists is `EEXIST`.
    case io(Int32)
    /// The archive was downloaded and the store refused it (`ArchiveDownloader.install`).
    case store(PackageStoreError)
}

/// Downloads a package archive over HTTPS with `URLSession`, under the rules every
/// language's download follows (lib/plugin/testdata/v1/download-cases.json):
///
/// - only `https` URLs with a host and without user information or a fragment, for the
///   first request and for every redirect;
/// - at most `maxRedirects` redirects;
/// - `Authorization`, `Cookie` and `Referer` are removed from every redirected request, and
///   the session neither stores nor sends cookies of its own;
/// - the body is no larger than the limit, whatever `Content-Length` says;
/// - the destination is a new file (owner-only), and a download that fails leaves no file
///   and never changes one that existed.
///
/// The content is not verified: `install` hands it to `PackageStore.installArchive`, which
/// checks the pinned digest before it extracts anything. The downloader has no timeout of
/// its own beyond the configuration's; cancel the task to stop a transfer.
public final class ArchiveDownloader: @unchecked Sendable {
    /// How many redirects a download follows.
    public static let maxRedirects = 5

    /// Decides whether to trust a server, given its trust object and host. Return true to
    /// accept the connection.
    public typealias ServerTrust = (SecTrust, String) -> Bool

    private let configuration: URLSessionConfiguration
    private let serverTrust: ServerTrust?

    /// A downloader over a copy of `configuration` (ephemeral by default), with its cookie
    /// storage, cookie sending, credential storage and cache turned off. `serverTrust`
    /// replaces the system's evaluation of a server's certificate, for a private
    /// certificate authority or a test server; nil keeps the system's.
    public init(configuration: URLSessionConfiguration = .ephemeral, serverTrust: ServerTrust? = nil) {
        let copy = configuration.copy() as? URLSessionConfiguration ?? .ephemeral
        copy.httpCookieStorage = nil
        copy.httpShouldSetCookies = false
        copy.httpCookieAcceptPolicy = .never
        copy.urlCredentialStorage = nil
        copy.urlCache = nil
        copy.requestCachePolicy = .reloadIgnoringLocalCacheData
        self.configuration = copy
        self.serverTrust = serverTrust
    }

    /// Whether `url` is one a download may request: `https`, a host, no user information
    /// and no fragment.
    public static func isAllowed(_ url: URL?) -> Bool {
        guard let url, url.scheme?.lowercased() == "https", let host = url.host, !host.isEmpty else { return false }
        return url.user == nil && url.password == nil && url.fragment == nil
    }

    /// Downloads `source` into the new file `destination`, sending `headers` with the first
    /// request only. The body may be at most `maxBytes` (the store's default archive bound
    /// when zero).
    public func download(_ source: URL, to destination: URL, maxBytes: UInt64 = 0,
                         headers: [String: String] = [:]) async throws {
        let limit = maxBytes == 0 ? PackageStoreLimits.defaultMaxArchiveBytes : maxBytes
        guard Self.isAllowed(source), destination.isFileURL, !destination.path.isEmpty,
              limit <= UInt64(Int64.max) else { throw ArchiveDownloadError.invalid }
        var request = URLRequest(url: source)
        request.httpMethod = "GET"
        for (name, value) in headers { request.setValue(value, forHTTPHeaderField: name) }

        let transfer = Transfer(destination: destination.path, limit: limit, serverTrust: serverTrust)
        let queue = OperationQueue()
        queue.maxConcurrentOperationCount = 1
        let session = URLSession(configuration: configuration, delegate: transfer, delegateQueue: queue)
        defer { session.finishTasksAndInvalidate() }
        let task = session.dataTask(with: request)
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                transfer.continuation = continuation
                task.resume()
            }
        } onCancel: {
            task.cancel()
        }
    }

    /// Downloads `source` into a private temporary directory, bounded by the store's
    /// `maxArchiveBytes`, installs it with `PackageStore.installArchive` against the pinned
    /// `sha256` (and `id`, when given) and removes the download. Returns the installed
    /// `plugin.json`. A store refusal is `ArchiveDownloadError.store`.
    @discardableResult
    public func install(_ source: URL, sha256: String, id: String? = nil, into store: PackageStore,
                        headers: [String: String] = [:], preserveExecutable: Bool = false) async throws -> Data {
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("ext-archive-download-\(UUID().uuidString)")
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false,
                                                    attributes: [.posixPermissions: 0o700])
        } catch {
            throw ArchiveDownloadError.io((error as NSError).domain == NSPOSIXErrorDomain ? Int32((error as NSError).code) : EIO)
        }
        defer { try? FileManager.default.removeItem(at: directory) }
        let archive = directory.appendingPathComponent("archive")
        try await download(source, to: archive, maxBytes: store.limits.maxArchiveBytes, headers: headers)
        do {
            return try store.installArchive(archive.path, sha256: sha256, id: id, preserveExecutable: preserveExecutable)
        } catch let error as PackageStoreError {
            throw ArchiveDownloadError.store(error)
        }
    }
}

/// One download's state. Its delegate queue is serial, so the callbacks never overlap.
private final class Transfer: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    private static let strippedOnRedirect = ["Authorization", "Cookie", "Referer"]

    let destination: String
    let limit: UInt64
    let serverTrust: ArchiveDownloader.ServerTrust?
    var continuation: CheckedContinuation<Void, Error>?
    private var redirects = 0
    private var failure: ArchiveDownloadError?
    private var descriptor: Int32 = -1
    private var received: UInt64 = 0

    init(destination: String, limit: UInt64, serverTrust: ArchiveDownloader.ServerTrust?) {
        self.destination = destination
        self.limit = limit
        self.serverTrust = serverTrust
    }

    private func fail(_ error: ArchiveDownloadError, _ task: URLSessionTask) {
        if failure == nil { failure = error }
        task.cancel()
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        redirects += 1
        guard redirects <= ArchiveDownloader.maxRedirects, ArchiveDownloader.isAllowed(request.url) else {
            fail(.invalid, task)
            completionHandler(nil)
            return
        }
        var next = request
        for name in Self.strippedOnRedirect { next.setValue(nil, forHTTPHeaderField: name) }
        completionHandler(next)
    }

    func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge,
                    completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        let space = challenge.protectionSpace
        guard space.authenticationMethod == NSURLAuthenticationMethodServerTrust, let serverTrust,
              let trust = space.serverTrust else {
            completionHandler(.performDefaultHandling, nil)
            return
        }
        if serverTrust(trust, space.host) {
            completionHandler(.useCredential, URLCredential(trust: trust))
        } else {
            completionHandler(.cancelAuthenticationChallenge, nil)
        }
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse,
                    completionHandler: @escaping (URLSession.ResponseDisposition) -> Void) {
        guard failure == nil else { return completionHandler(.cancel) }
        guard ArchiveDownloader.isAllowed(response.url) else {
            fail(.invalid, dataTask)
            return completionHandler(.cancel)
        }
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard status == 200 else {
            fail(.status(status), dataTask)
            return completionHandler(.cancel)
        }
        if response.expectedContentLength > 0, UInt64(response.expectedContentLength) > limit {
            fail(.invalid, dataTask)
            return completionHandler(.cancel)
        }
        let opened = open(destination, O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, 0o600)
        guard opened >= 0 else {
            fail(.io(errno), dataTask)
            return completionHandler(.cancel)
        }
        descriptor = opened
        completionHandler(.allow)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        guard failure == nil, descriptor >= 0 else { return }
        received += UInt64(data.count)
        guard received <= limit else { return fail(.invalid, dataTask) }
        let error: Int32 = data.withUnsafeBytes { buffer in
            var offset = 0
            while offset < buffer.count {
                let n = write(descriptor, buffer.baseAddress! + offset, buffer.count - offset)
                if n < 0 {
                    if errno == EINTR { continue }
                    return errno
                }
                offset += n
            }
            return 0
        }
        if error != 0 { fail(.io(error), dataTask) }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        if failure == nil, let error {
            failure = .transport((error as? URLError)?.code ?? .unknown)
        }
        if descriptor >= 0 {
            if close(descriptor) != 0, failure == nil { failure = .io(errno) }
            descriptor = -1
            if failure != nil { unlink(destination) }
        } else if failure == nil {
            // A response that never arrived cannot have succeeded.
            failure = .transport(.badServerResponse)
        }
        let continuation = self.continuation
        self.continuation = nil
        if let failure {
            continuation?.resume(throwing: failure)
        } else {
            continuation?.resume()
        }
    }
}
