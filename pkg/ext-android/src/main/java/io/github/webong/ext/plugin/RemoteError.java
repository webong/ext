package io.github.webong.ext.plugin;

import java.nio.charset.StandardCharsets;

/**
 * A failure the caller may see. Any other exception a handler throws is private
 * and becomes a generic operation_failed response.
 */
public final class RemoteError extends Exception {
    private static final long serialVersionUID = 1L;

    private final String code;
    private final long retryAfterMilliseconds;

    public RemoteError(String code, String message) {
        this(code, message, 0);
    }

    public RemoteError(String code, String message, long retryAfterMilliseconds) {
        super(message);
        this.code = code;
        this.retryAfterMilliseconds = retryAfterMilliseconds;
    }

    public String code() {
        return code;
    }

    public long retryAfterMilliseconds() {
        return retryAfterMilliseconds;
    }

    byte[] toJson() {
        StringBuilder out = new StringBuilder("{\"code\":");
        Json.quote(out, code).append(",\"message\":");
        Json.quote(out, getMessage() == null ? "" : getMessage());
        if (retryAfterMilliseconds > 0) {
            out.append(",\"retryAfterMilliseconds\":").append(retryAfterMilliseconds);
        }
        return out.append('}').toString().getBytes(StandardCharsets.UTF_8);
    }
}
