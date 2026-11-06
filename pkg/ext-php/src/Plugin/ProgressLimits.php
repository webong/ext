<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/** The rate the host set for a call's progress events. */
final class ProgressLimits
{
    public function __construct(
        public readonly int $eventsPerSecond,
        public readonly int $bytesPerSecond,
    ) {
    }
}
