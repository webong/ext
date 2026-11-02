package io.github.webong.ext.system;

import java.util.Objects;

public final class PowerInfo {
    private final String state; // charging, discharging, charged, unknown
    private final int percentage; // 0-100
    private final Long timeRemainingSeconds;

    public PowerInfo(String state, int percentage, Long timeRemainingSeconds) {
        if (state == null || state.isEmpty()) throw new IllegalArgumentException("power state is required");
        if (percentage < 0 || percentage > 100) throw new IllegalArgumentException("power percentage must be between 0 and 100");
        this.state = state;
        this.percentage = percentage;
        this.timeRemainingSeconds = timeRemainingSeconds;
    }

    public String state() { return state; }
    public int percentage() { return percentage; }
    public Long timeRemainingSeconds() { return timeRemainingSeconds; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof PowerInfo)) return false;
        PowerInfo value = (PowerInfo) other;
        return state.equals(value.state) && percentage == value.percentage && Objects.equals(timeRemainingSeconds, value.timeRemainingSeconds);
    }
    @Override public int hashCode() { return Objects.hash(state, percentage, timeRemainingSeconds); }
}
