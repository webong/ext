package io.github.webong.ext.plugin;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URI;
import java.net.URISyntaxException;
import java.net.URL;
import java.util.LinkedHashMap;
import java.util.Locale;
import java.util.Map;
import java.util.Objects;
import javax.net.ssl.HttpsURLConnection;
import javax.net.ssl.SSLSocketFactory;

/**
 * Downloads a package archive over HTTPS into a new file, under the rules every ext binding
 * shares (lib/plugin/testdata/v1/download-cases.json): only {@code https} URLs with a host and
 * without user information or a fragment, for the first request and for every redirect; at most
 * {@link #MAX_REDIRECTS} redirects, followed here one by one so that each hop is checked;
 * {@code Authorization}, {@code Cookie} and {@code Referer} are not sent after a redirect; the body
 * is no larger than the limit, whatever {@code Content-Length} says; and a download that fails
 * leaves no file, and never touches one that was already there.
 *
 * <p>The content is not verified: pass the file and its pinned digest to {@link
 * PackageStore#installArchive}, which checks it before extracting anything. The download runs on
 * the calling thread; on Android that must not be the main thread.
 *
 * <p>A refusal by the rules is a {@link GuestException} with status {@link PackageStore#INVALID};
 * anything else (a status other than 200, a network or file failure, a destination that exists)
 * is an {@link IOException}. A {@link java.net.CookieHandler} the application installed process-wide
 * is still consulted by {@link HttpURLConnection}; this class adds no cookies of its own.
 */
public final class ArchiveDownload {
    /** How many redirects a download follows. */
    public static final int MAX_REDIRECTS = 5;
    /** The size limit when {@link Options#maxBytes()} is zero: 512 MiB, the store's archive default. */
    public static final long DEFAULT_MAX_BYTES = 512L << 20;

    /**
     * How to download. {@code maxBytes} bounds the body (zero is {@link #DEFAULT_MAX_BYTES});
     * {@code headers} go with the first request, and those but {@code Authorization}, {@code Cookie}
     * and {@code Referer} with every redirected one; {@code timeoutMillis} bounds connecting and each
     * read (zero waits forever); {@code socketFactory}, when not null, replaces the platform's TLS
     * sockets, for example to pin or to trust a private authority.
     */
    public record Options(long maxBytes, Map<String, String> headers, int timeoutMillis,
            SSLSocketFactory socketFactory) {
        /** The default limit, no headers, 60 seconds and the platform's TLS. */
        public static final Options DEFAULT = new Options(0, Map.of(), 60_000, null);

        public Options {
            if (maxBytes < 0 || timeoutMillis < 0) {
                throw new IllegalArgumentException("a download limit cannot be negative");
            }
            headers = Map.copyOf(Objects.requireNonNull(headers, "headers"));
        }
    }

    private ArchiveDownload() {}

    /** As {@link #download(String, File, Options)} with {@link Options#DEFAULT}. */
    public static void download(String source, File destination) throws GuestException, IOException {
        download(source, destination, Options.DEFAULT);
    }

    /** Downloads {@code source} into {@code destination}, which must not exist. */
    public static void download(String source, File destination, Options options)
            throws GuestException, IOException {
        Objects.requireNonNull(source, "source");
        Objects.requireNonNull(destination, "destination");
        Objects.requireNonNull(options, "options");
        long limit = options.maxBytes() == 0 ? DEFAULT_MAX_BYTES : options.maxBytes();
        URI current = checked(source);
        Map<String, String> headers = new LinkedHashMap<>(options.headers());
        for (int redirects = 0; ; redirects++) {
            HttpsURLConnection connection = open(current, headers, options);
            try {
                int status = connection.getResponseCode();
                if (isRedirect(status)) {
                    String location = connection.getHeaderField("Location");
                    if (redirects >= MAX_REDIRECTS) {
                        throw new GuestException(PackageStore.INVALID);
                    }
                    if (location == null) {
                        throw new IOException("download: HTTP " + status + " without a Location");
                    }
                    try {
                        current = checked(current.resolve(new URI(location)).toString());
                    } catch (URISyntaxException e) {
                        throw new GuestException(PackageStore.INVALID);
                    }
                    headers.keySet().removeIf(ArchiveDownload::dropOnRedirect);
                    continue;
                }
                if (status != HttpURLConnection.HTTP_OK) {
                    throw new IOException("download: origin returned HTTP " + status);
                }
                if (connection.getContentLengthLong() > limit) {
                    throw new GuestException(PackageStore.INVALID);
                }
                try (InputStream body = connection.getInputStream()) {
                    save(body, destination, limit);
                }
                return;
            } finally {
                connection.disconnect();
            }
        }
    }

    private static HttpsURLConnection open(URI uri, Map<String, String> headers, Options options)
            throws IOException {
        URL url = uri.toURL();
        if (!(url.openConnection() instanceof HttpsURLConnection connection)) {
            throw new IOException("download: no HTTPS connection for " + uri.getScheme());
        }
        if (options.socketFactory() != null) {
            connection.setSSLSocketFactory(options.socketFactory());
        }
        connection.setInstanceFollowRedirects(false);
        connection.setUseCaches(false);
        connection.setConnectTimeout(options.timeoutMillis());
        connection.setReadTimeout(options.timeoutMillis());
        // An encoded body would make the limit count something other than the file.
        connection.setRequestProperty("Accept-Encoding", "identity");
        headers.forEach(connection::setRequestProperty);
        return connection;
    }

    /** Writes the body into a file that this call creates, and removes that file on any failure. */
    private static void save(InputStream body, File destination, long limit) throws GuestException, IOException {
        if (!destination.createNewFile()) {
            throw new IOException("download: destination exists: " + destination);
        }
        boolean done = false;
        try {
            destination.setReadable(false, false);
            destination.setWritable(false, false);
            destination.setReadable(true, true);
            destination.setWritable(true, true);
            try (OutputStream out = new FileOutputStream(destination)) {
                byte[] buffer = new byte[64 * 1024];
                long written = 0;
                for (int n; (n = body.read(buffer)) >= 0; ) {
                    written += n;
                    if (written > limit) {
                        throw new GuestException(PackageStore.INVALID);
                    }
                    out.write(buffer, 0, n);
                }
            }
            done = true;
        } finally {
            if (!done) {
                destination.delete();
            }
        }
    }

    /** The URL when the rules allow it; a refusal otherwise. */
    private static URI checked(String text) throws GuestException {
        URI uri;
        try {
            uri = new URI(text);
        } catch (URISyntaxException e) {
            throw new GuestException(PackageStore.INVALID);
        }
        String scheme = uri.getScheme();
        if (scheme == null || !scheme.toLowerCase(Locale.ROOT).equals("https") || uri.getHost() == null
                || uri.getHost().isEmpty() || uri.getRawUserInfo() != null || uri.getRawFragment() != null) {
            throw new GuestException(PackageStore.INVALID);
        }
        return uri;
    }

    private static boolean isRedirect(int status) {
        return status == 301 || status == 302 || status == 303 || status == 307 || status == 308;
    }

    private static boolean dropOnRedirect(String header) {
        String name = header.toLowerCase(Locale.ROOT);
        return name.equals("authorization") || name.equals("cookie") || name.equals("referer");
    }
}
