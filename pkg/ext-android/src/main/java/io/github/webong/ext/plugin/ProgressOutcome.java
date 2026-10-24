package io.github.webong.ext.plugin;

/** What became of a progress event the call reported. */
public enum ProgressOutcome {
    /** Written to the host. */
    SENT,
    /** Dropped by the rate limit, which is routine: the next event that passes carries the count. */
    DROPPED
}
