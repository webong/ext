package io.github.webong.ext.plugin;

/** The rate at which a call may report progress, as the host set it (defaults filled in). */
public record ProgressLimits(long eventsPerSecond, long bytesPerSecond) {}
