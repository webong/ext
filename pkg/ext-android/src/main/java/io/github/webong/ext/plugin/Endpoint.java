package io.github.webong.ext.plugin;

import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;

/**
 * Loopback data endpoints: the engine's rules for the endpoint a host hands a plugin
 * (endpoint.check and endpoint.validate, see lib/plugin/services.md). A plugin checks
 * what it receives before dialing; a host validates what it grants. Granting one (its
 * lifetime, one-time tokens, dialing) stays with the caller.
 *
 * <p>An outcome is a status and never text, because an endpoint is a credential: the
 * {@link GuestException} carries {@link #INVALID}, {@link #DENIED} or {@link #EXPIRED}
 * as its {@link GuestException#status()}, and nothing of the endpoint.
 */
public final class Endpoint {
    /** Malformed: unknown or duplicate fields, an invalid part, or an address that is not loopback. */
    public static final int INVALID = 1;
    /** Well formed, but the policy does not admit it (scheme, lifetime). */
    public static final int DENIED = 2;
    /** Its expiry has already passed. */
    public static final int EXPIRED = 17;

    private Endpoint() {}

    /**
     * Checks the shape rules alone: strict decoding, scheme, loopback address, path,
     * token and expiry. {@code endpoint} is the endpoint as UTF-8 JSON.
     */
    public static void check(byte[] endpoint) throws GuestException {
        call("endpoint.check", endpoint);
    }

    /**
     * Checks the shape rules and the policy, the JSON of {@code {"schemes":[...],
     * "maxLifetimeMilliseconds":n,"now":"RFC 3339"}} with every field optional (a
     * policy that names no scheme admits nothing).
     */
    public static void validate(byte[] endpoint, byte[] policy) throws GuestException {
        ByteArrayOutputStream input = new ByteArrayOutputStream(endpoint.length + policy.length + 32);
        input.writeBytes("{\"endpoint\":".getBytes(StandardCharsets.UTF_8));
        input.writeBytes(endpoint);
        input.writeBytes(",\"policy\":".getBytes(StandardCharsets.UTF_8));
        input.writeBytes(policy);
        input.write('}');
        call("endpoint.validate", input.toByteArray());
    }

    private static void call(String operation, byte[] input) throws GuestException {
        int status = NativeEngine.endpoint(operation, input);
        if (status != 0) {
            throw new GuestException(status);
        }
    }
}
