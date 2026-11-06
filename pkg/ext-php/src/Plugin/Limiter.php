<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/**
 * The sender's rate limit: a token bucket counted in events and bytes of text, with a burst of
 * twice the rate, and a count of events dropped since the last one that passed. Time is a
 * monotonic clock in nanoseconds, given by the caller.
 *
 * @internal
 */
final class Limiter
{
    private float $eventRate;
    private float $byteRate;
    private float $events;
    private float $bytes;
    private int $last;
    private int $dropped = 0;

    public function __construct(int $eventsPerSecond = 0, int $bytesPerSecond = 0, int $nowNs = 0)
    {
        $this->eventRate = (float) ($eventsPerSecond > 0 ? $eventsPerSecond : DEFAULT_PROGRESS_EVENTS);
        $this->byteRate = (float) ($bytesPerSecond > 0 ? $bytesPerSecond : DEFAULT_PROGRESS_BYTES);
        $this->events = 2 * $this->eventRate;
        $this->bytes = 2 * $this->byteRate;
        $this->last = $nowNs;
    }

    /**
     * The number of events dropped since the previous one that passed (which the caller attaches
     * to this event), or null when the rate limit drops it.
     */
    public function admit(int $textBytes, int $nowNs): ?int
    {
        $delta = $nowNs - $this->last;
        // Seconds as Go's Duration.Seconds computes them.
        $whole = intdiv($delta, 1_000_000_000);
        $elapsed = (float) $whole + (float) ($delta - $whole * 1_000_000_000) / 1e9;
        if ($elapsed > 0) {
            $this->events = min($this->events + $elapsed * $this->eventRate, 2 * $this->eventRate);
            $this->bytes = min($this->bytes + $elapsed * $this->byteRate, 2 * $this->byteRate);
            $this->last = $nowNs;
        }
        if ($this->events < 1 || $this->bytes < $textBytes) {
            $this->dropped++;
            return null;
        }
        $this->events -= 1;
        $this->bytes -= $textBytes;
        $reported = $this->dropped;
        $this->dropped = 0;
        return $reported;
    }

    /** The drops not yet reported, reset to zero. */
    public function pending(): int
    {
        $reported = $this->dropped;
        $this->dropped = 0;
        return $reported;
    }
}
