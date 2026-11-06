<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/**
 * Details of the call being handled, for handlers that need them.
 *
 * On a transport that carries frames (a guest served by serve()) the call can also report
 * progress and call its host: see progress(), $progressLimits and $reverse. This guest reads
 * inline, so $sessionReverse is always null and a handler makes one reverse call at a time.
 */
final class CallInfo
{
    /** The rate the host set for this call's events, or null when the operation reports none. */
    public ?ProgressLimits $progressLimits = null;

    /** Calls host operations, bound by the call's deadline; null on a transport with no frames. */
    public ?ReverseCaller $reverse = null;

    /** Always null here: nobody reads the connection between calls. */
    public readonly ?ReverseCaller $sessionReverse;

    /** @internal */
    public ?Reporter $reporter = null;

    /**
     * @param float $deadline seconds since the epoch (microtime(true) scale)
     */
    public function __construct(
        public readonly string $requestId,
        public readonly string $contractName,
        public readonly string $contractVersion,
        public readonly string $operation,
        public readonly string $surface,
        public readonly float $deadline,
    ) {
        $this->sessionReverse = null;
    }

    /** Seconds left before the deadline (negative once it has passed). */
    public function remaining(): float
    {
        return $this->deadline - microtime(true);
    }

    /**
     * Reports a progress event to the host while the call runs. Events reach the host in order,
     * before the result. Returns SENT, or DROPPED when the rate limit dropped it (routine: the
     * next event that passes carries the count). Throws FrameError: "unsupported" when the
     * operation did not declare progress, the host asked for no events or there is no frame
     * channel (stop reporting), "invalid" for an event that breaks the rules (nothing is sent),
     * "closed" once the call is over.
     */
    public function progress(mixed $event): string
    {
        if ($this->reporter === null) {
            throw new FrameError('unsupported', 'no progress channel on this call');
        }
        return $this->reporter->report($event);
    }
}
