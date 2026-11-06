<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/**
 * One progress event: a "progress" count of $completed out of $total (zero when not known), or a
 * "log" line, optionally on a $stream ("out" or "err"). Text is at most 2048 bytes of UTF-8; use
 * splitProgressText() to cut a longer line. The members are not typed so that CallInfo::progress
 * can refuse an event that breaks the rules with FrameError "invalid".
 */
final class Progress
{
    public function __construct(
        public mixed $kind,
        public mixed $stream = '',
        public mixed $text = '',
        public mixed $completed = 0,
        public mixed $total = 0,
    ) {
    }

    public static function log(string $text): self
    {
        return new self('log', '', $text);
    }

    /** A log line of a program's standard output. */
    public static function out(string $text): self
    {
        return new self('log', 'out', $text);
    }

    /** A log line of a program's standard error. */
    public static function err(string $text): self
    {
        return new self('log', 'err', $text);
    }

    /** $completed of $total; zero when the total is not known. */
    public static function fraction(mixed $completed, mixed $total): self
    {
        return new self('progress', '', '', $completed, $total);
    }
}
