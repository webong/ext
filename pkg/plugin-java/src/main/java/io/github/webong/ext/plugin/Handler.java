package io.github.webong.ext.plugin;

/**
 * Runs one admitted call and returns its result payload as one JSON value. Throw
 * {@link RemoteError} for a failure callers may see; any other throwable is private.
 *
 * <p>Long-running handlers call {@link Call#checkDeadline()} so they stop at the
 * deadline; native code cannot be interrupted from outside.
 */
@FunctionalInterface
public interface Handler {
    byte[] handle(Call call) throws Exception;
}
