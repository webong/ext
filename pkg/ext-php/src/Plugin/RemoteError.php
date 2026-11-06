<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/**
 * A deliberate public failure sent to the host as the structured error.
 *
 * The code (getCode(), a string, as PDOException's is) must match the identifier syntax, the
 * message is valid UTF-8 of at most 4096 bytes, and $retryAfterMs is an optional non-negative
 * bound of one year.
 */
class RemoteError extends \RuntimeException
{
    /** @var string */
    protected $code;

    public readonly ?int $retryAfterMs;

    public function __construct(mixed $code, mixed $message, mixed $retryAfterMs = null, ?\Throwable $previous = null)
    {
        if (!Pattern::identifier($code)) {
            throw new \InvalidArgumentException('error code must match [a-z0-9][a-z0-9._/-]{0,255}');
        }
        if (!\is_string($message) || \strlen($message) > MAX_MESSAGE_BYTES || !Text::isUtf8($message)) {
            throw new \InvalidArgumentException('error message must be UTF-8 of at most 4096 bytes');
        }
        if ($retryAfterMs !== null && (!\is_int($retryAfterMs) || $retryAfterMs < 0 || $retryAfterMs > MAX_RETRY_AFTER_MS)) {
            throw new \InvalidArgumentException('retryAfterMs must be an integer in [0, one year]');
        }
        parent::__construct($message, 0, $previous);
        $this->code = $code;
        $this->retryAfterMs = $retryAfterMs;
    }

    /** @return array{code: string, message: string, retryAfterMilliseconds?: int} */
    public function toWire(): array
    {
        $wire = ['code' => $this->code, 'message' => $this->message];
        if ($this->retryAfterMs) {
            $wire['retryAfterMilliseconds'] = $this->retryAfterMs;
        }
        return $wire;
    }
}
