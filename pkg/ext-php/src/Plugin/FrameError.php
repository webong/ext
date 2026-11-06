<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/**
 * A progress or reverse-call operation failed locally (a host's refusal is a RemoteError).
 *
 * getCode() is "unsupported" (no reporter or caller on this call), "invalid" (the event or
 * request breaks the rules; nothing was sent), "closed" (the call is over or the connection
 * ended), or "protocol" (the host broke the framing rules; the session ends).
 */
class FrameError extends \RuntimeException
{
    /** @var string */
    protected $code;

    public function __construct(string $code, ?string $message = null)
    {
        parent::__construct($message ?? $code);
        $this->code = $code;
    }
}
